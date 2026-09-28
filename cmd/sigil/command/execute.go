package command

import (
	"context"
	"os"
	"syscall"

	"charm.land/fang/v2"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/pretty"
)

// Execute runs cmd with sigil's styling for help, usage and errors, and
// returns the process's exit status: 1 when the command failed, after
// printing why, and 0 otherwise.
func Execute(ctx context.Context, cmd *cobra.Command) int {
	// `sigil version` is the single source of version information, so
	// fang's --version flag is off.
	err := fang.Execute(ctx, cmd,
		fang.WithColorSchemeFunc(pretty.ColorScheme),
		fang.WithErrorHandler(pretty.ErrorHandler),
		fang.WithoutVersion(),
		fang.WithNotifySignal(os.Interrupt, syscall.SIGTERM),
	)
	if err != nil {
		return 1
	}
	return 0
}
