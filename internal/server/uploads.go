package server

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"time"

	"photodrop/internal/abuse"
	"photodrop/internal/events"
	"photodrop/internal/media"
)

const uploadTimeout = 10 * time.Minute

func (a *application) writeAdminEvent(w http.ResponseWriter, r *http.Request, e events.Event) {
	stats, err := a.media.Stats(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	item := a.adminEvent(e)
	item.Media = stats[e.ID]
	writeJSON(w, 200, map[string]any{"event": item})
}

func (a *application) createUploadSession(w http.ResponseWriter, r *http.Request) {
	// Anonymous capabilities scoped to an event: never read an admin cookie or
	// require its CSRF token. Origin checks supplement the unguessable link.
	if !a.sameOrigin(w, r) {
		return
	}
	var input struct {
		Token string `json:"turnstile_token"`
	}
	if !decodeJSON(w, r, &input, 4096) {
		return
	}
	verified := false
	if a.security.TurnstileSiteKey != "" {
		if input.Token == "" || len(input.Token) > 2048 {
			a.fail(w, abuse.ErrChallenge)
			return
		}
		e, err := a.events.GetPublic(r.Context(), r.PathValue("public_id"))
		if err != nil {
			a.fail(w, err)
			return
		}
		if e.Status(time.Now()) != "open" {
			a.fail(w, media.ErrClosed)
			return
		}
		select {
		case a.challengeSlots <- struct{}{}:
		default:
			a.rateFailure(w, "verification", 1)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		result, err := a.verifier.Verify(ctx, input.Token)
		cancel()
		<-a.challengeSlots
		if err != nil {
			if !errors.Is(err, abuse.ErrChallenge) {
				err = abuse.ErrChallengeUnavailable
			}
			a.fail(w, err)
			return
		}
		expected, _ := url.Parse(a.baseURL)
		if err := abuse.Validate(result, expected.Hostname()); err != nil {
			a.fail(w, err)
			return
		}
		verified = true
	}
	session, err := a.media.CreateVerifiedSession(r.Context(), r.PathValue("public_id"), verified)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"upload_session": session, "upload_strategy": a.media.Strategy()})
}

func (a *application) prepareAsset(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	var input media.Preparation
	if !decodeJSON(w, r, &input, 4096) {
		return
	}
	prepared, err := a.media.Prepare(r.Context(), r.PathValue("public_id"), r.PathValue("session_id"), input, a.maxFileSize)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, prepared)
}
func (a *application) authorizeAsset(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	var input struct{}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	prepared, err := a.media.Authorize(r.Context(), r.PathValue("public_id"), r.PathValue("session_id"), r.PathValue("asset_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, prepared)
}
func (a *application) completeAsset(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	var input struct{}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	asset, err := a.media.Complete(ctx, r.PathValue("public_id"), r.PathValue("session_id"), r.PathValue("asset_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"asset": asset})
}

func (a *application) uploadAsset(w http.ResponseWriter, r *http.Request) {
	if !a.sameOrigin(w, r) {
		return
	}
	// Only file transfers extend Gate 1's short general-purpose deadlines.
	// Both a known Content-Length and chunked bodies are capped server-side.
	controller := http.NewResponseController(w)
	for _, set := range []func(time.Time) error{controller.SetReadDeadline, controller.SetWriteDeadline} {
		if err := set(time.Now().Add(uploadTimeout)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			a.fail(w, err)
			return
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, a.maxFileSize+1)
	disposition, params, err := mime.ParseMediaType(r.Header.Get("Content-Disposition"))
	if err != nil || disposition != "attachment" {
		a.fail(w, media.ErrFilename)
		return
	}
	asset, err := a.media.Upload(r.Context(), r.PathValue("public_id"), r.PathValue("session_id"), params["filename"], r.Header.Get("Content-Type"), r.Body, r.ContentLength, a.maxFileSize)
	if err != nil {
		a.logger.Info("asset upload rejected or failed", "error", err)
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"asset": asset})
}
