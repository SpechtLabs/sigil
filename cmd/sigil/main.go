package main

import (
	"context"
	"os"
	"syscall"

	"charm.land/fang/v2"

	"github.com/spechtlabs/sigil/cmd/sigil/command"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/pretty"
)

// version is set via ldflags -X at release time. Commit, commit time and dirty
// state come from the VCS info the Go toolchain embeds in every build.
var version string

func main() {
	cmd := command.NewCommand(
		command.WithVersion(version),
	)

	// fang styles help, usage and errors. `sigil version` is the single
	// source of version information, so fang's --version flag is off.
	err := fang.Execute(context.Background(), cmd,
		fang.WithColorSchemeFunc(pretty.ColorScheme),
		fang.WithErrorHandler(pretty.ErrorHandler),
		fang.WithoutVersion(),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
	if err != nil {
		os.Exit(1)
	}
}
