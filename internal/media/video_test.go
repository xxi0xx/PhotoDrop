package media

import (
	"bytes"
	"errors"
	"net/url"
	"testing"

	"photodrop/internal/storage"
	"photodrop/internal/testutil"
)

func TestVideoRejectionAndSameAttemptRecovery(t *testing.T) {
	for kind, data := range testutil.Videos() {
		t.Run(kind, func(t *testing.T) {
			s, _, e, session, fake, _ := directFixture(t)
			p := prepare(t, s, e, session, "application/octet-stream", data)
			for _, bad := range [][]byte{data[:20], bytes.Repeat([]byte("x"), len(data))} {
				put(t, p, bad)
				if _, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); !errors.Is(err, ErrType) && !errors.Is(err, ErrSize) {
					t.Fatalf("bad video finalized: %v", err)
				}
				u, _ := url.Parse(p.Upload.URL)
				if _, exists := fake.Objects()[u.Path]; exists {
					t.Fatal("invalid bytes retained")
				}
				refreshed, err := s.Authorize(t.Context(), e.PublicID, session.ID, p.Asset.ID)
				if err != nil || refreshed.Asset.ID != p.Asset.ID {
					t.Fatal("retry changed identity", err)
				}
				p = refreshed
			}
			put(t, p, data)
			a := finish(t, s, e, session, p)
			if a.MIMEType != kind || finish(t, s, e, session, p) != a {
				t.Fatal("finalize-first recovery changed media")
			}
			if err := s.DeleteEvent(t.Context(), e.ID); err != nil {
				t.Fatal(err)
			}
			if len(fake.Objects()) != 0 {
				t.Fatal("video delete left objects")
			}
		})
	}
}

func TestLocalVideoLimitsSignatureAndMixedQuota(t *testing.T) {
	s, _, e, session := fixture(t)
	data := testutil.Videos()["video/mp4"]
	size := int64(len(data))
	for _, tc := range []struct {
		name        string
		data        []byte
		size, limit int64
		want        error
	}{
		{"size", data, size + 1, size + 1, ErrSize},
		{"limit", data, size, size - 1, storage.ErrTooLarge},
		{"renamed", bytes.Repeat([]byte("x"), len(data)), size, size, ErrType},
		{"truncated", data[:20], 20, size, ErrType},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Upload(t.Context(), e.PublicID, session.ID, "clip.mp4", "video/mp4", bytes.NewReader(tc.data), tc.size, tc.limit)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if countAssets(t, s, "pending") != 0 || countAssets(t, s, "ready") != 0 {
				t.Fatal("invalid local upload retained")
			}
		})
	}
	if _, err := s.db.Exec("UPDATE events SET max_assets=2 WHERE id=?", e.ID); err != nil {
		t.Fatal(err)
	}
	upload(t, s, e, session, "photo.png")
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "clip.mp4", "application/octet-stream", bytes.NewReader(data), size, size); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "another.mp4", "video/mp4", bytes.NewReader(data), size, size); !errors.Is(err, ErrEventQuota) {
		t.Fatal("mixed quota not enforced", err)
	}
	stats, err := s.Stats(t.Context())
	if err != nil || stats[e.ID].PhotoCount != 2 || stats[e.ID].PendingCount != 0 || stats[e.ID].StorageBytes != size+int64(len(testutil.Images()["image/png"])) {
		t.Fatal("mixed totals", stats, err)
	}
	if _, err := s.db.Exec("UPDATE events SET max_assets=NULL,max_bytes=? WHERE id=?", stats[e.ID].StorageBytes, e.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "another.mp4", "video/mp4", bytes.NewReader(data), size, size); !errors.Is(err, ErrEventQuota) {
		t.Fatal("mixed byte quota not enforced", err)
	}
}
