package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"photodrop/internal/testutil"
)

func ftyp(major string, compatible ...string) []byte {
	b := make([]byte, 16+4*len(compatible))
	binary.BigEndian.PutUint32(b, uint32(len(b)))
	copy(b[4:], "ftyp"+major)
	for i, brand := range compatible {
		copy(b[16+i*4:], brand)
	}
	return b
}

func TestMediaIdentification(t *testing.T) {
	for kind, data := range testutil.Images() {
		t.Run(kind, func(t *testing.T) {
			got, err := Sniff(data)
			if err != nil || got != kind {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
	for kind, data := range testutil.Videos() {
		t.Run(kind, func(t *testing.T) {
			got, err := Sniff(data)
			if err != nil || got != kind {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
	box := []byte("\x00\x00\x00\x0cmdatdata")
	for _, brand := range []string{"isom", "iso2", "iso6", "mp41", "mp42", "avc1", "M4V ", "MSNV", "qt  "} {
		t.Run(brand, func(t *testing.T) {
			want := "video/mp4"
			if brand == "qt  " {
				want = "video/quicktime"
			}
			got, err := Sniff(append(ftyp(brand, "isom"), box...))
			if err != nil || got != want {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		data []byte
		want string
	}{
		{"HEIC before MP4", ftyp("mp42", "heic", "isom"), "image/heic"},
		{"HEIF before MP4", ftyp("isom", "mif1"), "image/heif"},
		{"QuickTime compatible brand", append(ftyp("isom", "qt  "), box...), "video/quicktime"},
		{"AVIF before MP4", append(ftyp("mp42", "avif"), box...), ""},
		{"AVIF sequence", append(ftyp("avis", "isom"), box...), ""},
		{"AVIF before HEIF", ftyp("avif", "mif1"), ""},
		{"audio only", append(ftyp("M4A ", "isom"), box...), ""},
		{"3GP", append(ftyp("3gp4", "isom"), box...), ""},
		{"unknown major", append(ftyp("xxxx", "isom"), box...), ""},
		{"bogus mp4 bytes", []byte("arbitrary text renamed to movie.mp4"), ""},
		{"WebM", []byte{0x1a, 0x45, 0xdf, 0xa3, 0x80}, ""},
		{"truncated ftyp", ftyp("mp42", "isom")[:19], ""},
		{"header only", ftyp("mp42"), ""},
		{"missing minor version", []byte("\x00\x00\x00\x0cftypmp42"), ""},
		{"zero size", []byte("\x00\x00\x00\x00ftypmp42\x00\x00\x00\x00"), ""},
		{"misaligned ftyp", []byte("\x00\x00\x00\x11ftypmp42\x00\x00\x00\x00x"), ""},
		{"oversized ftyp", append([]byte("\x00\x00\x04\x00ftypmp42"), bytes.Repeat([]byte{0}, 1024)...), ""},
		{"truncated following box", append(ftyp("mp42"), []byte("\x00\x00\x10\x00mdatx")...), ""},
		{"truncated extended box", append(ftyp("mp42"), []byte("\x00\x00\x00\x01mdatx")...), ""},
		{"zero extended size", append(ftyp("mp42"), []byte("\x00\x00\x00\x01mdat\x00\x00\x00\x00\x00\x00\x00\x00")...), ""},
		{"valid extended box", append(ftyp("mp42"), []byte("\x00\x00\x00\x01mdat\x00\x00\x00\x00\x00\x00\x00\x14data")...), "video/mp4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Sniff(tc.data)
			if got != tc.want || (tc.want == "" && !errors.Is(err, ErrType)) || (tc.want != "" && err != nil) {
				t.Fatalf("%q %v", got, err)
			}
		})
	}
	if _, err := Sniff(nil); !errors.Is(err, ErrEmpty) {
		t.Fatal(err)
	}
	for _, kind := range []string{"video/mp4", "video/quicktime", "application/octet-stream", ""} {
		if err := ValidateDeclaredType(kind); err != nil {
			t.Fatal(err)
		}
	}
}
