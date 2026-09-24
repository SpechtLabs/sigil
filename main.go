package main

import (
	"github.com/spechtlabs/sigil/cmd"
)

var (
	version string
	commit  string
	date    string
	builtBy string
)

func main() {
	cmd.Execute(version, commit, date, builtBy)
}
