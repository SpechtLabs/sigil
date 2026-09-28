package command

import (
	"context"
	"io"
	"os"
	"syscall"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/usage"
)

// Execute runs cmd on args, os.Args without the program name when nil,
// with sigil's styling for help, usage and errors, and returns the
// process's exit status: 1 when the command failed, after printing why,
// and 0 otherwise.
func Execute(ctx context.Context, cmd *cobra.Command, args []string) int {
	if args == nil {
		args = os.Args[1:]
	}
	cmd.SetArgs(args)
	// --color must take effect before anything is written, including the
	// help that cobra prints while it's still parsing flags, so it's read
	// ahead of the real parse.
	colorMode(args).Apply()

	// `sigil version` is the single source of version information, so
	// fang's --version flag is off.
	err := fang.Execute(ctx, cmd,
		fang.WithColorSchemeFunc(pretty.ColorScheme),
		fang.WithErrorHandler(func(w io.Writer, styles fang.Styles, err error) {
			pretty.ErrorHandler(w, styles, usage.Humanize(cmd, args, err))
		}),
		fang.WithoutVersion(),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
	if err != nil {
		return 1
	}
	return 0
}

// colorMode reads --color from args, ignoring everything else.
func colorMode(args []string) output.Color {
	mode := output.ColorAuto
	fs := pflag.NewFlagSet("color", pflag.ContinueOnError)
	fs.ParseErrorsAllowlist.UnknownFlags = true
	fs.SetOutput(io.Discard)
	fs.Var(&mode, "color", "")
	// A parse error means the real parse reports it; the mode stays auto.
	_ = fs.Parse(args)
	return mode
}
