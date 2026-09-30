// Package eval implements the `sigil eval` command. It compiles the root
// policy of a bundle, decodes a JSON or YAML input into the kind's input
// types, evaluates the policy, and prints the decision with its full
// trace, as package report renders it. The command fails when the evaluation does:
// on a runtime error, a conflict or a failing assert.
package eval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/report"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/stub"
)

// inputHelp describes the input, in advice on one eval can't use.
const inputHelp = "the input is a JSON or YAML object with one key per input the kind declares"

// request is what one run of eval was asked for on the command line.
type request struct {
	src    project.Sources
	input  string   // --input: a file, "-" for stdin, or empty to read stdin unless terminal is set
	policy string   // --policy: the root's name; empty for the bundle's only policy
	config string   // --config: the configuration file whose kinds: to load; the nearest one when empty
	stubs  string   // --stubs: a file of stubs; empty for none
	stub   []string // --stub: NAME=VALUE stubs, applied after the file's in order
	// terminal is set when stdin is a terminal, which eval never waits on
	// for an input nobody asked it to read.
	terminal bool
}

// NewCommand returns the eval command, configured by opts. Without
// [WithOutput] it prints text, and without [WithKinds] the kinds come from
// the paths and --kind.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:        "eval [PATH...]",
		Aliases:    []string{"evaluate"},
		SuggestFor: []string{"run", "exec"},
		Short:      "Evaluate a policy against an input and show the trace",
		Long: `Evaluates a policy against an input and prints the decision with its full
trace: every candidate, the winner, and which of the winner's conditions held.

Every PATH is a file, a directory, or "-" for stdin, and a file may hold several
documents; with no PATH, eval reads the current directory. A directory
contributes every .sigil file below it. All documents from all paths form one
bundle, indexed by the names in their headers. If the bundle holds exactly one policy,
that policy is evaluated; otherwise --policy names the one to evaluate.

Each document's header names its kind, and the kind is found among the
inputs: a kind file among the paths, or a kind document in the same file as the
policies. --kind adds a kind file the paths don't hold, and so does the kinds:
list of the configuration file (the nearest sigil.yaml, sigil.json or
sigil.toml, or the file --config names); a host binary has its kinds linked in.
The same kind from two sources must be identical, which catches a stale
export. The configuration's require: trusted: paths are read too, so a policy
finds the required policies it uses.

The input is a JSON or YAML object with one key per input. A key the kind
doesn't declare is an error, and a missing one reads as its zero value.
Durations are strings in Sigil's syntax ("1h30m"), timestamps RFC 3339 strings,
and only optionals, lists and maps may be null. --input names the file, or "-"
for stdin; without it, eval reads the input from stdin, unless stdin is a
terminal or the bundle comes from stdin.

The stock sigil binary knows the host functions' signatures but not their
implementations, so a rule that calls one fails with a runtime error. --stub
NAME=VALUE makes the host function NAME return VALUE, JSON or YAML, whatever
its args; --stubs names a YAML or JSON file of stubs in a test file's stubs:
format, which can answer particular args or fail the call. --stub applies after
the file, and a later stub of a function replaces an earlier one. A host builds
its own sigil binary with its kind and functions linked in (see the pkg/cli
package); that binary evaluates with the real functions, unless a stub replaces
one, and needs no kind file. eval exits non-zero when the evaluation fails: on a runtime error, a
conflict or a failing assert.`,
		Example: `# Evaluate the only policy in a file and print the decision with its trace
sigil eval --kind deploy_approval.sigil --input release.json gate.sigil

# Pick the root policy from the documents in two directories and the kind file
sigil eval --input release.json --policy payments.production deploy_approval.sigil deploy/ payments/

# Pipe the input in, as JSON or YAML, and read the policies from the current directory
yq '.release' request.yaml | sigil eval --policy payments.production

# Stub the host function split, which the stock binary has only the signature of
sigil eval --input release.json --policy payments.production --stub 'split=[eu, us]'

# Read a self-contained bundle from stdin, for example a rendered ConfigMap key
kustomize build . | yq '.data["policies.sigil"]' | sigil eval --input release.json --policy payments.production -`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(cmd.Context(), cmd.OutOrStdout(), o, newRequest(cmd, args))
		},
	}

	addFlags(cmd)
	return cmd
}

// addFlags declares the eval command's flags.
func addFlags(cmd *cobra.Command) {
	cmd.Flags().StringSliceP("kind", "k", nil, "Kind file the paths don't hold; the policy's kind is found among the paths and the kinds linked in (repeatable)")
	cmd.Flags().StringP("input", "i", "", `Input document (JSON or YAML) to evaluate the policy against, or "-" for stdin; stdin when omitted and it isn't a terminal`)
	cmd.Flags().StringP("policy", "p", "", "Name of the policy to evaluate; required when the bundle holds more than one")
	cmd.Flags().String("config", "", "Configuration file with kind files and trusted paths to load; the nearest "+config.Names+" when omitted")
	cmd.Flags().String("stubs", "", "YAML or JSON file of host function stubs, keyed by function name, as a test file's stubs:")
	cmd.Flags().StringArray("stub", nil, "NAME=VALUE: host function NAME returns VALUE, JSON or YAML, whatever the args; applied after --stubs (repeatable)")
	// -R read subdirectories before every command did; it stays so scripts
	// that pass it keep working.
	cmd.Flags().BoolP("recursive", "R", false, "Read .sigil files in subdirectories too; always on")
	_ = cmd.Flags().MarkDeprecated("recursive", "directories are always read recursively")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("input", "json", "yaml", "yml")
	_ = cmd.MarkFlagFilename("config", "yaml")
	_ = cmd.MarkFlagFilename("stubs", "yaml", "yml", "json")
	_ = cmd.RegisterFlagCompletionFunc("policy", complete.Policies)
}

func run(ctx context.Context, out io.Writer, o *options, req request) humane.Error {
	src := req.src
	if len(src.Paths) == 0 {
		src.Paths = []string{"."}
	}
	input, err := inputFile(req.input, slices.Contains(src.Paths, "-"), req.terminal)
	if err != nil {
		return err
	}
	k, prog, err := compile(o, req, src)
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
		return pretty.Fail("the evaluation failed; the host would act on the fallback shown above") // the report above already says what to do
	}
	return nil
}

// inputFile returns where the input comes from: the file --input names,
// or "-" for stdin, which is also where it comes from without --input,
// unless stdin holds the bundle or is a terminal.
func inputFile(input string, bundleFromStdin, terminal bool) (string, humane.Error) {
	switch {
	case input == "-" && bundleFromStdin:
		return "", humane.New("the input and the bundle can't both come from stdin", "pass the input with --input FILE, or name the bundle's files")
	case input != "":
		return input, nil
	case bundleFromStdin:
		return "", humane.New("--input is required when the bundle comes from stdin", "name the input file with --input")
	case terminal:
		return "", humane.New("--input is required when stdin is a terminal", "name the input file with --input, or pipe the input in, such as `sigil eval < release.json`")
	}
	return "-", nil
}

// newRequest reads what the command line asks for from its flags and
// args.
func newRequest(cmd *cobra.Command, args []string) request {
	kindFiles, _ := cmd.Flags().GetStringSlice("kind")
	input, _ := cmd.Flags().GetString("input")
	name, _ := cmd.Flags().GetString("policy")
	configFile, _ := cmd.Flags().GetString("config")
	stubsFile, _ := cmd.Flags().GetString("stubs")
	stubs, _ := cmd.Flags().GetStringArray("stub")
	return request{
		src:      project.Sources{Paths: args, Kinds: kindFiles, Stdin: cmd.InOrStdin()},
		input:    input,
		policy:   name,
		config:   configFile,
		stubs:    stubsFile,
		stub:     stubs,
		terminal: isTerminal(cmd.InOrStdin()),
	}
}

// isTerminal reports whether r is a terminal.
func isTerminal(r io.Reader) bool {
	f, ok := r.(interface{ Fd() uintptr })
	return ok && term.IsTerminal(f.Fd())
}

// compile loads the project, with the kind files the configuration lists,
// and compiles the root policy against its kind, which it returns with
// the compiled policy.
func compile(o *options, req request, src project.Sources) (*project.Kind, *eval.Policy, humane.Error) {
	if err := config.Apply(req.config, ".", &src); err != nil {
		return nil, nil, err
	}
	p, err := project.Load(src, o.kinds)
	if err != nil {
		return nil, nil, err
	}
	p.Check()
	root, err := project.Root(p.Policies(), req.policy)
	if err != nil {
		// Without a root there's no scope, and the policy asked for may be
		// missing because its document or kind doesn't check.
		if errs := p.Errors(); errs != nil {
			return nil, nil, pretty.Diagnose(p.Resolve(errs), p.SourceOf, "the bundle doesn't check, so nothing was evaluated", "fix the errors above; sigil check reports every problem in a bundle at once")
		}
		return nil, nil, err
	}
	// Only the root, what it uses and the kinds count: an error in a
	// document the root doesn't use doesn't stop its evaluation.
	s := p.ScopeOf([]string{root})
	if errs := s.Keep(p.Errors()); errs != nil {
		return nil, nil, pretty.Diagnose(errs, p.SourceOf, root+" doesn't check, so nothing was evaluated", "fix the errors above; sigil check reports every problem in a bundle at once")
	}
	g := p.Group(root)
	binding, err := stubs(g.Kind, req.stubs, req.stub)
	if err != nil {
		return nil, nil, err
	}
	prog, errs := s.Bundle(g).Compile(root, bundle.Options{Binding: binding})
	if errs != nil {
		return nil, nil, pretty.Diagnose(p.Resolve(errs), p.SourceOf, "the policy doesn't compile, so nothing was evaluated", "fix the errors above; sigil check reports every problem in a bundle at once")
	}
	return g.Kind, prog, nil
}

// stubs returns the binding the policy compiles with: the kind's, with
// the host functions the stubs file and then each --stub replace. Every
// stub that doesn't parse or fit the kind is reported at once: a file's
// at its line, in line order, then the flags'.
func stubs(k *project.Kind, file string, flags []string) (*gokind.Binding, humane.Error) {
	var set stub.Set
	var problems []string
	add := func(where string, errs ...*stub.Error) {
		for _, e := range errs {
			loc := where
			if e.Line > 0 {
				loc = fmt.Sprintf("%s:%d", file, e.Line)
			}
			problems = append(problems, loc+": "+e.Msg+" ("+e.Help+")")
		}
	}
	if file != "" {
		data, err := os.ReadFile(file) //nolint:gosec // the path comes from the command line, which is the point
		if err != nil {
			return nil, humane.Wrap(err, "the stubs file "+file+" couldn't be read", "name a YAML or JSON file of stubs with --stubs")
		}
		var errs []*stub.Error
		set, errs = stub.ParseDocument(data)
		add(file, errs...)
	}
	for _, flag := range flags {
		one, err := stub.ParseFlag(flag)
		if err != nil {
			add("--stub", err)
			continue
		}
		set = set.Merge(one)
	}
	if problems == nil {
		b, errs := set.Bind(k.Model, k.Binding)
		if errs == nil {
			return b, nil
		}
		add("--stub", fileFirst(errs)...)
	}
	return nil, humane.New(strings.Join(problems, "\n"), "a stub names a host function the kind declares and gives results of its type, as in a test file's stubs:")
}

// fileFirst orders Bind's problems for reading: a stubs file's first, in
// line order, as Bind sorts them, and then the flags', which have no line.
func fileFirst(errs []*stub.Error) []*stub.Error {
	out := make([]*stub.Error, 0, len(errs))
	for _, e := range errs {
		if e.Line > 0 {
			out = append(out, e)
		}
	}
	for _, e := range errs {
		if e.Line == 0 {
			out = append(out, e)
		}
	}
	return out
}

// readInput reads and parses the input document: as JSON, with its
// numbers kept as [json.Number], and as YAML when it isn't JSON.
func readInput(file string, stdin io.Reader) (any, humane.Error) {
	var data []byte
	var err error
	if file == "-" {
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(file) //nolint:gosec // the path comes from the command line, which is the point
	}
	if err != nil {
		return nil, humane.Wrap(err, "the input couldn't be read", "name a JSON or YAML file with --input, or - for stdin")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, humane.New(inputName(file)+" is empty", "pipe the input in, or name the input file with --input")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var raw any
	jerr := dec.Decode(&raw)
	if jerr == nil {
		return raw, nil
	}
	if yerr := yaml.Unmarshal(data, &raw); yerr != nil {
		return nil, humane.Wrap(errors.Join(jerr, yerr), fmt.Sprintf("%s is neither JSON (%v) nor YAML (%v)", inputName(file), jerr, yerr), inputHelp)
	}
	return raw, nil
}

// decodeError explains an input that doesn't fit the kind.
func decodeError(file string, err humane.Error) humane.Error {
	return humane.New(inputName(file)+": "+err.Error(), append(err.Advice(), inputHelp)...)
}

// inputName names the input in messages: its file, or the input from
// stdin.
func inputName(file string) string {
	if file == "-" {
		return "the input from stdin"
	}
	return file
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
		return p.Print(report.Text(r, p.Theme()))
	}
	if err != nil {
		return humane.Wrap(err, "the result couldn't be written", "check where the output is going")
	}
	return nil
}
