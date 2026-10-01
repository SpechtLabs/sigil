package eval

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/report"
)

// compiledReport is the record a compiled binary's eval prints as JSON
// and YAML: the report, and the digest of the bundle it evaluated.
type compiledReport struct {
	*report.Report `yaml:",inline"`

	Bundle string `json:"bundle" yaml:"bundle"` // the compiled bundle's digest, as version prints it
}

// newCompiledCommand returns the eval command of a compiled binary,
// which evaluates the policies compiled into it, and nothing else.
func newCompiledCommand(o *options) *cobra.Command {
	root := o.payload.Bundle.Root
	what, flag, policyHelp := "one of the policies compiled into this binary, the one --policy names,", " --policy NAME", "Name of the compiled policy to evaluate; required, since the binary holds several"
	if root != "" {
		what, flag, policyHelp = root+", the policy this binary was compiled to evaluate,", "", "Name of the compiled policy to evaluate; "+root+" when omitted"
	}
	cmd := &cobra.Command{
		Use:        "eval",
		Aliases:    []string{"evaluate"},
		SuggestFor: []string{"run", "exec"},
		Short:      "Evaluate the compiled policy against an input and show the trace",
		Long: `Evaluates ` + what + `
against an input, and prints the decision with its full trace: every candidate,
the winner, and which of the winner's conditions held.

The input is a JSON or YAML object with one key per input the policy's kind
declares. A key the kind doesn't declare is an error, and a missing one reads as
its zero value. Durations are strings such as "1h30m", timestamps RFC 3339
strings, and only optionals, lists and maps may be null. --input names the file,
or "-" for stdin; without it, eval reads the input from stdin, unless stdin is a
terminal.

eval exits non-zero when the evaluation fails: on a runtime error, a conflict or
a failing assert. -o json and -o yaml print the decision as a record, with the
digest of the compiled bundle under bundle.`,
		Example: fmt.Sprintf(`# Evaluate the policy against an input file
%[1]s eval%[2]s --input input.json

# Pipe the input in, as JSON or YAML
yq '.input' request.yaml | %[1]s eval%[2]s

# Print the decision as JSON, for a script to act on
%[1]s eval%[2]s -o json < input.json`, o.name, flag),
		Args:              noPaths(o.name),
		ValidArgsFunction: cobra.NoFileCompletions,
		RunE: func(cmd *cobra.Command, _ []string) error {
			input, _ := cmd.Flags().GetString("input")
			name, _ := cmd.Flags().GetString("policy")
			return runCompiled(cmd.Context(), cmd.OutOrStdout(), o, request{
				src:      project.Sources{Stdin: cmd.InOrStdin()},
				input:    input,
				policy:   name,
				terminal: isTerminal(cmd.InOrStdin()),
			})
		},
	}

	cmd.Flags().StringP("input", "i", "", `Input document (JSON or YAML) to evaluate the policy against, or "-" for stdin; stdin when omitted and it isn't a terminal`)
	cmd.Flags().StringP("policy", "p", "", policyHelp)
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("input", "json", "yaml", "yml")
	_ = cmd.RegisterFlagCompletionFunc("policy", complete.Compiled(&o.payload.Bundle))
	return cmd
}

// runCompiled evaluates the policy req names, or the bundle's root,
// from the bundle compiled into the binary, against the input req names.
func runCompiled(ctx context.Context, out io.Writer, o *options, req request) humane.Error {
	input, err := inputFile(req.input, false, req.terminal, o.name+" eval < input.json")
	if err != nil {
		return err
	}
	p, err := project.LoadFiles(project.FromBundle(&o.payload.Bundle), o.kinds)
	if err != nil {
		return err
	}
	if req.policy == "" {
		req.policy = o.payload.Bundle.Root
	}
	k, prog, err := compileRoot(p, req)
	if err != nil {
		return err
	}
	return evaluate(ctx, out, o, k, prog, input, req.src.Stdin)
}

// noPaths rejects args: the policies of a compiled binary called name
// are compiled in, and the input comes with --input or on stdin.
func noPaths(name string) cobra.PositionalArgs {
	return func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return humane.New(
			fmt.Sprintf("eval takes no paths, got %s: this binary has its policies compiled in", strings.Join(args, " ")),
			"pass the input with --input or on stdin, such as `"+name+" eval < "+args[0]+"`",
		)
	}
}
