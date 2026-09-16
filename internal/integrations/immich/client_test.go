package immich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"photodrop/internal/config"
)

const albumUUID = "11111111-1111-4111-8111-111111111111"
const assetUUID = "22222222-2222-4222-8222-222222222222"

func testClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	c, err := NewClient(config.Immich{URL: server.URL, APIKey: "private-test-api-key"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func jsonResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func TestAdapterContractAndStreaming(t *testing.T) {
	var duplicate atomic.Bool
	var uploaded atomic.Int64
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "private-test-api-key" {
			t.Error("missing authentication")
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/api-keys/me":
			jsonResponse(w, map[string]any{"permissions": requiredPermissions})
		case "GET /api/server/version":
			jsonResponse(w, Version{3, 2, 1})
		case "POST /api/albums":
			var input map[string]string
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input["albumName"] != "Wedding" || input["description"] != "marker" {
				t.Error("wrong album request")
			}
			jsonResponse(w, Album{albumUUID, "Wedding", "marker"})
		case "GET /api/albums/" + albumUUID:
			jsonResponse(w, Album{albumUUID, "Externally renamed", "marker"})
		case "GET /api/albums":
			jsonResponse(w, []Album{{assetUUID, "Wedding", "unrelated"}, {albumUUID, "Renamed", "marker"}})
		case "POST /api/assets":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			fields := map[string]string{}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Error(err)
					return
				}
				if part.FormName() == "assetData" {
					if part.FileName() != "image.png" {
						t.Error("unsafe filename", part.FileName())
					}
					n, err := io.Copy(io.Discard, part)
					if err != nil {
						t.Error(err)
					}
					uploaded.Add(n)
				} else {
					b, _ := io.ReadAll(io.LimitReader(part, 100))
					fields[part.FormName()] = string(b)
				}
			}
			if fields["fileCreatedAt"] != "2026-01-01T00:00:00Z" || fields["fileModifiedAt"] != fields["fileCreatedAt"] {
				t.Error("missing timestamps")
			}
			status := "created"
			if duplicate.Load() {
				status = "duplicate"
			}
			jsonResponse(w, Upload{assetUUID, status})
		case "PUT /api/albums/" + albumUUID + "/assets":
			jsonResponse(w, []map[string]any{{"id": assetUUID, "success": false, "error": "duplicate"}})
		default:
			t.Error("unexpected endpoint", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	if v, err := c.Validate(t.Context()); err != nil || v.String() != "3.2.1" {
		t.Fatal(v, err)
	}
	if _, err := c.CreateAlbum(t.Context(), "Wedding", "marker"); err != nil {
		t.Fatal(err)
	}
	if a, err := c.Album(t.Context(), albumUUID); err != nil || a.Name != "Externally renamed" {
		t.Fatal(a, err)
	}
	if a, err := c.FindAlbum(t.Context(), "marker"); err != nil || a.ID != albumUUID {
		t.Fatal(a, err)
	}
	for _, isDuplicate := range []bool{false, true} {
		duplicate.Store(isDuplicate)
		source := &boundedSource{remaining: 4 << 20}
		u, err := c.Upload(t.Context(), "../../image.png", "2026-01-01T00:00:00Z", 4<<20, source)
		if err != nil || u.ID != assetUUID || (u.Status == "duplicate") != isDuplicate || source.remaining != 0 || source.reads < 128 {
			t.Fatal("upload failed", u, err)
		}
		if err := c.AddToAlbum(t.Context(), albumUUID, u.ID); err != nil {
			t.Fatal(err)
		}
	}
	if uploaded.Load() != 8<<20 {
		t.Fatal("streamed bytes differ")
	}
}

type boundedSource struct{ remaining, reads int }

func (s *boundedSource) Read(p []byte) (int, error) {
	if len(p) > 32<<10 {
		return 0, errors.New("unbounded read")
	}
	if s.remaining == 0 {
		return 0, io.EOF
	}
	s.reads++
	n := min(s.remaining, len(p))
	clear(p[:n])
	s.remaining -= n
	return n, nil
}

func TestAdapterFailuresAreBoundedAndRedacted(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 429, 500, 503, 302} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Location", "https://never-forward-credentials.test")
				w.WriteHeader(status)
				w.Write([]byte("private-test-api-key arbitrary secret provider response"))
			})
			_, err := c.Validate(t.Context())
			if err == nil || strings.Contains(err.Error(), "private-test-api-key") {
				t.Fatal("unsafe failure", err)
			}
		})
	}
	for _, body := range []string{`{broken`, `{}`, `{"permissions":[]}`, strings.Repeat("x", (64<<10)+1)} {
		t.Run("malformed or missing permissions", func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) })
			if _, err := c.Validate(t.Context()); err == nil {
				t.Fatal("accepted invalid response")
			}
		})
	}
	t.Run("cancel", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
		defer cancel()
		if _, err := c.Validate(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	})
	t.Run("connection", func(t *testing.T) {
		server := httptest.NewServer(http.NotFoundHandler())
		server.Close()
		c, _ := NewClient(config.Immich{URL: server.URL, APIKey: "secret"})
		if _, err := c.Validate(t.Context()); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	})
	t.Run("invalid upload", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			io.WriteString(w, `{"id":"../../escape","status":"created"}`)
		})
		if _, err := c.Upload(t.Context(), "a.png", now(), 3, bytes.NewReader([]byte("abc"))); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	})
	t.Run("truncated source", func(t *testing.T) {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			jsonResponse(w, Upload{assetUUID, "created"})
		})
		if _, err := c.Upload(t.Context(), "a.png", now(), 4, bytes.NewReader([]byte("abc"))); err == nil {
			t.Fatal("accepted incomplete source")
		}
	})
}
