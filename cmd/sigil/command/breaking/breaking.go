// Package breaking implements the `sigil breaking` command.
package breaking

import (
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/placeholder"
)

// NewCommand returns the breaking command.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	return &cobra.Command{
		Use:        "breaking OLD_KIND_FILE NEW_KIND_FILE",
		SuggestFor: []string{"compat", "compatible", "diff"},
		Short:      "Detect kind changes that break existing policies",
		Long: `Compares two versions of a kind file and flags changes that would break
existing policies, modeled on buf breaking.

Run it in CI on every change to a kind, comparing against the version on the
main branch, so incompatible changes are caught before any policy fails.`,
		Example: `# Compare the kind on main with the working copy
git show main:policies/deploy_approval.sigil > /tmp/deploy_approval.main.sigil
sigil breaking /tmp/deploy_approval.main.sigil policies/deploy_approval.sigil`,
		Args:              cobra.ExactArgs(2),
		ValidArgsFunction: complete.SigilFilesUpTo(2),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return placeholder.NotImplemented(cmd)
		},
	}
}
