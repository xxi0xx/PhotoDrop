// Package export writes a portable event directory independently of integrations.
package export

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"photodrop/internal/media"
)

type Event struct {
	ID        int64   `json:"id"`
	PublicID  string  `json:"publicId"`
	Name      string  `json:"name"`
	EventDate *string `json:"eventDate"`
}
type Asset struct {
	ID               string `json:"photoDropAssetId"`
	OriginalFilename string `json:"originalFilename"`
	ExportFilename   string `json:"exportFilename"`
	MIMEType         string `json:"mimeType"`
	Size             int64  `json:"sizeBytes"`
	UploadedAt       string `json:"uploadedAt"`
}
type Manifest struct {
	FormatVersion int     `json:"formatVersion"`
	Event         Event   `json:"event"`
	ExportedAt    string  `json:"exportedAt"`
	Assets        []Asset `json:"assets"`
}

// Filename is also suitable for a multipart filename. Treat both platforms'
// separators as untrusted, avoid Windows devices, and reserve suffix room.
func Filename(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = name[strings.LastIndex(name, "/")+1:]
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	if name == "" {
		name = "photo"
	}
	base := strings.ToUpper(strings.SplitN(name, ".", 2)[0])
	device := false
	if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
		suffix := strings.TrimPrefix(strings.TrimPrefix(base, "COM"), "LPT")
		device = utf8.RuneCountInString(suffix) == 1 && strings.Contains("123456789¹²³", suffix)
	}
	if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || device {
		name = "_" + name
	}
	for len(name) > 180 {
		_, n := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-n]
	}
	return strings.TrimRight(name, " .")
}

func filenames(assets []media.ReadyAsset) []string {
	used := map[string]bool{}
	next := map[string]int{}
	names := make([]string, len(assets))
	for i, a := range assets {
		name := Filename(a.Filename)
		ext := filepath.Ext(name)
		stem := strings.TrimSuffix(name, ext)
		candidate := name
		key := strings.ToLower(name)
		for suffix := max(2, next[key]); used[strings.ToLower(candidate)]; suffix++ {
			candidate = fmt.Sprintf("%s_%d%s", stem, suffix, ext)
			next[key] = suffix + 1
		}
		used[strings.ToLower(candidate)] = true
		names[i] = candidate
	}
	return names
}

// Run requires a new output directory. Refusing existing output makes reruns
// explicit and avoids confusing old files/manifests with this export snapshot.
func Run(ctx context.Context, source *media.Service, eventID int64, output string, logger *slog.Logger) error {
	snapshot, err := source.Snapshot(ctx, eventID)
	if err != nil {
		return err
	}
	defer snapshot.Close()
	if err := os.Mkdir(output, 0700); err != nil {
		return errors.New("export requires a new, writable output directory with an existing parent")
	}
	root, err := os.OpenRoot(output)
	if err != nil {
		return errors.New("open export directory failed")
	}
	defer root.Close()
	if err := root.Mkdir("photos", 0700); err != nil {
		return errors.New("create photos directory failed")
	}
	manifest := Manifest{1, Event{snapshot.Event.ID, snapshot.Event.PublicID, snapshot.Event.Name, snapshot.Event.EventDate}, time.Now().UTC().Format(time.RFC3339Nano), []Asset{}}
	logger.Info("export started", "event_id", eventID, "asset_count", len(snapshot.Assets))
	names := filenames(snapshot.Assets)
	for i, a := range snapshot.Assets {
		err := func() error {
			stream, _, err := snapshot.Open(ctx, a)
			if err != nil {
				return err
			}
			defer stream.Close()
			return publish(root, filepath.Join("photos", names[i]), func(f io.Writer) error {
				n, err := io.CopyBuffer(f, io.LimitReader(stream, a.Size+1), make([]byte, 32*1024))
				if err != nil {
					return errors.New("source read or destination write failed")
				}
				if n != a.Size {
					return errors.New("source size changed")
				}
				return ctx.Err()
			})
		}()
		if err != nil {
			logger.Warn("export failed", "event_id", eventID, "asset_id", a.ID)
			return fmt.Errorf("export asset %s failed: %w (completed files remain; no manifest published)", a.ID, err)
		}
		manifest.Assets = append(manifest.Assets, Asset{a.ID, a.Filename, names[i], a.MIMEType, a.Size, a.UploadedAt})
		logger.Info("export asset completed", "event_id", eventID, "asset_id", a.ID)
	}
	if err := publish(root, "photodrop-manifest.json", func(f io.Writer) error {
		encoder := json.NewEncoder(f)
		encoder.SetIndent("", "  ")
		return encoder.Encode(manifest)
	}); err != nil {
		return errors.New("export manifest publication failed")
	}
	logger.Info("export completed", "event_id", eventID, "asset_count", len(manifest.Assets))
	return nil
}

func publish(root *os.Root, name string, write func(io.Writer) error) error {
	temp := name + ".part"
	f, err := root.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return errors.New("create export file failed")
	}
	defer root.Remove(temp)
	defer f.Close()
	if err := write(struct{ io.Writer }{f}); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return errors.New("sync export file failed")
	}
	if err := f.Close(); err != nil {
		return errors.New("close export file failed")
	}
	// Link publishes the completed inode atomically with no-replace semantics
	// on both Linux and Windows. Rename alone can overwrite on Unix.
	if err := root.Link(temp, name); err != nil {
		return errors.New("publish export file failed (destination exists or filesystem does not support hard links)")
	}
	return nil
}
