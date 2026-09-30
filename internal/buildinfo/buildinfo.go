// Package buildinfo reads what a sigil build says about itself: the
// release version, and the commit, commit time, dirty state, Go version
// and platform the Go toolchain embeds in every binary. `sigil version`
// prints it, and so does the WebAssembly module's version op, so the two
// report a build the same way.
package buildinfo

import "runtime/debug"

// Unknown is what a field reads as when the build doesn't carry it, such
// as the commit of a binary built with go run.
const Unknown = "unknown"

// Info is what a build reports about itself, as `sigil version -o json`
// prints it, in that order.
type Info struct { //nolint:govet // the field order is the JSON's
	Version    string `json:"version"`
	Commit     string `json:"commit"`
	CommitTime string `json:"commitTime"`
	Dirty      bool   `json:"dirty"`
	GoVersion  string `json:"goVersion"`
	Platform   string `json:"platform"`
}

// New combines the release version, which a release build sets with
// -ldflags, with the VCS and toolchain details of bi. An empty version
// falls back to the main module's version. `go run` and `go test` don't
// embed VCS details, so those fields read as [Unknown], and so does every
// field when bi is nil.
func New(version string, bi *debug.BuildInfo) Info {
	i := Info{
		Version:    version,
		Commit:     Unknown,
		CommitTime: Unknown,
		GoVersion:  Unknown,
		Platform:   Unknown,
	}

	if bi == nil {
		if i.Version == "" {
			i.Version = Unknown
		}
		return i
	}

	if i.Version == "" {
		i.Version = bi.Main.Version
	}
	if bi.GoVersion != "" {
		i.GoVersion = bi.GoVersion
	}

	var goos, goarch string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			i.Commit = s.Value
		case "vcs.time":
			i.CommitTime = s.Value
		case "vcs.modified":
			i.Dirty = s.Value == "true"
		case "GOOS":
			goos = s.Value
		case "GOARCH":
			goarch = s.Value
		}
	}
	if goos != "" && goarch != "" {
		i.Platform = goos + "/" + goarch
	}

	return i
}
