package command

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/examples/deploy-gates/cmd/demo-cli/internal/output"
)

// Execute runs cmd on args, using os.Args when args is nil, prints errors,
// and returns the process exit code. Success returns 0. An error status the
// service answered returns its [output.ResponseError.ExitCode], 2 for a
// policy refusal and 1 otherwise, without printing again, since the response
// is printed already. Any other error is printed with its advice and returns
// 1. SIGINT and SIGTERM cancel the command's context.
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
