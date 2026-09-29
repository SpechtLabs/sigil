// Package breaking implements the `sigil breaking` command, which will
// compare two versions of a kind file and flag the changes that break
// existing policies. It isn't implemented yet. [NewCommand] registers the
// command with its help and argument checks, and running it reports a
// not-implemented error, as text or as a JSON or YAML error record.
package breaking

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the breaking command.
func NewCommand(opts ...Option) *cobra.Command {
	text := output.Text
	o := &options{output: &text}
	for _, opt := range opts {
		opt(o)
	}

	return &cobra.Command{
		Use:        "breaking OLD_KIND_FILE NEW_KIND_FILE",
		SuggestFor: []string{"compat", "compatible", "diff"},
		Short:      "Detect kind changes that break existing policies (planned)",
		Long: `Planned: this command is not implemented yet, and exits with an error.

Compares two versions of a kind file and flags changes that would break
existing policies, modeled on buf breaking.

Run it in CI on every change to a kind, comparing against the version on the
main branch, so incompatible changes are caught before any policy fails.`,
		Example: `# Compare the kind on main with the working copy
git show main:policies/deploy_approval.sigil > /tmp/deploy_approval.main.sigil
sigil breaking /tmp/deploy_approval.main.sigil policies/deploy_approval.sigil`,
		Args:              usage.Exactly("OLD_KIND_FILE", "NEW_KIND_FILE"),
		ValidArgsFunction: complete.SigilFilesUpTo(2),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd, *o.output)
		},
	}
}
