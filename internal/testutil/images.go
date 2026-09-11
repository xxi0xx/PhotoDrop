// Package testutil provides tiny generated fixtures for upload tests.
package testutil

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
)

func Images() map[string][]byte {
	pixel := image.NewRGBA(image.Rect(0, 0, 2, 2))
	pixel.Set(0, 0, color.RGBA{R: 120, G: 180, B: 80, A: 255})
	result := map[string][]byte{}
	var output bytes.Buffer
	jpeg.Encode(&output, pixel, nil)
	result["image/jpeg"] = append([]byte(nil), output.Bytes()...)
	output.Reset()
	png.Encode(&output, pixel)
	result["image/png"] = append([]byte(nil), output.Bytes()...)
	output.Reset()
	gif.Encode(&output, pixel, nil)
	result["image/gif"] = append([]byte(nil), output.Bytes()...)
	// Header fixtures are deliberate: the implementation only sniffs content,
	// and never claims to verify full WebP/HEIF image integrity.
	result["image/webp"] = []byte("RIFF\x14\x00\x00\x00WEBPVP8 \x08\x00\x00\x00fixture!")
	result["image/heic"] = []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00mif1heic")
	result["image/heif"] = []byte("\x00\x00\x00\x14ftypmif1\x00\x00\x00\x00mif1")
	return result
}
