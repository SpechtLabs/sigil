// Package command implements the sigil root command. Every subcommand lives in
// its own sub-package, exposes a NewCommand constructor, and receives each of
// its dependencies through a With* option declared in its options.go.
package command

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/command/breaking"
	"github.com/spechtlabs/sigil/cmd/sigil/command/check"
	"github.com/spechtlabs/sigil/cmd/sigil/command/eval"
	"github.com/spechtlabs/sigil/cmd/sigil/command/explain"
	"github.com/spechtlabs/sigil/cmd/sigil/command/format"
	"github.com/spechtlabs/sigil/cmd/sigil/command/gen"
	"github.com/spechtlabs/sigil/cmd/sigil/command/lsp"
	"github.com/spechtlabs/sigil/cmd/sigil/command/test"
	"github.com/spechtlabs/sigil/cmd/sigil/command/version"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
)

// Command groups, in the order help lists them. Every subcommand belongs to
// one; the root assigns them so subcommand packages stay unaware of the layout.
var (
	groupPolicy = &cobra.Group{ID: "policy", Title: "Policy commands"}
	groupKind   = &cobra.Group{ID: "kind", Title: "Kind commands"}
	groupEditor = &cobra.Group{ID: "editor", Title: "Editor integration"}
	groupOther  = &cobra.Group{ID: "other", Title: "Other commands"}
)

// NewCommand returns the sigil root command with every subcommand attached.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}

	// Subcommands receive a pointer so they read the value cobra parsed.
	outputFormat := output.Text

	cmd := &cobra.Command{
		Use:   "sigil",
		Short: "Write, check and evaluate Sigil policies",
		Long: `Sigil evaluates host-provided input to a typed decision such as approve, deny
or review. Every decision carries a reason and a payload, every policy is
type-checked against a kind the host defines in Go, and every evaluation is
guaranteed to halt.`,
		Example: `# Type-check a policy against its kind
sigil check --kind deploy_approval.sigil deploy/production.sigil

# Evaluate it against an input and see why it decided what it did
sigil eval --kind deploy_approval.sigil --input release.json deploy/production.sigil`,
		// No Args validator: cobra then reports an unknown subcommand and
		// suggests the closest one.
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	cmd.PersistentFlags().VarP(&outputFormat, "output", "o", "Output format: "+strings.Join(output.Formats, ", "))
	// Only fails if the flag is missing or already has a completion func,
	// both of which are programming errors caught by the tests.
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(output.Formats, cobra.ShellCompDirectiveNoFileComp))

	cmd.AddGroup(groupPolicy, groupKind, groupEditor, groupOther)
	cmd.SetHelpCommandGroupID(groupOther.ID)
	cmd.SetCompletionCommandGroupID(groupOther.ID)

	addToGroup(cmd, groupPolicy.ID,
		format.NewCommand(),
		check.NewCommand(),
		eval.NewCommand(),
		explain.NewCommand(),
		test.NewCommand(),
	)
	addToGroup(cmd, groupKind.ID,
		breaking.NewCommand(),
		gen.NewCommand(),
	)
	addToGroup(cmd, groupEditor.ID,
		lsp.NewCommand(),
	)
	addToGroup(cmd, groupOther.ID,
		version.NewCommand(
			version.WithVersion(o.version),
			version.WithOutput(&outputFormat),
		),
	)

	return cmd
}

func addToGroup(parent *cobra.Command, groupID string, cmds ...*cobra.Command) {
	for _, c := range cmds {
		c.GroupID = groupID
	}
	parent.AddCommand(cmds...)
}
