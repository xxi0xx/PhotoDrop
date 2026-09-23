package media

import (
	"errors"
	"photodrop/internal/testutil"
	"strings"
	"testing"
)

func TestContributorValidation(t *testing.T) {
	for _, name := range []string{"", "   ", "\t\n\u2003"} {
		value, err := NormalizeContributor(name)
		if err != nil || value != nil {
			t.Fatalf("whitespace: %v %v", value, err)
		}
	}
	for _, name := range []string{"Maria", "José 王小明", "<img src=x onerror=alert(1)>", strings.Repeat("界", 100)} {
		value, err := NormalizeContributor("  " + name + "  ")
		if err != nil || value == nil || *value != name {
			t.Fatal("valid name not preserved", err)
		}
	}
	for _, name := range []string{strings.Repeat("界", 101), "a\x00b", "a\nb", "a\x7fb", string([]byte{0xff})} {
		if _, err := NormalizeContributor(name); !errors.Is(err, ErrContributor) {
			t.Fatal("invalid name accepted")
		}
	}
}

func TestContributorInheritanceLocalAndDirect(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run(map[bool]string{false: "local", true: "direct"}[direct], func(t *testing.T) {
			s, _, e, anonymous, _, _ := directFixture(t)
			if !direct {
				configureDirect(t, s, nil, false)
			}
			first, err := s.CreateAttributedSession(t.Context(), e.PublicID, true, "  José 王小明  ")
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.CreateAttributedSession(t.Context(), e.PublicID, false, "Maria & David")
			if err != nil {
				t.Fatal(err)
			}
			var original string
			if err := s.db.QueryRow("SELECT contributor_name FROM upload_sessions WHERE id=?", first.ID).Scan(&original); err != nil || original != "José 王小明" {
				t.Fatal(original, err)
			}
			data := testutil.Images()["image/png"]
			for _, pair := range []struct {
				session Session
				name    *string
			}{{first, &original}, {second, func() *string { v := "Maria & David"; return &v }()}, {anonymous, nil}} {
				var asset Asset
				if direct {
					input := Preparation{Filename: "photo.png", ContentType: "image/png", Size: int64(len(data)), RequestID: randomID()}
					p, err := s.Prepare(t.Context(), e.PublicID, pair.session.ID, input, 1024)
					if err != nil {
						t.Fatal(err)
					}
					repeated, err := s.Prepare(t.Context(), e.PublicID, pair.session.ID, input, 1024)
					if err != nil || repeated.Asset.ID != p.Asset.ID {
						t.Fatal("retry identity changed", err)
					}
					if status, err := directPUT(p.Upload, data); err != nil || status != 200 {
						t.Fatal(status, err)
					}
					asset, err = s.Complete(t.Context(), e.PublicID, pair.session.ID, p.Asset.ID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = s.Complete(t.Context(), e.PublicID, pair.session.ID, p.Asset.ID); err != nil {
						t.Fatal(err)
					}
				} else {
					asset = upload(t, s, e, pair.session, "photo.png")
				}
				var saved *string
				if err := s.db.QueryRow("SELECT contributor_name FROM assets WHERE id=?", asset.ID).Scan(&saved); err != nil {
					t.Fatal(err)
				}
				if (saved == nil) != (pair.name == nil) || saved != nil && *saved != *pair.name {
					t.Fatal("asset attribution changed")
				}
			}
			if _, err := s.db.Exec("UPDATE upload_sessions SET expires_at='2000-01-01T00:00:00Z'"); err != nil {
				t.Fatal(err)
			}
			if err := s.Cleanup(t.Context()); err != nil {
				t.Fatal(err)
			}
			summary, err := s.Contributors(t.Context(), e.ID)
			if err != nil || len(summary) != 3 {
				t.Fatal(summary, err)
			}
			for _, c := range summary {
				if c.PhotoCount != 1 {
					t.Fatal("retry added another photo")
				}
			}
		})
	}
}
