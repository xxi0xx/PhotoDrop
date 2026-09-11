package server

import (
	"errors"
	"mime"
	"net/http"
	"time"

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
	var input struct{}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	session, err := a.media.CreateSession(r.Context(), r.PathValue("public_id"))
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"upload_session": session})
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
