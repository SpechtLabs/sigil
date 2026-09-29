// Package eval implements the `sigil eval` command. It compiles the root
// policy of a bundle, decodes a JSON input into the kind's input types,
// evaluates the policy, and prints the decision with its full trace, as
// package report renders it. The command fails when the evaluation does:
// on a runtime error, a conflict or a failing assert.
package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"slices"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/internal/usage"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/report"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/result"
)

// NewCommand returns the eval command, configured by opts. Without
// [WithOutput] it prints text, and without [WithKinds] every run needs
// --kind.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "eval PATH...",
		Aliases:    []string{"evaluate"},
		SuggestFor: []string{"run", "exec"},
		Short:      "Evaluate a policy against a JSON input and show the trace",
		Long: `Evaluates a policy against a JSON input and prints the decision with its full
trace: every candidate, the winner, and which of the winner's conditions held.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents. All documents from all paths form one bundle, indexed by the names in
their headers. A directory contributes the .sigil files directly inside it, or
every one below it with --recursive. If the bundle holds exactly one policy,
that policy is evaluated; otherwise --policy names the one to evaluate.

The input is a JSON object with one key per input. A key the kind doesn't
declare is an error, and a missing one reads as its zero value. Durations are
strings in Sigil's syntax ("1h30m"), timestamps RFC 3339 strings, and only
optionals, lists and maps may be null.

The stock sigil binary knows the host functions' signatures but not their
implementations, so a rule that calls one fails with a runtime error. A host
builds its own sigil binary with its kind and functions linked in (see the
pkg/cli package); that binary evaluates with the real functions and needs no
--kind. eval exits non-zero when the evaluation fails: on a runtime error, a
conflict or a failing assert.`,
		Example: `# Evaluate the only policy in a file and print the decision with its trace
sigil eval --kind deploy_approval.sigil --input release.json gate.sigil

# Pick the root policy from the documents in two directories
sigil eval --kind deploy_approval.sigil --input release.json --policy payments.production deploy/ payments/

# Read the bundle from stdin, for example a rendered ConfigMap key
kustomize build . | yq '.data["policies.sigil"]' | sigil eval --kind deploy_approval.sigil --input release.json --policy payments.production -`,
		Args:              usage.AtLeast(1, "PATH"),
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFile, _ := cmd.Flags().GetString("kind")
			input, _ := cmd.Flags().GetString("input")
			name, _ := cmd.Flags().GetString("policy")
			recursive, _ := cmd.Flags().GetBool("recursive")
			src := project.Sources{Paths: args, Recursive: recursive, Stdin: cmd.InOrStdin()}
			return run(cmd.Context(), cmd.OutOrStdout(), o, kindFile, input, name, src)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policy is written against; optional in a binary with the kind linked in")
	cmd.Flags().StringP("input", "i", "", `Input document (JSON) to evaluate the policy against, or "-" for stdin (required)`)
	cmd.Flags().StringP("policy", "p", "", "Name of the policy to evaluate; required when the bundle holds more than one")
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories of directory arguments too")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagRequired("input")
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("input", "json")
	_ = cmd.RegisterFlagCompletionFunc("policy", cobra.NoFileCompletions)

	return cmd
}

func run(ctx context.Context, out io.Writer, o *options, kindFile, input, name string, src project.Sources) humane.Error {
	if input == "-" && slices.Contains(src.Paths, "-") {
		return humane.New("the input and the bundle can't both come from stdin", "pass the input with --input FILE, or name the bundle's files")
	}
	k, prog, err := compile(o, kindFile, name, src)
	if err != nil {
		return err
	}
	raw, err := readInput(input, src.Stdin)
	if err != nil {
		return err
	}
	in, derr := k.Binding.DecodeInput(k.Model, raw)
	if derr != nil {
		return decodeError(input, derr)
	}
	if err := ctx.Err(); err != nil {
		return humane.Wrap(err, "eval was interrupted", "run it again")
	}
	r := report.New(k, result.Evaluate(prog, in.Interface()))
	if err := write(out, r, *o.output); err != nil {
		return err
	}
	if r.Error != nil {
		return pretty.Fail("the evaluation failed; the host would act on the fallback shown above", r.Error.Help)
	}
	return nil
}

// compile loads the kind and the bundle and compiles the root policy.
func compile(o *options, kindFile, name string, src project.Sources) (*project.Kind, *eval.Policy, humane.Error) {
	k, err := project.LoadKind(kindFile, o.kinds)
	if err != nil {
		return nil, nil, err
	}
	b, err := k.Bundle(src)
	if err != nil {
		return nil, nil, err
	}
	b.Check()
	if errs := b.Errors(); errs != nil {
		return nil, nil, pretty.Diagnose(b.Resolve(errs), b.SourceOf, "the bundle doesn't check, so nothing was evaluated", "fix the errors above; sigil check reports every problem in a bundle at once")
	}
	root, err := project.Root(b.Policies(), name)
	if err != nil {
		return nil, nil, err
	}
	prog, errs := b.Compile(root, bundle.Options{Binding: k.Binding})
	if errs != nil {
		return nil, nil, pretty.Diagnose(b.Resolve(errs), b.SourceOf, "the policy doesn't compile, so nothing was evaluated", "fix the errors above; sigil check reports every problem in a bundle at once")
	}
	return k, prog, nil
}

// readInput reads and parses the input document.
func readInput(file string, stdin io.Reader) (any, humane.Error) {
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(file) //nolint:gosec // the path comes from the command line, which is the point
	}
	if err != nil {
		return nil, humane.Wrap(err, "the input couldn't be read", "pass a JSON file with --input, or - for stdin")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	if err := dec.Decode(&raw); err != nil {
		name := file
		if file == "-" {
			name = "the input from stdin"
		}
		return nil, humane.Wrap(err, name+" isn't valid JSON: "+err.Error(), "the input is a JSON object with one key per input the kind declares")
	}
	return raw, nil
}

// decodeError explains an input that doesn't fit the kind.
func decodeError(file string, err humane.Error) humane.Error {
	return humane.New(file+": "+err.Error(), append(err.Advice(), "the input is a JSON object with one key per input the kind declares")...)
}

// write prints the report in the chosen format.
func write(out io.Writer, r *report.Report, format output.Format) humane.Error {
	var err error
	switch format {
	case output.JSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		err = enc.Encode(r)
	case output.YAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		err = enc.Encode(r)
	default:
		p := pretty.New(out)
		return p.Print(r.Text(p.Theme()))
	}
	if err != nil {
		return humane.Wrap(err, "the result couldn't be written", "check where the output is going")
	}
	return nil
}
