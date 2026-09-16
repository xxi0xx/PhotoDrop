// Package immich owns the optional HTTP integration; it never writes to or
// deletes from Immich's filesystem and never deletes remote assets or albums.
package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"slices"
	"time"

	"photodrop/internal/config"
	portable "photodrop/internal/export"
)

var ErrUnavailable = errors.New("Immich is unavailable or returned an unsupported response; check the configured server and retry")
var ErrAuth = errors.New("Immich rejected the API key or its permissions")
var ErrMissing = errors.New("the mapped Immich album or asset is missing; restore it before retrying")
var ErrCredentials = errors.New("this Immich target has no runtime credentials; restore its deployment configuration")
var ErrAlbumUncertain = errors.New("album creation outcome is uncertain; inspect Immich and restore the PhotoDrop album marker before retrying")
var requiredPermissions = []string{"album.create", "album.read", "asset.upload", "albumAsset.create"}
var uuid = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-4[0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

type Version struct {
	Major int `json:"major"`
	Minor int `json:"minor"`
	Patch int `json:"patch"`
}
type Album struct {
	ID          string `json:"id"`
	Name        string `json:"albumName"`
	Description string `json:"description"`
}
type Upload struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type API interface {
	Validate(context.Context) (Version, error)
	CreateAlbum(context.Context, string, string) (Album, error)
	Album(context.Context, string) (Album, error)
	FindAlbum(context.Context, string) (Album, error)
	Upload(context.Context, string, string, int64, io.Reader) (Upload, error)
	AddToAlbum(context.Context, string, string) error
}

type Client struct {
	base, key string
	http      *http.Client
}

func NewClient(cfg config.Immich) (*Client, error) {
	base, err := config.NormalizeImmichURL(cfg.URL)
	if err != nil || cfg.APIKey == "" {
		return nil, ErrCredentials
	}
	return &Client{base: base + "/api", key: cfg.APIKey, http: &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Responses/errors are bounded and never propagated verbatim. Transport errors
// can embed URLs; remote JSON can echo headers, credentials, or arbitrary HTML.
func (c *Client) request(ctx context.Context, method, path, kind string, body io.Reader, out any, limit int64) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, body)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("x-api-key", c.key)
	if kind != "" {
		req.Header.Set("Content-Type", kind)
	}
	response, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode == 401 || response.StatusCode == 403 {
		return ErrAuth
	}
	if response.StatusCode == 404 {
		return ErrMissing
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return ErrUnavailable
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(data)) > limit || json.Unmarshal(data, out) != nil {
		return ErrUnavailable
	}
	return nil
}
func (c *Client) json(ctx context.Context, method, path string, input, out any, limit int64) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var body io.Reader
	if input != nil {
		b, err := json.Marshal(input)
		if err != nil {
			return ErrUnavailable
		}
		body = bytes.NewReader(b)
	}
	return c.request(ctx, method, path, "application/json", body, out, limit)
}

func (c *Client) Validate(ctx context.Context) (Version, error) {
	var key struct {
		Permissions []string `json:"permissions"`
	}
	if err := c.json(ctx, "GET", "/api-keys/me", nil, &key, 64<<10); err != nil {
		return Version{}, err
	}
	for _, p := range requiredPermissions {
		if !slices.Contains(key.Permissions, p) && !slices.Contains(key.Permissions, "all") {
			return Version{}, ErrAuth
		}
	}
	var version Version
	if err := c.json(ctx, "GET", "/server/version", nil, &version, 4096); err != nil {
		return Version{}, err
	}
	// v3 removed device upload fields used in v2. Only claim the contract tested
	// here; fail safely on future incompatible major versions.
	if version.Major != 3 || version.Minor < 0 || version.Patch < 0 {
		return Version{}, errors.New("Immich API compatibility requires version 3.x (validated with 3.2.1)")
	}
	return version, nil
}

func (c *Client) CreateAlbum(ctx context.Context, name, marker string) (Album, error) {
	var a Album
	err := c.json(ctx, "POST", "/albums", map[string]string{"albumName": name, "description": marker}, &a, 64<<10)
	if err == nil && (!uuid.MatchString(a.ID) || a.Description != marker) {
		err = ErrUnavailable
	}
	return a, err
}
func (c *Client) Album(ctx context.Context, id string) (Album, error) {
	if !uuid.MatchString(id) {
		return Album{}, ErrUnavailable
	}
	var a Album
	err := c.json(ctx, "GET", "/albums/"+id, nil, &a, 4<<20)
	if err == nil && a.ID != id {
		err = ErrUnavailable
	}
	return a, err
}
func (c *Client) FindAlbum(ctx context.Context, marker string) (Album, error) {
	var albums []Album
	if err := c.json(ctx, "GET", "/albums", nil, &albums, 4<<20); err != nil {
		return Album{}, err
	}
	var found Album
	for _, a := range albums {
		if a.Description == marker {
			if found.ID != "" || !uuid.MatchString(a.ID) {
				return Album{}, ErrAlbumUncertain
			}
			found = a
		}
	}
	if found.ID == "" {
		return Album{}, ErrAlbumUncertain
	}
	return found, nil
}
func (c *Client) AddToAlbum(ctx context.Context, albumID, assetID string) error {
	if !uuid.MatchString(albumID) || !uuid.MatchString(assetID) {
		return ErrUnavailable
	}
	var result []struct {
		ID      string `json:"id"`
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	err := c.json(ctx, "PUT", "/albums/"+albumID+"/assets", map[string]any{"ids": []string{assetID}}, &result, 64<<10)
	if err != nil {
		return err
	}
	if len(result) != 1 || result[0].ID != assetID || (!result[0].Success && result[0].Error != "duplicate") {
		return ErrUnavailable
	}
	return nil
}

// Upload streams multipart media directly from the provider with fixed buffers.
// No checksum prepass or local staging: Immich computes its own content hash.
func (c *Client) Upload(ctx context.Context, filename, uploadedAt string, size int64, source io.Reader) (Upload, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	reader, writer := io.Pipe()
	multi := multipart.NewWriter(writer)
	done := make(chan error, 1)
	go func() {
		err := func() error {
			for _, name := range []string{"fileCreatedAt", "fileModifiedAt"} {
				if err := multi.WriteField(name, uploadedAt); err != nil {
					return err
				}
			}
			part, err := multi.CreateFormFile("assetData", portable.Filename(filename))
			if err != nil {
				return err
			}
			n, err := io.CopyBuffer(part, io.LimitReader(source, size+1), make([]byte, 32*1024))
			if err != nil {
				return err
			}
			if n != size {
				return errors.New("source size changed")
			}
			return multi.Close()
		}()
		writer.CloseWithError(err)
		done <- err
	}()
	var result Upload
	err := c.request(ctx, "POST", "/assets", multi.FormDataContentType(), reader, &result, 64<<10)
	reader.CloseWithError(io.ErrClosedPipe)
	// Closing the provider stream unblocks a stalled source read if Immich
	// rejected early. The worker owns the stream and also closes it on exit.
	if closer, ok := source.(io.Closer); ok {
		closer.Close()
	}
	copyErr := <-done
	if err != nil {
		return Upload{}, err
	}
	if copyErr != nil {
		return Upload{}, ErrUnavailable
	}
	if !uuid.MatchString(result.ID) || (result.Status != "created" && result.Status != "duplicate") {
		return Upload{}, ErrUnavailable
	}
	return result, nil
}

func safeError(err error) string {
	for _, known := range []error{ErrUnavailable, ErrAuth, ErrMissing, ErrCredentials, ErrAlbumUncertain} {
		if errors.Is(err, known) {
			return known.Error()
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "Import interrupted or timed out; unfinished work is retryable"
	}
	return "Import failed; check source storage and target availability, then retry"
}

// SafeError is suitable for an authenticated admin response, including when a
// provider or transport returned an arbitrary error containing secret material.
func SafeError(err error) string { return safeError(err) }
func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }
