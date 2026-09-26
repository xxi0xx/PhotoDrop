package testutil

import _ "embed"

// Original 16x16 single-frame fixtures; see testdata/README.md. These are only
// imported by tests, never by the production executable.
//
//go:embed testdata/tiny.mp4
var mp4 []byte

//go:embed testdata/tiny.mov
var mov []byte

func Videos() map[string][]byte {
	return map[string][]byte{"video/mp4": append([]byte(nil), mp4...), "video/quicktime": append([]byte(nil), mov...)}
}

func Media() map[string][]byte {
	result := Images()
	for kind, data := range Videos() {
		result[kind] = data
	}
	return result
}
