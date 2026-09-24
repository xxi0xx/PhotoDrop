// Package buildinfo contains metadata injected by the release build, never runtime secrets.
package buildinfo

var Version = "dev"
var Commit = ""

func String() string {
	if Commit == "" {
		return "PhotoDrop " + Version
	}
	return "PhotoDrop " + Version + " (" + Commit + ")"
}
