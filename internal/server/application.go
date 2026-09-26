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

	"photodrop/internal/abuse"
	"photodrop/internal/auth"
	"photodrop/internal/config"
	"photodrop/internal/events"
	"photodrop/internal/integrations/immich"
	"photodrop/internal/media"
	"photodrop/internal/storage"
)

const sessionCookie = "photodrop_session"

type application struct {
	events         *events.Store
	auth           *auth.Manager
	media          *media.Service
	immich         *immich.Service
	maxFileSize    int64
	baseURL        string
	index          []byte
	logger         *slog.Logger
	security       config.Security
	limiter        *abuse.Limiter
	verifier       abuse.Verifier
	challengeSlots chan struct{}
	loginSlots     chan struct{}
}

func (a *application) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/login", func(w http.ResponseWriter, r *http.Request) { a.page(w, http.StatusOK) })
	for _, path := range []string{"/admin", "/admin/{$}", "/admin/events/new", "/admin/events/{id}"} {
		mux.HandleFunc("GET "+path, a.adminPage)
	}
	mux.HandleFunc("GET /e/{public_id}", a.publicPage)
	mux.HandleFunc("GET /api/public/events/{public_id}", a.publicEvent)
	mux.HandleFunc("POST /api/public/events/{public_id}/upload-sessions", a.guarded("session", a.createUploadSession))
	mux.HandleFunc("POST /api/public/events/{public_id}/upload-sessions/{session_id}/assets", a.guarded("local", a.uploadAsset))
	mux.HandleFunc("POST /api/public/events/{public_id}/upload-sessions/{session_id}/assets/prepare", a.guarded("prepare", a.prepareAsset))
	mux.HandleFunc("POST /api/public/events/{public_id}/upload-sessions/{session_id}/assets/{asset_id}/authorize", a.guarded("authorize", a.authorizeAsset))
	mux.HandleFunc("POST /api/public/events/{public_id}/upload-sessions/{session_id}/assets/{asset_id}/complete", a.guarded("complete", a.completeAsset))
	mux.HandleFunc("POST /api/admin/login", a.guarded("login", a.login))
	mux.HandleFunc("POST /api/admin/logout", a.protected(a.logout))
	mux.HandleFunc("GET /api/admin/session", a.protected(func(w http.ResponseWriter, r *http.Request, s auth.Session) { writeJSON(w, 200, s) }))
	mux.HandleFunc("GET /api/admin/events", a.protected(a.listEvents))
	mux.HandleFunc("POST /api/admin/events", a.protected(a.createEvent))
	mux.HandleFunc("GET /api/admin/events/{id}", a.protected(a.getEvent))
	mux.HandleFunc("PUT /api/admin/events/{id}", a.protected(a.updateEvent))
	mux.HandleFunc("DELETE /api/admin/events/{id}", a.protected(a.deleteEvent))
	mux.HandleFunc("GET /api/admin/events/{id}/immich", a.protected(a.immichStatus))
	mux.HandleFunc("POST /api/admin/events/{id}/immich/test", a.protected(a.immichTest))
	mux.HandleFunc("POST /api/admin/events/{id}/immich/jobs", a.protected(a.immichStart))
	mux.HandleFunc("POST /api/admin/events/{id}/immich/cancel", a.protected(a.immichCancel))
	for path, methods := range map[string]string{
		"/api/admin/events/{id}/immich": "GET, HEAD", "/api/admin/events/{id}/immich/test": "POST", "/api/admin/events/{id}/immich/jobs": "POST", "/api/admin/events/{id}/immich/cancel": "POST",
		"/api/admin/login": "POST", "/api/admin/logout": "POST", "/api/admin/session": "GET, HEAD",
		"/api/admin/events": "GET, HEAD, POST", "/api/admin/events/{id}": "GET, HEAD, PUT, DELETE", "/api/public/events/{public_id}": "GET, HEAD",
		"/api/public/events/{public_id}/upload-sessions":                                          "POST",
		"/api/public/events/{public_id}/upload-sessions/{session_id}/assets":                      "POST",
		"/api/public/events/{public_id}/upload-sessions/{session_id}/assets/prepare":              "POST",
		"/api/public/events/{public_id}/upload-sessions/{session_id}/assets/{asset_id}/authorize": "POST",
		"/api/public/events/{public_id}/upload-sessions/{session_id}/assets/{asset_id}/complete":  "POST",
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
	var tooLarge *http.MaxBytesError
	switch {
	case errors.Is(err, media.ErrContributor):
		apiError(w, 422, "contributor_name", media.ErrContributor.Error(), map[string]string{"contributor_name": media.ErrContributor.Error()})
	case errors.Is(err, media.ErrExpired):
		a.logger.Info("upload session expired")
		apiError(w, 409, "session_expired", media.ErrExpired.Error(), nil)
	case errors.Is(err, media.ErrEventQuota):
		a.logger.Info("event quota reached")
		apiError(w, 409, "event_quota", media.ErrEventQuota.Error(), nil)
	case errors.Is(err, media.ErrSessionQuota):
		a.logger.Info("session quota reached")
		apiError(w, 409, "session_quota", media.ErrSessionQuota.Error(), nil)
	case errors.Is(err, abuse.ErrChallengeUnavailable):
		a.logger.Info("Turnstile service unavailable")
		apiError(w, 503, "verification_unavailable", abuse.ErrChallengeUnavailable.Error(), nil)
	case errors.Is(err, abuse.ErrChallenge), errors.Is(err, abuse.ErrChallengeExpired):
		a.logger.Info("Turnstile verification failed")
		apiError(w, 403, "verification_failed", err.Error(), nil)
	case errors.Is(err, media.ErrStrategy):
		apiError(w, 409, "upload_strategy", media.ErrStrategy.Error(), nil)
	case errors.Is(err, media.ErrAsset):
		apiError(w, 404, "asset_not_found", "This upload attempt is no longer available. Retry to start a new session.", nil)
	case errors.Is(err, media.ErrReady):
		apiError(w, 409, "asset_ready", media.ErrReady.Error(), nil)
	case errors.Is(err, media.ErrRequest):
		apiError(w, 422, "upload_metadata", media.ErrRequest.Error(), nil)
	case errors.Is(err, storage.ErrMissing):
		apiError(w, 409, "object_missing", "The upload has not arrived. Please retry this file.", nil)
	case errors.Is(err, storage.ErrObjectChanged):
		apiError(w, 409, "object_changed", "The upload could not be verified. Please retry this file.", nil)
	case errors.Is(err, storage.ErrBackend), errors.Is(err, storage.ErrUnavailable):
		a.logger.Error("object storage operation unavailable", "error", err)
		apiError(w, 503, "storage_unavailable", "Media storage is temporarily unavailable. Please try again later.", nil)
	case errors.Is(err, media.ErrClosed):
		apiError(w, 409, "event_closed", "This event is no longer accepting uploads.", nil)
	case errors.Is(err, media.ErrSession):
		apiError(w, 404, "upload_session_not_found", "This upload session is no longer available. Retry to start a new session.", nil)
	case errors.Is(err, media.ErrFilename):
		apiError(w, 422, "invalid_filename", "Provide a filename of 1 to 255 valid characters.", nil)
	case errors.Is(err, media.ErrType):
		apiError(w, 415, "unsupported_image", "This file does not appear to be supported media.", nil)
	case errors.Is(err, media.ErrEmpty):
		apiError(w, 422, "empty_file", "Empty files cannot be uploaded.", nil)
	case errors.Is(err, storage.ErrTooLarge), errors.As(err, &tooLarge):
		apiError(w, 413, "file_too_large", "This file is larger than the upload limit.", nil)
	case errors.Is(err, media.ErrSize):
		apiError(w, 400, "size_mismatch", "The complete file was not received. Please try again.", nil)
	case errors.Is(err, media.ErrBusy):
		apiError(w, 409, "uploads_active", media.ErrBusy.Error(), nil)
	case errors.Is(err, media.ErrDeleting):
		apiError(w, 500, "media_cleanup_failed", media.ErrDeleting.Error(), nil)
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
		if a.logger != nil {
			a.logger.Info("foreign or missing origin rejected")
		}
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
	select {
	case a.loginSlots <- struct{}{}:
		defer func() { <-a.loginSlots }()
	default:
		a.rateFailure(w, "login", 1)
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
	if !emptyControl(w, r) {
		return
	}
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
	Status       string              `json:"status"`
	PublicURL    string              `json:"public_url"`
	Media        media.Stats         `json:"media"`
	Contributors []media.Contributor `json:"contributors,omitempty"`
}

func (a *application) adminEvent(e events.Event) adminEvent {
	// Validated base URLs have no subpath. With no configured origin, return a
	// relative URL; never construct absolute public links from Host headers.
	return adminEvent{Event: e, Status: e.Status(time.Now()), PublicURL: a.baseURL + "/e/" + e.PublicID}
}

func (a *application) listEvents(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	list, err := a.events.List(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	result := make([]adminEvent, 0, len(list))
	stats, err := a.media.Stats(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	for _, e := range list {
		item := a.adminEvent(e)
		item.Media = stats[e.ID]
		result = append(result, item)
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
	a.writeAdminEvent(w, r, e)
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
	a.writeAdminEvent(w, r, e)
}

func (a *application) deleteEvent(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	if !emptyControl(w, r) {
		return
	}
	id := internalID(r)
	if err := a.media.DeleteEvent(r.Context(), id); err != nil {
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
		MaxFileSize int64   `json:"max_file_size,omitempty"`
		Challenge   *struct {
			SiteKey string `json:"site_key"`
			Action  string `json:"action"`
		} `json:"challenge,omitempty"`
	}
	guest := guestEvent{Name: e.Name, Status: "closed"}
	if e.Status(time.Now()) == "open" {
		guest.Status = "open"
		guest.Description = e.Description
		guest.EventDate = e.EventDate
		guest.MaxFileSize = a.maxFileSize
		if a.security.TurnstileSiteKey != "" {
			guest.Challenge = &struct {
				SiteKey string `json:"site_key"`
				Action  string `json:"action"`
			}{a.security.TurnstileSiteKey, abuse.ChallengeAction}
		}
	}
	writeJSON(w, 200, map[string]any{"event": guest})
}
