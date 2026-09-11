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
var ErrType = errors.New("this file does not appear to be a supported image")
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
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/heic", "image/heif":
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

// Sniff inspects at most 512 bytes, never decodes a full image. ISO BMFF HEIF
// files use an ftyp box. Generic video/AVIF brands alone do not qualify.
func Sniff(prefix []byte) (string, error) {
	if len(prefix) == 0 {
		return "", ErrEmpty
	}
	kind := http.DetectContentType(prefix)
	if SupportedMIME(kind) {
		return kind, nil
	}
	if len(prefix) >= 16 && string(prefix[4:8]) == "ftyp" {
		size := int(binary.BigEndian.Uint32(prefix[:4]))
		if size >= 16 && size <= len(prefix) && size <= 512 && size%4 == 0 {
			brands := []string{string(prefix[8:12])}
			for i := 16; i < size; i += 4 {
				brands = append(brands, string(prefix[i:i+4]))
			}
			for _, brand := range brands {
				switch brand {
				case "avif", "avis", "qt  ":
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
		}
	}
	return "", ErrType
}
