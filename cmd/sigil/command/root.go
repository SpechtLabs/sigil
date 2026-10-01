// Package command builds the sigil command tree: the root command with its
// global --output and --color flags, and every subcommand in its help group.
// [NewCommand] builds the tree and [Execute] runs it, with sigil's styling
// for help and errors, and turns the outcome into an exit status.
//
// Package [github.com/spechtlabs/sigil/pkg/cli] wraps this package for
// hosts. It re-exports [Option], [WithKind] and [WithVersion], so a host can
// build its own sigil binary, and cmd/sigil is cli.Main with a version.
//
// A binary that `sigil compile` wrote carries a bundle of policies, and
// NewCommand builds another tree for it: eval, explain, test and version,
// which work on the compiled policies only, named after the binary.
//
// Every subcommand lives in its own sub-package, exposes a NewCommand
// constructor, and receives each of its dependencies through a With* option
// declared in its options.go. The root hands each one a pointer to the
// --output value cobra parses, and the kinds linked in with [WithKind].
package command

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/command/breaking"
	"github.com/spechtlabs/sigil/cmd/sigil/command/check"
	"github.com/spechtlabs/sigil/cmd/sigil/command/compile"
	"github.com/spechtlabs/sigil/cmd/sigil/command/eval"
	"github.com/spechtlabs/sigil/cmd/sigil/command/explain"
	"github.com/spechtlabs/sigil/cmd/sigil/command/export"
	"github.com/spechtlabs/sigil/cmd/sigil/command/format"
	"github.com/spechtlabs/sigil/cmd/sigil/command/gen"
	"github.com/spechtlabs/sigil/cmd/sigil/command/lsp"
	"github.com/spechtlabs/sigil/cmd/sigil/command/test"
	"github.com/spechtlabs/sigil/cmd/sigil/command/version"
	"github.com/spechtlabs/sigil/internal/payload"
)

// Command groups, in the order help lists them. Every subcommand help lists
// belongs to one; the root assigns them so subcommand packages stay unaware
// of the layout. A group whose commands are all hidden, such as the planned
// ones, isn't added, so help shows no empty heading.
var (
	groupPolicy = &cobra.Group{ID: "policy", Title: "Policy commands"}
	groupKind   = &cobra.Group{ID: "kind", Title: "Kind commands"}
	groupEditor = &cobra.Group{ID: "editor", Title: "Editor integration"}
	groupOther  = &cobra.Group{ID: "other", Title: "Other commands"}
)

// NewCommand returns the sigil root command with every subcommand attached,
// configured by opts. Each call builds a new command tree. Run it with
// [Execute]. Its own [cobra.Command.Execute] runs the same commands, but
// returns errors without printing them, and --color has no effect.
//
// When the binary carries a compiled bundle ([payload.Embedded]), or
// [WithPayload] passes one, the tree is a compiled binary's: the root is
// named after the binary as it was run, and holds eval, explain, test and
// version, which evaluate, explain and test the compiled policies and
// read no policy file. When the bundle is damaged, each of them fails and
// says so.
func NewCommand(opts ...Option) *cobra.Command {
	o := &options{embedded: payload.Embedded}
	for _, opt := range opts {
		opt(o)
	}

	// Subcommands receive a pointer so they read the value cobra parsed.
	outputFormat := output.Text
	colorMode := output.ColorAuto

	var cmd *cobra.Command
	switch p, err := o.embedded(); {
	case err != nil:
		cmd = newDamaged(o, &outputFormat, err)
	case p != nil:
		cmd = newCompiled(o, p, &outputFormat)
	default:
		cmd = newStock(o, &outputFormat)
	}

	cmd.PersistentFlags().VarP(&outputFormat, "output", "o", "Output format: "+strings.Join(output.Formats, ", "))
	// Execute reads --color before the parse; the flag is declared so it's
	// documented, completed and accepted.
	cmd.PersistentFlags().Var(&colorMode, "color", "When to color the output: "+strings.Join(output.Colors, ", "))
	// These only fail if the flag is missing or already has a completion
	// func, both of which are programming errors caught by the tests.
	_ = cmd.RegisterFlagCompletionFunc("output", cobra.FixedCompletions(output.Formats, cobra.ShellCompDirectiveNoFileComp))
	_ = cmd.RegisterFlagCompletionFunc("color", cobra.FixedCompletions(output.Colors, cobra.ShellCompDirectiveNoFileComp))

	cmd.SetHelpCommandGroupID(groupOther.ID)
	cmd.SetCompletionCommandGroupID(groupOther.ID)
	return cmd
}

// newStock returns the stock sigil root, with every subcommand.
func newStock(o *options, outputFormat *output.Format) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sigil",
		Short: "Write, check and evaluate Sigil policies",
		Long: `Sigil evaluates host-provided input to a typed decision such as approve, deny
or review. Every decision carries a reason and a payload, every policy is
type-checked against a kind the host defines in Go, and every evaluation is
guaranteed to halt.`,
		Example: `# Type-check every policy in the current directory against its kind
sigil check

# Evaluate a policy against an input and see why it decided what it did
sigil eval --input release.json --policy payments.production

# Run the policy tests
sigil test`,
		// No Args validator: cobra then reports an unknown subcommand and
		// suggests the closest one.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addCommands(cmd, o, outputFormat)
	return cmd
}

// newCompiled returns the root of a binary with p compiled into it, named
// after the binary, with the commands that work on the compiled policies.
func newCompiled(o *options, p *payload.Payload, outputFormat *output.Format) *cobra.Command {
	name := filepath.Base(os.Args[0])
	short := "Evaluate the policies compiled into this binary"
	if p.Bundle.Root != "" {
		short = "Evaluate " + p.Bundle.Root + ", a policy compiled into this binary"
	}
	cmd := &cobra.Command{
		Use:     name,
		Short:   short,
		Long:    compiledLong(name, p),
		Example: compiledExample(name, p.Bundle.Root),
		// No Args validator: cobra then reports an unknown subcommand and
		// suggests the closest one.
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addCompiledCommands(cmd, o, p, name, outputFormat)
	return cmd
}

// newDamaged returns the root of a binary whose compiled bundle doesn't
// decode: it's damaged, or in a newer format than this binary reads. It
// holds the compiled binary's commands, so help reads as usual, and each
// of them fails with err, which says why and what to do.
func newDamaged(o *options, outputFormat *output.Format, err humane.Error) *cobra.Command {
	name := filepath.Base(os.Args[0])
	// The cause's advice is shown with it, so the advice here adds to it.
	msg, advice := "this binary's compiled policies are damaged", "if the binary was copied or downloaded, fetch it again and compare its checksum with the original's"
	if strings.Contains(err.Error(), "written by a newer sigil") {
		msg, advice = "this binary can't read the policies compiled into it", "or evaluate the policies with the newer sigil that compiled them, from their source files"
	}
	damaged := humane.Wrap(err, msg, advice)
	cmd := &cobra.Command{
		Use:   name,
		Short: "A binary whose compiled policies can't be read",
		Long: `This binary was written by sigil compile, but the policies compiled into it
can't be read, so every command fails. Compile it again with sigil compile.`,
		Example:       name + " version",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	addCompiledCommands(cmd, o, &payload.Payload{}, name, outputFormat)
	for _, c := range cmd.Commands() {
		c.Args = cobra.ArbitraryArgs
		c.RunE = func(*cobra.Command, []string) error { return damaged }
	}
	return cmd
}

// compiledLong is the root's description in a compiled binary called
// name, with p compiled in.
func compiledLong(name string, p *payload.Payload) string {
	what := `Evaluates the Sigil policies compiled into this binary against an input, the
one --policy names, and shows why it decided what it did. explain lists every
decision the policies can produce, and test runs test cases against them.`
	if p.Bundle.Root != "" {
		what = "Evaluates " + p.Bundle.Root + `, a Sigil policy compiled into this binary,
against an input, and shows why it decided what it did. explain lists every
decision the policy can produce, and test runs test cases against it.`
	}
	return what + `

The binary reads no policy files. It evaluates the bundle compiled into it,

  ` + p.Bundle.Digest() + `

which ` + name + ` version describes: the policies, their kinds, and the sigil that
compiled them.`
}

// compiledExample is the root's example in a compiled binary called
// name, whose bundle's root policy is root, or empty for none.
func compiledExample(name, root string) string {
	policy := ""
	if root == "" {
		policy = " --policy NAME"
	}
	return fmt.Sprintf(`# Evaluate the policy against an input and see why it decided what it did
%[1]s eval%[2]s < input.json

# List every decision the policy can produce, and under which conditions
%[1]s explain%[2]s

# Show which policies were compiled in, and by which sigil
%[1]s version`, name, policy)
}

// addCommands attaches every subcommand to root, in its help group. Each
// one reads the root --output flag through outputFormat. The planned
// commands, and export when no kind is linked, hide themselves.
func addCommands(root *cobra.Command, o *options, outputFormat *output.Format) {
	addToGroup(root, groupPolicy,
		format.NewCommand(format.WithOutput(outputFormat)),
		check.NewCommand(
			check.WithOutput(outputFormat),
			check.WithKinds(o.kinds),
		),
		eval.NewCommand(
			eval.WithOutput(outputFormat),
			eval.WithKinds(o.kinds),
		),
		explain.NewCommand(
			explain.WithOutput(outputFormat),
			explain.WithKinds(o.kinds),
		),
		test.NewCommand(
			test.WithOutput(outputFormat),
			test.WithKinds(o.kinds),
		),
		compile.NewCommand(
			compile.WithOutput(outputFormat),
			compile.WithKinds(o.kinds),
			compile.WithVersion(o.version),
		),
	)
	addToGroup(root, groupKind,
		export.NewCommand(
			export.WithOutput(outputFormat),
			export.WithKinds(o.kinds),
		),
		breaking.NewCommand(breaking.WithOutput(outputFormat)),
		gen.NewCommand(gen.WithOutput(outputFormat)),
	)
	addToGroup(root, groupEditor,
		lsp.NewCommand(lsp.WithOutput(outputFormat)),
	)
	addToGroup(root, groupOther,
		version.NewCommand(
			version.WithVersion(o.version),
			version.WithOutput(outputFormat),
		),
	)
}

// addCompiledCommands attaches the commands of a compiled binary called
// name, with p compiled in, to root: each works on p's bundle, with the
// kinds linked in.
func addCompiledCommands(root *cobra.Command, o *options, p *payload.Payload, name string, outputFormat *output.Format) {
	addToGroup(root, groupPolicy,
		eval.NewCommand(
			eval.WithOutput(outputFormat),
			eval.WithKinds(o.kinds),
			eval.WithPayload(p, name),
		),
		explain.NewCommand(
			explain.WithOutput(outputFormat),
			explain.WithKinds(o.kinds),
			explain.WithPayload(p, name),
		),
		test.NewCommand(
			test.WithOutput(outputFormat),
			test.WithKinds(o.kinds),
			test.WithPayload(p, name),
		),
	)
	addToGroup(root, groupOther,
		version.NewCommand(
			version.WithVersion(o.version),
			version.WithOutput(outputFormat),
			version.WithKinds(o.kinds),
			version.WithPayload(p, name),
		),
	)
}

// addToGroup attaches cmds to parent in group. The group is added to
// parent when help lists one of cmds; otherwise they are all hidden, and
// stay out of every group.
func addToGroup(parent *cobra.Command, group *cobra.Group, cmds ...*cobra.Command) {
	if slices.ContainsFunc(cmds, func(c *cobra.Command) bool { return !c.Hidden }) {
		parent.AddGroup(group)
		for _, c := range cmds {
			c.GroupID = group.ID
		}
	}
	parent.AddCommand(cmds...)
}
