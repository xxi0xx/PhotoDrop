package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"net/url"
	"testing"
)

func boundaryMovie(brand string, extended bool) []byte {
	data := make([]byte, 1024)
	copy(data, ftyp(brand))
	binary.BigEndian.PutUint32(data[16:20], 1008)
	copy(data[20:], "mdat")
	if extended {
		binary.BigEndian.PutUint32(data[16:20], 1)
		binary.BigEndian.PutUint64(data[24:32], 1008)
	}
	return data
}

func TestExact512EOF(t *testing.T) {
	for _, brand := range []string{"mp42", "qt  "} {
		for _, extended := range []bool{false, true} {
			data := boundaryMovie(brand, extended)
			kind := "video/mp4"
			if brand == "qt  " {
				kind = "video/quicktime"
			}
			if _, err := Sniff(data[:512], 512); !errors.Is(err, ErrType) {
				t.Fatal("exact EOF accepted", err)
			}
			if got, err := Sniff(data[:512], 1024); err != nil || got != kind {
				t.Fatal("identical prefix of larger valid object rejected", got, err)
			}
			for _, claimed := range []int64{-1, 512} {
				s, _, e, session := fixture(t)
				if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "short.mp4", kind, bytes.NewReader(data[:512]), claimed, 2048); !errors.Is(err, ErrType) {
					t.Fatal("local exact EOF accepted", err)
				}
				if countAssets(t, s, "ready") != 0 || countAssets(t, s, "pending") != 0 {
					t.Fatal("rejected local object retained")
				}
				if _, err := s.Upload(t.Context(), e.PublicID, session.ID, "complete.mp4", kind, bytes.NewReader(data), int64(len(data)), 2048); err != nil {
					t.Fatal(err)
				}
			}
			s, _, e, session, fake, _ := directFixture(t)
			p := prepare(t, s, e, session, kind, data[:512])
			put(t, p, data[:512])
			if _, err := s.Complete(t.Context(), e.PublicID, session.ID, p.Asset.ID); !errors.Is(err, ErrType) {
				t.Fatal("S3 exact EOF accepted", err)
			}
			address, _ := url.Parse(p.Upload.URL)
			if _, exists := fake.Objects()[address.Path]; exists {
				t.Fatal("invalid S3 object retained")
			}
			p = prepare(t, s, e, session, kind, data)
			put(t, p, data)
			if a := finish(t, s, e, session, p); a.MIMEType != kind {
				t.Fatal(a)
			}
		}
	}
}
