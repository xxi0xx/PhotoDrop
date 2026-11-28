package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"photodrop/internal/auth"
	"photodrop/internal/destinations"
	"photodrop/internal/integrations/immich"
	"photodrop/internal/media"
)

func (a *application) immichFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, media.ErrDeleting):
		a.fail(w, err)
	case errors.Is(err, immich.ErrInput):
		apiError(w, 422, "immich_input", immich.ErrInput.Error(), nil)
	case errors.Is(err, immich.ErrActive):
		apiError(w, 409, "immich_active", immich.ErrActive.Error(), nil)
	case errors.Is(err, destinations.ErrNoAccount):
		apiError(w, 422, "immich_no_account", destinations.ErrNoAccount.Error(), nil)
	default:
		apiError(w, 503, "immich_unavailable", immich.SafeError(err), nil)
	}
}
func (a *application) immichStatus(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	status, err := a.immich.Status(r.Context(), internalID(r), r.URL.Query().Get("target"))
	if err != nil {
		a.immichFailure(w, err)
		return
	}
	writeJSON(w, 200, status)
}

// immichTargets lists the caller's usable targets, for the new-event form.
func (a *application) immichTargets(w http.ResponseWriter, r *http.Request, s auth.Session) {
	catalog, err := a.immich.TargetCatalog(r.Context(), s.Host.ID)
	if err != nil {
		a.fail(w, err)
		return
	}
	writeJSON(w, 200, catalog)
}

func (a *application) immichAutoImport(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var input struct {
		Target     string `json:"target"`
		AutoImport *bool  `json:"auto_import"`
	}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	if input.AutoImport == nil {
		a.immichFailure(w, immich.ErrInput)
		return
	}
	if err := a.immich.SetAutoImport(r.Context(), internalID(r), input.Target, *input.AutoImport); err != nil {
		a.immichFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *application) immichProvision(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var input struct {
		Target    string `json:"target"`
		AlbumName string `json:"album_name"`
	}
	if !decodeJSON(w, r, &input, 4096) {
		return
	}
	id, err := a.immich.Provision(r.Context(), internalID(r), input.Target, input.AlbumName)
	if err != nil {
		a.immichFailure(w, err)
		return
	}
	writeJSON(w, 202, map[string]int64{"job_id": id})
}
func (a *application) immichTest(w http.ResponseWriter, r *http.Request, s auth.Session) {
	var input struct {
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	if _, err := a.events.Get(r.Context(), scope(s), internalID(r)); err != nil {
		a.fail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	v, err := a.immich.Test(ctx, internalID(r), input.Target)
	if err != nil {
		a.immichFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"version": v.String()})
}
func (a *application) immichStart(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var input struct {
		Target    string `json:"target"`
		AlbumName string `json:"album_name"`
		Mode      string `json:"mode"`
	}
	if !decodeJSON(w, r, &input, 4096) {
		return
	}
	id, err := a.immich.Enqueue(r.Context(), internalID(r), input.Target, input.AlbumName, input.Mode)
	if err != nil {
		a.immichFailure(w, err)
		return
	}
	writeJSON(w, 202, map[string]int64{"job_id": id})
}
func (a *application) immichCancel(w http.ResponseWriter, r *http.Request, _ auth.Session) {
	var input struct {
		Target string `json:"target"`
	}
	if !decodeJSON(w, r, &input, 1024) {
		return
	}
	if err := a.immich.Cancel(r.Context(), internalID(r), input.Target); err != nil {
		a.immichFailure(w, err)
		return
	}
	w.WriteHeader(204)
}
