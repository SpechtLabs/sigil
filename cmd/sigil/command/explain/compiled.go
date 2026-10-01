package explain

import (
	"fmt"
	"io"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// newCompiledCommand returns the explain command of a compiled binary,
// which explains the policies compiled into it, and nothing else.
func newCompiledCommand(o *options) *cobra.Command {
	root := o.payload.Bundle.Root
	what, policyHelp := "each policy compiled into this binary", "Name or pattern of the compiled policies to explain; every one when omitted"
	if root != "" {
		what, policyHelp = root, "Name or pattern of the compiled policies to explain; "+root+" when omitted"
	}
	cmd := &cobra.Command{
		Use:        "explain",
		SuggestFor: []string{"flatten", "expand", "show"},
		Short:      "List every decision the compiled policy can produce",
		Long: `Lists every decision ` + what + ` can produce, and the conditions
under which it does. Every policy it invokes is inlined, and params show as the
values they are bound to, so the list answers what the policy actually does
without reading its source.

--policy names another compiled policy to explain, or a pattern such as
'payments.*' to explain several, one after another.`,
		Example: fmt.Sprintf(`# List every decision the policy can produce, and under which conditions
%[1]s explain

# Explain every compiled policy whose name starts with payments.
%[1]s explain --policy 'payments.*'

# Print the decisions as JSON, for a review tool
%[1]s explain -o json`, o.name),
		Args:              noPaths,
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pattern, _ := cmd.Flags().GetString("policy")
			return runCompiled(cmd.OutOrStdout(), o, pattern)
		},
	}

	cmd.Flags().StringP("policy", "p", "", policyHelp)
	// This only fails for an undefined flag, which the tests would catch.
	_ = cmd.RegisterFlagCompletionFunc("policy", complete.Compiled(&o.payload.Bundle))
	return cmd
}

// runCompiled explains the compiled policies pattern matches, or the
// bundle's root, or every compiled policy when it has none.
func runCompiled(out io.Writer, o *options, pattern string) humane.Error {
	proj, err := project.LoadFiles(project.FromBundle(&o.payload.Bundle), o.kinds)
	if err != nil {
		return err
	}
	if pattern == "" {
		pattern = o.payload.Bundle.Root
	}
	return explain(out, o, proj, pattern)
}

// noPaths rejects args: a compiled binary explains the policies compiled
// into it.
func noPaths(_ *cobra.Command, args []string) error {
	if len(args) == 0 {
		return nil
	}
	return humane.New(
		fmt.Sprintf("explain takes no paths, got %s: this binary has its policies compiled in", strings.Join(args, " ")),
		"name the compiled policies to explain with --policy, such as --policy 'payments.*'",
	)
}
