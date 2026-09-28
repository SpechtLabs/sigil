package main

import "github.com/spechtlabs/sigil/pkg/cli"

// version is set via ldflags -X at release time. Commit, commit time and dirty
// state come from the VCS info the Go toolchain embeds in every build.
var version string

func main() {
	cli.Main(cli.WithVersion(version))
}
