package test

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// newCompiledCommand returns the test command of a compiled binary,
// which runs test files against the policies compiled into it.
func newCompiledCommand(o *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "test [PATH...]",
		Short: "Run test cases against the compiled policies",
		Long: `Runs test cases against the policies compiled into this binary, each case an
input plus the decision and reason it expects. Test cases live in YAML files
named *_test.yaml, one file per policy:

  policy: payments.production
  cases:
    - name: pci deploy needs a review
      input_file: testdata/pci.json
      expect:
        decision: review
        reason: service_owner

A case expects a decision and reason, with the payload fields it lists; the
whole outcome of a collect all kind under outcome:, in any order; the reasons
of the asserts that fail; or, under error:, text the message of the runtime
error it fails with contains. Inputs follow the rules of eval, and an
input_file is relative to the test file.

Every PATH is a test file or a directory; with no PATH, test searches the
current directory. A directory contributes every test file below it. The
policies always come from this binary: .sigil files among the paths are
ignored, so the tests check what the binary evaluates. A test file for a policy
that isn't compiled in is skipped, so the binary runs in a repository that holds
other policies' tests too; -v lists the files it skipped.`,
		Example: fmt.Sprintf(`# Run every test case under the current directory
%[1]s test

# Run the test cases in one directory, listing each one
%[1]s test -v tests/

# Run the test cases whose name matches a regular expression
%[1]s test --run 'freeze'`, o.name),
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: cobra.FixedCompletions([]cobra.Completion{"yaml", "yml"}, cobra.ShellCompDirectiveFilterFileExt),
		RunE: func(cmd *cobra.Command, args []string) error {
			run, _ := cmd.Flags().GetString("run")
			verbose, _ := cmd.Flags().GetBool("verbose")
			return runCompiled(cmd.Context(), cmd.OutOrStdout(), o, args, run, verbose)
		},
	}

	cmd.Flags().String("run", "", "Only run test cases whose name matches this regular expression")
	cmd.Flags().BoolP("verbose", "v", false, "List every test case, not only the ones that fail")
	// This only fails for an undefined flag, which the tests would catch.
	_ = cmd.RegisterFlagCompletionFunc("run", cobra.NoFileCompletions)
	return cmd
}

// runCompiled runs the test files under paths, the cases run matches,
// against the policies compiled into the binary. A test file for a
// policy that isn't compiled in is skipped, but finding only such files
// is an error.
func runCompiled(ctx context.Context, out io.Writer, o *options, paths []string, run string, verbose bool) humane.Error {
	filter, err := runFilter(run)
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	_, tests, err := find(paths)
	if err != nil {
		return err
	}
	p, err := project.LoadFiles(project.FromBundle(&o.payload.Bundle), o.kinds)
	if err != nil {
		return err
	}
	results := suites(ctx, p, tests, filter, true)
	var skipped []string
	for _, s := range results {
		if !s.Skipped {
			return write(out, results, *o.output, verbose)
		}
		if !slices.Contains(skipped, s.Policy) {
			skipped = append(skipped, s.Policy)
		}
	}
	return humane.New(
		"no test files for the compiled policies among "+strings.Join(paths, ", "),
		"the test files found test "+strings.Join(skipped, ", ")+", and this binary has "+strings.Join(p.Policies(), ", ")+" compiled in",
	)
}
