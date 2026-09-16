package export

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"photodrop/internal/database"
	"photodrop/internal/events"
	"photodrop/internal/media"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
)

func TestExportManifestNamesBytesAndFailure(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "missing source"}[missing], func(t *testing.T) {
			dir := t.TempDir()
			db, err := database.Open(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			objects, err := storage.NewLocal(filepath.Join(dir, "uploads"))
			if err != nil {
				t.Fatal(err)
			}
			s := media.New(db, objects, logger)
			enabled := true
			e, err := events.New(db).Create(t.Context(), events.Input{Name: "Export event", Enabled: &enabled})
			if err != nil {
				t.Fatal(err)
			}
			session, err := s.CreateSession(t.Context(), e.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			data := testutil.Images()["image/png"]
			names := []string{"photo.png", "photo.png", "PHOTO.PNG", "../../outside.png", `C:\Windows\system.png`, "/absolute.png", "café-喜.png", "CON.txt", "...", "photo_2.png"}
			for i, name := range names {
				a, err := s.Upload(t.Context(), e.PublicID, session.ID, "safe.png", "image/png", bytes.NewReader(data), int64(len(data)), 1024)
				if err != nil {
					t.Fatal(err)
				}
				// Existing databases may contain filenames from older versions. Export
				// independently defends its destination even if ingress rules change.
				if _, err := db.Exec("UPDATE assets SET original_filename=?,created_at=? WHERE id=?", name, strings.Repeat("0", i+1), a.ID); err != nil {
					t.Fatal(err)
				}
				if missing && i == 0 {
					if err := objects.Delete(t.Context(), "e1_"+a.ID); err != nil {
						t.Fatal(err)
					}
				}
			}
			if _, err := db.Exec("INSERT INTO assets(id,event_id,upload_session_id,original_filename,storage_key,status,created_at,storage_backend_id) VALUES('pending',?,?, 'pending.png','pending','pending','old',1)", e.ID, session.ID); err != nil {
				t.Fatal(err)
			}
			output := filepath.Join(dir, "export")
			err = Run(t.Context(), s, e.ID, output, logger)
			if missing {
				if err == nil {
					t.Fatal("missing source succeeded")
				}
				if _, err := os.Stat(filepath.Join(output, "photodrop-manifest.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("failed export claimed completion")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(filepath.Join(output, "photodrop-manifest.json"))
			if err != nil {
				t.Fatal(err)
			}
			var manifest Manifest
			if err := json.Unmarshal(b, &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.FormatVersion != 1 || manifest.Event.ID != e.ID || manifest.Event.PublicID != e.PublicID || len(manifest.Assets) != len(names) {
				t.Fatal("manifest incomplete", string(b))
			}
			seen := map[string]bool{}
			for _, a := range manifest.Assets {
				key := strings.ToLower(a.ExportFilename)
				if seen[key] || a.ExportFilename != filepath.Base(a.ExportFilename) {
					t.Fatal("collision or escape", a.ExportFilename)
				}
				seen[key] = true
				got, err := os.ReadFile(filepath.Join(output, "photos", a.ExportFilename))
				if err != nil || !bytes.Equal(got, data) || a.Size != int64(len(data)) || a.UploadedAt == "" || a.MIMEType != "image/png" {
					t.Fatal("wrong output", err)
				}
			}
			for _, forbidden := range []string{"storage_key", "storage_backend", "endpoint", "secret", dir} {
				if strings.Contains(string(b), forbidden) {
					t.Fatal("manifest leaked internals")
				}
			}
			if err := Run(t.Context(), s, e.ID, output, logger); err == nil {
				t.Fatal("overwrote existing directory")
			}
			second := filepath.Join(dir, "second")
			if err := Run(t.Context(), s, e.ID, second, logger); err != nil {
				t.Fatal(err)
			}
			secondBytes, _ := os.ReadFile(filepath.Join(second, "photodrop-manifest.json"))
			var repeat Manifest
			json.Unmarshal(secondBytes, &repeat)
			for i, a := range manifest.Assets {
				if a.ExportFilename != repeat.Assets[i].ExportFilename {
					t.Fatal("nondeterministic names")
				}
			}
		})
	}
}

func TestPublishDoesNotOverwriteAndDiscardsPartial(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	os.WriteFile(filepath.Join(dir, "existing"), []byte("keep"), 0600)
	if err := publish(root, "existing", func(w io.Writer) error { _, err := io.WriteString(w, "replace"); return err }); err == nil {
		t.Fatal("overwrote destination")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "existing")); string(b) != "keep" {
		t.Fatal("destination changed")
	}
	if err := publish(root, "partial", func(w io.Writer) error { w.Write([]byte("partial")); return errors.New("failed midstream") }); err == nil {
		t.Fatal("accepted partial")
	}
	for _, name := range []string{"partial", "partial.part", "existing.part"} {
		if _, err := root.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("partial file remains", name)
		}
	}
}

func TestExportPublicationRejectsSymlinkEscape(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "photos")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := publish(root, filepath.Join("photos", "escape.png"), func(w io.Writer) error { _, err := io.WriteString(w, "unexpected"); return err }); err == nil {
		t.Fatal("escaped export root")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote outside root", err)
	}
}
