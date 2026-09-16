package server

import (
	"context"
	"log/slog"
	"net/http"
	"photodrop/internal/abuse"
	"photodrop/internal/media"
	"strconv"
	"time"
)

func emptyControl(w http.ResponseWriter, r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	var input struct{}
	return decodeJSON(w, r, &input, 1024)
}
func (a *application) rateFailure(w http.ResponseWriter, scope string, retry int) {
	a.logger.Info("rate limit triggered", "scope", scope)
	w.Header().Set("Retry-After", strconv.Itoa(retry))
	unit := " seconds"
	if retry == 1 {
		unit = " second"
	}
	apiError(w, 429, "rate_limited", "Too many requests. Please wait "+strconv.Itoa(retry)+unit+" and try again.", nil)
}
func (a *application) guarded(scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.sameOrigin(w, r) {
			return
		}
		if !a.security.RateDisabled {
			ip, malformed := abuse.ClientIP(r, a.security.TrustedProxies)
			if malformed {
				a.logger.Info("trusted proxy parse failure")
			}
			key, rate, global, globalRate := scope+":"+r.PathValue("public_id")+":"+r.PathValue("session_id"), 120, "control", 6000
			switch scope {
			case "login":
				key = "login:" + ip.String()
				rate = 10
				global = "login"
				globalRate = 60
			case "session":
				key = "session:" + r.PathValue("public_id") + ":" + ip.String()
				rate = 120
				global = "session"
				globalRate = 600
			case "complete":
				rate = 300
			}
			for _, limit := range []struct {
				key  string
				rate int
			}{{"global:" + global, globalRate}, {key, rate}} {
				if ok, retry := a.limiter.Allow(limit.key, limit.rate*a.security.RateMultiplier); !ok {
					a.rateFailure(w, scope, retry)
					return
				}
			}
		}
		next(w, r)
	}
}

type maintainedHandler struct {
	http.Handler
	imports interface{ Run(context.Context) }
	uploads *media.Service
	logger  *slog.Logger
}

func (h *maintainedHandler) maintain(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cycle, cancel := context.WithTimeout(ctx, 5*time.Second)
			// Cleanup logs individual safe failures and retains their recovery state.
			if err := h.uploads.Cleanup(cycle); err != nil && ctx.Err() == nil {
				h.logger.Warn("periodic media cleanup deferred")
			}
			cancel()
		}
	}
}
