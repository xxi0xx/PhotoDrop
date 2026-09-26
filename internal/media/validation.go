// Package media owns guest upload authorization and asset metadata, independently
// of the filesystem mechanics in package storage.
package media

import (
	"encoding/binary"
	"errors"
	"mime"
	"net/http"
	"strings"
	"unicode/utf8"
)

var ErrClosed = errors.New("this event is no longer accepting uploads")
var ErrSession = errors.New("upload session not found for this event")
var ErrFilename = errors.New("provide a filename of 1 to 255 valid characters")
var ErrType = errors.New("this file does not appear to be supported media")
var ErrEmpty = errors.New("empty files cannot be uploaded")
var ErrBusy = errors.New("uploads are in progress for this event; try deleting it again after they finish")
var ErrDeleting = errors.New("event media cleanup is incomplete; the event is closed, retry deletion")
var ErrSize = errors.New("upload size did not match the request")

func ValidateFilename(name string) error {
	if !utf8.ValidString(name) || strings.TrimSpace(name) == "" || utf8.RuneCountInString(name) > 255 || strings.ContainsAny(name, "\x00\r\n") {
		return ErrFilename
	}
	return nil
}

func SupportedMIME(value string) bool {
	switch value {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/heic", "image/heif", "video/mp4", "video/quicktime":
		return true
	}
	return false
}

func ValidateDeclaredType(value string) error {
	if value == "" {
		return nil
	}
	kind, _, err := mime.ParseMediaType(value)
	if err != nil || (kind != "application/octet-stream" && !SupportedMIME(kind)) {
		return ErrType
	}
	return nil
}

// Sniff inspects at most 512 bytes, without decoding media or verifying codecs.
// Parse ISO BMFF before http.DetectContentType: its MP4 heuristic does not
// distinguish image brands or require the complete declared ftyp box.
func Sniff(prefix []byte) (string, error) {
	if len(prefix) == 0 {
		return "", ErrEmpty
	}
	if len(prefix) > 512 {
		prefix = prefix[:512]
	}
	if len(prefix) >= 8 && string(prefix[4:8]) == "ftyp" {
		size := int(binary.BigEndian.Uint32(prefix[:4]))
		if size >= 16 && size <= len(prefix) && size <= 512 && size%4 == 0 {
			brands := []string{string(prefix[8:12])}
			for i := 16; i < size; i += 4 {
				brands = append(brands, string(prefix[i:i+4]))
			}
			for _, brand := range brands {
				switch brand {
				case "avif", "avis":
					return "", ErrType
				}
			}
			for _, brand := range brands {
				switch brand {
				case "heic", "heix", "heim", "heis", "hevc", "hevx":
					return "image/heic", nil
				}
			}
			for _, brand := range brands {
				if brand == "mif1" || brand == "msf1" {
					return "image/heif", nil
				}
			}
			// A movie needs more than an isolated ftyp box. Reject missing or
			// malformed following box headers visible in the bounded prefix.
			if len(prefix)-size < 8 {
				return "", ErrType
			}
			next := uint64(binary.BigEndian.Uint32(prefix[size : size+4]))
			minimum := uint64(8)
			if next == 1 {
				if len(prefix)-size < 16 {
					return "", ErrType
				}
				next = binary.BigEndian.Uint64(prefix[size+8 : size+16])
				minimum = 16
				if next < minimum {
					return "", ErrType
				}
			}
			if next != 0 && (next < minimum || (len(prefix) < 512 && next > uint64(len(prefix)-size))) {
				return "", ErrType
			}
			for _, brand := range brands {
				if brand == "qt  " {
					return "video/quicktime", nil
				}
			}
			// Use a conservative major-brand allowlist. Unknown/image/audio/3GP
			// majors must not become video merely by listing isom compatibility.
			switch brands[0] {
			case "isom", "iso2", "iso3", "iso4", "iso5", "iso6", "iso7", "iso8", "iso9", "mp41", "mp42", "avc1", "M4V ", "MSNV":
				return "video/mp4", nil
			}
		}
		return "", ErrType
	}
	kind := http.DetectContentType(prefix)
	if strings.HasPrefix(kind, "image/") && SupportedMIME(kind) {
		return kind, nil
	}
	return "", ErrType
}
