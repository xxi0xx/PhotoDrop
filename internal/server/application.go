package server

import (
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"photodrop/internal/auth"
	"photodrop/internal/events"
)

const sessionCookie = "photodrop_session"

type application struct {
	events  *events.Store
	auth    *auth.Manager
	baseURL string
	index   []byte
	logger  *slog.Logger
}

func (a *application) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) { a.page(w, http.StatusOK) })
	for _, path := range []string{"/admin", "/admin/{$}", "/admin/events/new", "/admin/events/{id}"} {
		mux.HandleFunc("GET "+path, a.adminPage)
	}
	mux.HandleFunc("GET /e/{public_id}", a.publicPage)
	mux.HandleFunc("GET /api/public/events/{public_id}", a.publicEvent)
	mux.HandleFunc("POST /api/admin/login", a.login)
	mux.HandleFunc("POST /api/admin/logout", a.protected(a.logout))
	mux.HandleFunc("GET /api/admin/session", a.protected(func(w http.ResponseWriter, r *http.Request, s auth.Session) { writeJSON(w, 200, s) }))
	mux.HandleFunc("GET /api/admin/events", a.protected(a.listEvents))
	mux.HandleFunc("POST /api/admin/events", a.protected(a.createEvent))
	mux.HandleFunc("GET /api/admin/events/{id}", a.protected(a.getEvent))
	mux.HandleFunc("PUT /api/admin/events/{id}", a.protected(a.updateEvent))
	mux.HandleFunc("DELETE /api/admin/events/{id}", a.protected(a.deleteEvent))
	for path, methods := range map[string]string{
		"/api/admin/login": "POST", "/api/admin/logout": "POST", "/api/admin/session": "GET, HEAD",
		"/api/admin/events": "GET, HEAD, POST", "/api/admin/events/{id}": "GET, HEAD, PUT, DELETE", "/api/public/events/{public_id}": "GET, HEAD",
	} {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Allow", methods)
			apiError(w, 405, "method_not_allowed", "Method not allowed", nil)
		})
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { apiError(w, 404, "not_found", "Not found", nil) })
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(value)
}

func apiError(w http.ResponseWriter, status int, code, message string, fields map[string]string) {
	type detail struct {
		Code    string            `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields,omitempty"`
	}
	writeJSON(w, status, struct {
		Error detail `json:"error"`
	}{detail{code, message, fields}})
}

func (a *application) fail(w http.ResponseWriter, err error) {
	var validation *events.ValidationError
	switch {
	case errors.Is(err, sql.ErrNoRows):
		apiError(w, 404, "not_found", "Event not found", nil)
	case errors.As(err, &validation):
		apiError(w, 422, "validation", validation.Error(), validation.Fields)
	default:
		a.logger.Error("request failed", "error", err)
		apiError(w, 500, "internal_error", "Something went wrong. Please try again.", nil)
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		apiError(w, 415, "content_type", "Use application/json", nil)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(dst)
	if err == nil {
		var extra any
		if next := decoder.Decode(&extra); next != io.EOF {
			if next != nil {
				err = next
			} else {
				err = errors.New("multiple JSON values")
			}
		}
	}
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			apiError(w, 413, "body_too_large", "Request body is too large", nil)
		} else {
			apiError(w, 400, "invalid_json", "Invalid JSON body or unsupported field", nil)
		}
		return false
	}
	return true
}

func cookieToken(r *http.Request) string {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// Unsafe requests require a same-origin Origin (or a Referer fallback).
// Authenticated mutations additionally require a synchronizer CSRF token.
// Missing origin evidence is rejected, including for login; no CORS is enabled.
func (a *application) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	expected := a.baseURL
	if expected == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		expected = scheme + "://" + r.Host
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		u, err := url.Parse(r.Header.Get("Referer"))
		if err == nil && u.User == nil && u.Host != "" {
			origin = u.Scheme + "://" + u.Host
		}
	}
	if origin != expected || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
		apiError(w, 403, "csrf", "Request origin was not accepted", nil)
		return false
	}
	return true
}

func (a *application) protected(next func(http.ResponseWriter, *http.Request, auth.Session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		session, err := a.auth.Lookup(r.Context(), cookieToken(r))
		if errors.Is(err, auth.ErrUnauthorized) {
			apiError(w, 401, "unauthorized", "Sign in to continue", nil)
			return
		}
		if err != nil {
			a.fail(w, err)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !a.sameOrigin(w, r) {
				return
			}
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(session.CSRFToken)) != 1 {
				apiError(w, 403, "csrf", "Invalid CSRF token", nil)
				return
			}
		}
		next(w, r, session)
	}
}

func (a *application) setCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true,
		Secure: strings.HasPrefix(a.baseURL, "https://") || r.TLS != nil, SameSite: http.SameSiteLaxMode, Expires: expires, MaxAge: maxAge})
}

func (a *application) login(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	token, session, err := a.auth.Login(r.Context(), input.Password, cookieToken(r))
	if errors.Is(err, auth.ErrUnauthorized) {
		a.logger.Info("admin login failed")
		apiError(w, 401, "invalid_password", "Invalid password", nil)
		return
	}
	if err != nil {
		a.fail(w, err)
		return
	}
	a.setCookie(w, r, token, session.ExpiresAt, int(auth.SessionLifetime.Seconds()))
	a.logger.Info("admin login succeeded")
	writeJSON(w, 200, session)
}

func (a *application) logout(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if err := a.auth.Logout(r.Context(), cookieToken(r)); err != nil {
		a.fail(w, err)
		return
	}
	a.setCookie(w, r, "", time.Unix(1, 0), -1)
	a.logger.Info("admin logout")
	w.WriteHeader(http.StatusNoContent)
}

type adminEvent struct {
	events.Event
	Status    string `json:"status"`
	PublicURL string `json:"public_url"`
}

func (a *application) adminEvent(e events.Event) adminEvent {
	// Validated base URLs have no subpath. With no configured origin, return a
	// relative URL; never construct absolute public links from Host headers.
	return adminEvent{e, e.Status(time.Now()), a.baseURL + "/e/" + e.PublicID}
}

func (a *application) listEvents(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	list, err := a.events.List(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	result := make([]adminEvent, 0, len(list))
	for _, e := range list {
		result = append(result, a.adminEvent(e))
	}
	writeJSON(w, 200, map[string]any{"events": result})
}

func internalID(r *http.Request) int64 {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		return 0
	}
	return id
}

func (a *application) getEvent(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	e, err := a.events.Get(r.Context(), internalID(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"event": a.adminEvent(e)})
}

func (a *application) createEvent(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var input events.Input
	if !decodeJSON(w, r, &input, 32768) {
		return
	}
	e, err := a.events.Create(r.Context(), input)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.logger.Info("event created", "event_id", e.ID)
	w.Header().Set("Location", "/api/admin/events/"+strconv.FormatInt(e.ID, 10))
	writeJSON(w, 201, map[string]any{"event": a.adminEvent(e)})
}

func (a *application) updateEvent(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var input events.Input
	if !decodeJSON(w, r, &input, 32768) {
		return
	}
	e, err := a.events.Update(r.Context(), internalID(r), input)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.logger.Info("event updated", "event_id", e.ID)
	writeJSON(w, 200, map[string]any{"event": a.adminEvent(e)})
}

func (a *application) deleteEvent(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	id := internalID(r)
	if err := a.events.Delete(r.Context(), id); err != nil {
		a.fail(w, err)
		return
	}
	a.logger.Info("event deleted", "event_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func (a *application) page(w http.ResponseWriter, status int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	w.Write(a.index)
}

func (a *application) adminPage(w http.ResponseWriter, r *http.Request) {
	_, err := a.auth.Lookup(r.Context(), cookieToken(r))
	if errors.Is(err, auth.ErrUnauthorized) {
		http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
		return
	}
	if err != nil {
		a.logger.Error("load admin page", "error", err)
		http.Error(w, "Unable to load this page", 500)
		return
	}
	a.page(w, 200)
}

func (a *application) publicPage(w http.ResponseWriter, r *http.Request) {
	_, err := a.events.GetPublic(r.Context(), r.PathValue("public_id"))
	if errors.Is(err, sql.ErrNoRows) {
		a.page(w, 404)
		return
	}
	if err != nil {
		a.logger.Error("load public page", "error", err)
		http.Error(w, "Unable to load this page", 500)
		return
	}
	a.page(w, 200)
}

func (a *application) publicEvent(w http.ResponseWriter, r *http.Request) {
	e, err := a.events.GetPublic(r.Context(), r.PathValue("public_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	type guestEvent struct {
		Name        string  `json:"name"`
		Status      string  `json:"status"`
		Description string  `json:"description,omitempty"`
		EventDate   *string `json:"event_date,omitempty"`
	}
	guest := guestEvent{Name: e.Name, Status: "closed"}
	if e.Status(time.Now()) == "open" {
		guest.Status = "open"
		guest.Description = e.Description
		guest.EventDate = e.EventDate
	}
	writeJSON(w, 200, map[string]any{"event": guest})
}
