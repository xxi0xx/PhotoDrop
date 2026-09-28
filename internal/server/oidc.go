package server

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"photodrop/internal/abuse"
	"photodrop/internal/auth"
)

const oidcCookie = "photodrop_oidc"

func (a *application) authMethods(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, struct {
		Password bool `json:"password"`
		OIDC     bool `json:"oidc"`
	}{a.passwordEnabled, a.oidc != nil})
}
func (a *application) oidcRoute(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			apiError(w, 405, "method_not_allowed", "Method not allowed", nil)
			return
		}
		if a.oidc == nil {
			apiError(w, 404, "not_found", "Not found", nil)
			return
		}
		if !a.security.RateDisabled {
			ip, _ := abuse.ClientIP(r, a.security.TrustedProxies)
			for key, rate := range map[string]int{"global:oidc": 60, "oidc:" + ip.String(): 20} {
				if ok, retry := a.limiter.Allow(key, rate*a.security.RateMultiplier); !ok {
					a.rateFailure(w, "oidc", retry)
					return
				}
			}
		}
		select {
		case a.oidcSlots <- struct{}{}:
			defer func() { <-a.oidcSlots }()
		default:
			a.oidcFailure(w, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		next(w, r.WithContext(ctx))
	}
}
func (a *application) bindingCookie(w http.ResponseWriter, r *http.Request, value string, age int) {
	expires := time.Now().Add(auth.OIDCLifetime)
	if age < 0 {
		expires = time.Unix(1, 0)
	}
	http.SetCookie(w, &http.Cookie{Name: oidcCookie, Value: value, Path: "/api/admin/oidc", HttpOnly: true, Secure: strings.HasPrefix(a.baseURL, "https://") || r.TLS != nil, SameSite: http.SameSiteLaxMode, MaxAge: age, Expires: expires})
}
func binding(r *http.Request) string {
	c, err := r.Cookie(oidcCookie)
	if err != nil {
		return ""
	}
	return c.Value
}
func (a *application) oidcFailure(w http.ResponseWriter, r *http.Request) {
	a.bindingCookie(w, r, "", -1)
	a.logger.Info("OIDC sign-in failed")
	http.Redirect(w, r, "/admin/login?error=oidc", http.StatusSeeOther)
}
func (a *application) oidcLogin(w http.ResponseWriter, r *http.Request) {
	location, secret, err := a.oidc.Begin(r.Context(), binding(r))
	if err != nil {
		a.oidcFailure(w, r)
		return
	}
	a.bindingCookie(w, r, secret, int(auth.OIDCLifetime.Seconds()))
	http.Redirect(w, r, location, http.StatusSeeOther)
}
func (a *application) oidcCallback(w http.ResponseWriter, r *http.Request) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	// Reject duplicate security parameters and provider errors without echoing them.
	for _, key := range []string{"state", "code", "error", "iss"} {
		if len(query[key]) > 1 {
			err = auth.ErrOIDC
		}
	}
	if issuer := query.Get("iss"); issuer != "" { // issuer parameter is optional; token issuer is always verified.
		// The transaction/code/token checks remain authoritative. Never use this URL as a destination.
		if !a.oidc.MatchesIssuer(issuer) {
			err = auth.ErrOIDC
		}
	}
	if err == nil {
		err = a.oidc.Complete(r.Context(), query.Get("state"), binding(r), query.Get("code"), query.Has("error"))
	}
	if err != nil {
		a.oidcFailure(w, r)
		return
	}
	token, session, err := a.auth.CreateSession(r.Context(), cookieToken(r))
	if err != nil {
		a.oidcFailure(w, r)
		return
	}
	a.bindingCookie(w, r, "", -1)
	a.setCookie(w, r, token, session.ExpiresAt, int(auth.SessionLifetime.Seconds()))
	a.logger.Info("admin OIDC login succeeded")
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}
