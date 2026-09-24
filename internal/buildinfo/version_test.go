package buildinfo

import "testing"

func TestVersion(t *testing.T) {
	oldVersion, oldCommit := Version, Commit
	defer func() { Version, Commit = oldVersion, oldCommit }()
	for _, tc := range []struct{ version, commit, want string }{
		{"dev", "", "PhotoDrop dev"},
		{"1.0.0", "", "PhotoDrop 1.0.0"},
		{"1.1.0-rc.1", "abcdef", "PhotoDrop 1.1.0-rc.1 (abcdef)"},
	} {
		Version, Commit = tc.version, tc.commit
		if got := String(); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}
