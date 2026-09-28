package command

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/cmd/demo-cli/internal/output"
)

// Execute runs cmd on args, using os.Args when args is nil, prints errors,
// and returns the process exit code. Policy refusals return 2.
func Execute(ctx context.Context, cmd *cobra.Command, args []string) int {
	if args == nil {
		args = os.Args[1:]
	}
	cmd.SetArgs(args)
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := cmd.ExecuteContext(ctx)
	if err == nil {
		return 0
	}
	if responseErr, ok := errors.AsType[*output.ResponseError](err); ok {
		// Its response and diagnostics have already been printed.
		return responseErr.ExitCode()
	}
	_ = humane.Fprint(cmd.ErrOrStderr(), err)
	return 1
}
