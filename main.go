package main

import (
	"github.com/cedi/go-application-template/cmd"
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
