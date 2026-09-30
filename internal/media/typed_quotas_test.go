package media

import (
	"bytes"
	"errors"
	"io"
	"photodrop/internal/database"
	"photodrop/internal/storage"
	"photodrop/internal/testutil"
	"sync"
	"sync/atomic"
	"testing"
)

func wantQuota(t *testing.T, err error, code string) {
	t.Helper()
	var q *QuotaError
	if !errors.As(err, &q) || q.Code != code {
		t.Fatalf("want %s, got %v", code, err)
	}
}

func TestDirectIndividualLimitsAndSharedMixedCeilings(t *testing.T) {
	for _, kind := range []string{"image/png", "video/mp4"} {
		t.Run(kind, func(t *testing.T) {
			s, _, e, session, _, _ := directFixture(t)
			data := testutil.Media()[kind]
			class := Class(kind)
			if _, err := s.db.Exec("UPDATE events SET max_"+class+"_file_bytes=? WHERE id=?", len(data)-1, e.ID); err != nil {
				t.Fatal(err)
			}
			p := Preparation{"a.bin", int64(len(data)), kind, randomID(), class}
			_, err := s.Prepare(t.Context(), e.PublicID, session.ID, p, 1<<20)
			wantQuota(t, err, class+"_file_too_large")
			if countAssets(t, s, "pending") != 0 {
				t.Fatal("oversize reserved")
			}
			if _, err := s.db.Exec("UPDATE events SET max_"+class+"_file_bytes=? WHERE id=?", len(data)+1, e.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Prepare(t.Context(), e.PublicID, session.ID, p, int64(len(data)-1)); !errors.Is(err, storage.ErrTooLarge) {
				t.Fatal("bypassed server ceiling", err)
			}
			if _, err = s.Prepare(t.Context(), e.PublicID, session.ID, p, int64(len(data))); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, scope := range []string{"count", "storage"} {
		t.Run("overall "+scope, func(t *testing.T) {
			s, _, e, session, _, _ := directFixture(t)
			photo := testutil.Media()["image/png"]
			video := testutil.Media()["video/mp4"]
			if scope == "count" {
				_, _ = s.db.Exec("UPDATE events SET max_assets=2,max_photos=10,max_videos=10 WHERE id=?", e.ID)
			} else {
				_, _ = s.db.Exec("UPDATE events SET max_bytes=? WHERE id=?", len(photo)+len(video), e.ID)
			}
			prepare(t, s, e, session, "image/png", photo)
			prepare(t, s, e, session, "video/mp4", video)
			_, err := s.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"a.png", int64(len(photo)), "image/png", randomID(), "photo"}, 1<<20)
			code := "event_storage"
			if scope == "count" {
				code = "event_file_count"
			}
			wantQuota(t, err, code)
		})
	}
}

func TestTypedBucketsAndStatsAcrossProviders(t *testing.T) {
	for _, scope := range []string{"count", "storage"} {
		t.Run(scope, func(t *testing.T) {
			s, _, e, session, _, remote := directFixture(t)
			photo, video := testutil.Media()["image/png"], testutil.Media()["video/mp4"]
			if scope == "count" {
				_, _ = s.db.Exec("UPDATE events SET max_photos=1,max_videos=1 WHERE id=?", e.ID)
			} else {
				_, _ = s.db.Exec("UPDATE events SET max_photo_storage_bytes=?,max_video_storage_bytes=? WHERE id=?", len(photo), len(video), e.ID)
			}
			configureDirect(t, s, remote, false)
			upload(t, s, e, session, "local.png")
			configureDirect(t, s, remote, true)
			p := prepare(t, s, e, session, "video/mp4", video)
			for kind, data := range map[string][]byte{"image/png": photo, "video/mp4": video} {
				_, err := s.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"anything.bin", int64(len(data)), kind, randomID(), ""}, 1024*1024)
				wantQuota(t, err, Class(kind)+"_"+scope)
			}
			stats, err := s.Stats(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got := stats[e.ID]
			if got.PhotoCount != 1 || got.ReadyCount != 1 || got.Photos.ReadyCount != 1 || got.Photos.Bytes != int64(len(photo)) || got.Videos.PendingCount != 1 || got.Videos.ReservedBytes != int64(len(video)) || got.ReservedBytes != int64(len(video)) {
				t.Fatal(got)
			}
			put(t, p, video)
			finish(t, s, e, session, p)
			stats, err = s.Stats(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			got = stats[e.ID]
			if got.PhotoCount != 2 || got.ReadyCount != 2 || got.Videos.ReadyCount != 1 || got.Videos.Bytes != int64(len(video)) || got.PendingCount != 0 || got.ReservedBytes != 0 {
				t.Fatal(got)
			}
		})
	}
}

func TestLocalSniffedClassAndLimitsBeforeStreaming(t *testing.T) {
	for _, kind := range []string{"image/png", "video/mp4"} {
		t.Run(kind, func(t *testing.T) {
			s, _, e, session := fixture(t)
			class := Class(kind)
			data := append(testutil.Media()[kind], make([]byte, 2048)...)
			_, err := s.db.Exec("UPDATE events SET max_"+class+"_file_bytes=1024 WHERE id=?", e.ID)
			if err != nil {
				t.Fatal(err)
			}
			consumed := 0
			source := &callbackReader{reader: bytes.NewReader(data[512:]), callback: func() { consumed++ }}
			_, err = s.Upload(t.Context(), e.PublicID, session.ID, "deceptive.jpg", "application/octet-stream", io.MultiReader(bytes.NewReader(data[:512]), source), int64(len(data)), 4096)
			wantQuota(t, err, class+"_file_too_large")
			if consumed != 0 || countAssets(t, s, "pending") != 0 {
				t.Fatal("read body/reserved oversized file")
			}
			_, _ = s.db.Exec("UPDATE events SET max_"+class+"_file_bytes=4096 WHERE id=?", e.ID)
			source = &callbackReader{reader: bytes.NewReader(data[512:]), callback: func() {
				var actual string
				var reserved int64
				if err := s.db.QueryRow("SELECT media_class,expected_size_bytes FROM assets WHERE status='pending'").Scan(&actual, &reserved); err != nil || actual != class || reserved != 4096 {
					t.Fatal(actual, reserved, err)
				}
				// Lowering after reservation must not cancel this stream.
				if _, err := s.db.Exec("UPDATE events SET max_"+class+"_file_bytes=1,max_"+class+"_storage_bytes=1 WHERE id=?", e.ID); err != nil {
					t.Fatal(err)
				}
			}}
			declared := "image/png"
			if class == "photo" {
				declared = "video/mp4"
			}
			a, err := s.Upload(t.Context(), e.PublicID, session.ID, "wrong.extension", declared, io.MultiReader(bytes.NewReader(data[:512]), source), -1, 4096)
			if err != nil || a.MIMEType != kind {
				t.Fatal(a, err)
			}
			stats, _ := s.Stats(t.Context())
			got := stats[e.ID]
			if got.StorageBytes != int64(len(data)) || got.ReservedBytes != 0 {
				t.Fatal(got)
			}
			_, err = s.Upload(t.Context(), e.PublicID, session.ID, "new.bin", "", bytes.NewReader(data), int64(len(data)), 4096)
			wantQuota(t, err, class+"_file_too_large")
		})
	}
}

func TestDirectClassCannotTransferQuotaAndUnknownMetadataRejected(t *testing.T) {
	s, _, e, session, _, remote := directFixture(t)
	for _, p := range []Preparation{
		{"movie.mp4", 100, "application/octet-stream", randomID(), ""},
		{"movie.mp4", 100, "video/mp4", randomID(), "photo"},
		{"photo.png", 100, "image/png", randomID(), "video"},
		{"photo.png", 100, "image/png", randomID(), "other"},
	} {
		if _, err := s.Prepare(t.Context(), e.PublicID, session.ID, p, 1024); !errors.Is(err, ErrType) {
			t.Fatal(err)
		}
	}
	for _, actual := range []string{"image/png", "video/mp4"} {
		data := testutil.Media()[actual]
		reserved := "photo"
		if Class(actual) == reserved {
			reserved = "video"
		}
		input := Preparation{"anything.bin", int64(len(data)), "application/octet-stream", randomID(), reserved}
		p, err := s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024*1024)
		if err != nil {
			t.Fatal(err)
		}
		put(t, p, data)
		if _, err = s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); !errors.Is(err, ErrType) {
			t.Fatal(err)
		}
		var key, class string
		if err = s.db.QueryRow("SELECT storage_key,media_class FROM assets WHERE id=?", p.Asset.ID).Scan(&key, &class); err != nil || class != reserved {
			t.Fatal(class, err)
		}
		if _, err = remote.Head(t.Context(), key); !errors.Is(err, storage.ErrMissing) {
			t.Fatal("invalid remote bytes retained", err)
		}
		again, err := s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024*1024)
		if err != nil || again.Asset.ID != p.Asset.ID {
			t.Fatal("lost immutable retry", err)
		}
		input.MediaClass = Class(actual)
		if _, err = s.Prepare(t.Context(), e.PublicID, session.ID, input, 1024*1024); !errors.Is(err, ErrRequest) {
			t.Fatal("transferred bucket", err)
		}
	}
}

func TestLegacyCompletionQuotaIsAtomicAcrossConnections(t *testing.T) {
	for _, scope := range []string{"count", "storage"} {
		t.Run(scope, func(t *testing.T) {
			s, dir, e, session, _, _ := directFixture(t)
			data := testutil.Media()["video/mp4"]
			var pending []Prepared
			for range 12 {
				p := prepare(t, s, e, session, "video/mp4", data)
				put(t, p, data)
				pending = append(pending, p)
			}
			if _, err := s.db.Exec("UPDATE assets SET media_class=NULL"); err != nil {
				t.Fatal(err)
			}
			if scope == "count" {
				_, _ = s.db.Exec("UPDATE events SET max_videos=1,max_assets=1 WHERE id=?", e.ID)
			} else {
				_, _ = s.db.Exec("UPDATE events SET max_video_storage_bytes=?,max_bytes=1 WHERE id=?", len(data), e.ID)
			}
			db, err := database.Open(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			other := New(db, s.storage, s.logger)
			other.ConfigureBackends(s.backends)
			var passed, rejected atomic.Int64
			var wg sync.WaitGroup
			start := make(chan struct{})
			for i, p := range pending {
				svc := s
				if i%2 == 0 {
					svc = other
				}
				wg.Go(func() {
					<-start
					_, err := svc.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID)
					if err == nil {
						passed.Add(1)
					} else if errors.Is(err, ErrEventQuota) {
						rejected.Add(1)
					} else {
						t.Error(err)
					}
				})
			}
			close(start)
			wg.Wait()
			if passed.Load() != 1 || rejected.Load() != 11 {
				t.Fatal(passed.Load(), rejected.Load())
			}
			var ready, unknown int
			if err := s.db.QueryRow("SELECT sum(status='ready' AND media_class='video'),sum(media_class IS NULL) FROM assets").Scan(&ready, &unknown); err != nil || ready != 1 || unknown != 11 {
				t.Fatal(ready, unknown, err)
			}
		})
	}
}

func TestDirectLoweredCapsHonorTypedReservationButNotLegacy(t *testing.T) {
	s, _, e, session, _, _ := directFixture(t)
	data := testutil.Media()["image/png"]
	p := prepare(t, s, e, session, "image/png", data)
	legacy := prepare(t, s, e, session, "image/png", data)
	_, _ = s.db.Exec("UPDATE assets SET media_class=NULL WHERE id=?", legacy.Asset.ID)
	_, _ = s.db.Exec("UPDATE events SET max_photos=1,max_photo_storage_bytes=1,max_photo_file_bytes=1 WHERE id=?", e.ID)
	put(t, p, data)
	finish(t, s, e, session, p)
	finish(t, s, e, session, p)
	put(t, legacy, data)
	_, err := s.Complete(t.Context(), e.PublicID, session.ID, legacy.Asset.ID)
	wantQuota(t, err, "photo_file_too_large")
	if countAssets(t, s, "pending") != 1 || countAssets(t, s, "ready") != 1 {
		t.Fatal("lowering removed assets")
	}
	_, err = s.Prepare(t.Context(), e.PublicID, session.ID, Preparation{"a.png", int64(len(data)), "image/png", randomID(), "photo"}, 1024*1024)
	wantQuota(t, err, "photo_file_too_large")
	_, _ = s.db.Exec("UPDATE events SET max_photos=NULL,max_photo_storage_bytes=NULL,max_photo_file_bytes=NULL WHERE id=?", e.ID)
	finish(t, s, e, session, legacy)
}
