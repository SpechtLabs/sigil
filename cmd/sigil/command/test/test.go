// Package test implements the `sigil test` command. It walks its paths for
// .sigil files and *_test.yaml test files, checks the .sigil files as one
// bundle, then runs each test file's cases against the policy the file
// names. A case passes when the evaluation gives the decision, reason and
// payload it expects, the whole outcome of a collecting kind, or exactly
// the failing asserts it lists. [SuiteResult] and [CaseResult] are the
// records it prints as JSON and YAML.
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/testsuite"
)

// NewCommand returns the test command, configured by opts. Without
// [WithOutput] it prints text, and without [WithKinds] the kinds come from
// the paths and --kind.
func NewCommand(opts ...Option) *cobra.Command {
	format := output.Text
	o := &options{output: &format}
	for _, opt := range opts {
		opt(o)
	}

	cmd := &cobra.Command{
		Use:   "test [PATH...]",
		Short: "Run policy test cases",
		Long: `Runs test cases, each an input plus the expected decision and reason.
Asserting on the reason as well as the decision catches a deploy that is
denied for the wrong reason, a common way policy regressions hide.

Test cases live in YAML files named *_test.yaml next to the policies, one file
per policy:

  policy: payments.production
  cases:
    - name: pci deploy needs a review
      input_file: testdata/pci.json
      expect:
        decision: review
        reason: service_owner
        payload:
          approvers: [payments-leads, security-leads]
    - name: an unnamed actor fails the assert
      input: {actor: {name: ""}}
      expect:
        asserts: [named_actor]

A case expects a decision and reason, with the payload fields it lists; the
whole outcome of a collect all kind under outcome:, in any order; the
reasons of the asserts that fail; or, under error:, text the message of the
runtime error it fails with contains. Inputs follow the rules of sigil eval.

The stock sigil binary knows the host functions' signatures but not their
implementations. A test file's stubs: give a host function results, such as
"stubs: {owner: {returns: ada}}", and a case's own stubs: replace those, so a
case runs without the real function; in a host binary a stub replaces the real
one, which pins a function whose result changes from run to run.

Every PATH is a file or a directory; with no PATH, test searches the current
directory. A directory contributes every .sigil file and test file below it.
The .sigil files found form one bundle, and every test file found runs against
it, with the kind of the policy it names.

Each document's header names its kind, and the kind is found among the
inputs: a kind file among the paths, or a kind document in the same file as the
policies. --kind adds a kind file the paths don't hold, and so does the kinds:
list of the nearest sigil.yaml, or of the file --config names; a host binary
has its kinds linked in. The same kind from two sources must be identical, which
catches a stale export. sigil.yaml's require: trusted: paths are read
too, so a policy finds the required policies it uses.`,
		Example: `# Run every test case under the current directory, for every kind in it
sigil test

# Run only the test cases for the production policies, with a kind file kept elsewhere
sigil test --kind deploy_approval.sigil deploy/

# Run the test cases whose name matches a regular expression
sigil test --run 'freeze'`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFiles, _ := cmd.Flags().GetStringSlice("kind")
			run, _ := cmd.Flags().GetString("run")
			verbose, _ := cmd.Flags().GetBool("verbose")
			configFile, _ := cmd.Flags().GetString("config")
			return runTests(cmd.Context(), cmd.OutOrStdout(), o, configFile, project.Sources{Paths: args, Kinds: kindFiles, Stdin: cmd.InOrStdin()}, run, verbose)
		},
	}

	addFlags(cmd)
	return cmd
}

// addFlags declares the test command's flags.
func addFlags(cmd *cobra.Command) {
	cmd.Flags().StringSliceP("kind", "k", nil, "Kind file the paths don't hold; the policies' kinds are found among the paths and the kinds linked in (repeatable)")
	cmd.Flags().String("run", "", "Only run test cases whose name matches this regular expression")
	cmd.Flags().BoolP("verbose", "v", false, "List every test case, not only the ones that fail")
	cmd.Flags().String("config", "", "Configuration file with kind files and trusted paths to load; the nearest "+config.FileName+" when omitted")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.MarkFlagFilename("config", "yaml")
	_ = cmd.RegisterFlagCompletionFunc("run", cobra.NoFileCompletions)
}

// SuiteResult is one test file's run. When Error is set, none of the
// file's cases ran.
type SuiteResult struct {
	File   string       `json:"file" yaml:"file"`
	Policy string       `json:"policy" yaml:"policy"`                   // the policy the file tests
	Error  string       `json:"error,omitempty" yaml:"error,omitempty"` // an unreadable or invalid test file, or a policy that doesn't compile
	Cases  []CaseResult `json:"cases" yaml:"cases"`                     // the cases --run selects, in file order
	// What's behind Error, for the text report to render with each hint
	// on its own line: the test file's problems, or the policy's compile
	// errors with src finding their source lines.
	problems []*testsuite.Error
	diags    diag.ErrorList
	src      diag.Sources
}

// CaseResult is one test case's run.
type CaseResult struct {
	Name     string              `json:"name" yaml:"name"`
	Error    string              `json:"error,omitempty" yaml:"error,omitempty"`       // why the case couldn't run: its input can't be read or doesn't fit the kind
	Failures []string            `json:"failures,omitempty" yaml:"failures,omitempty"` // how the evaluation differs from what the case expects
	Line     int                 `json:"line" yaml:"line"`                             // of the case in its test file
	Passed   bool                `json:"passed" yaml:"passed"`
	problem  *testsuite.Error    // what's behind Error
	diffs    []testsuite.Failure // what's behind Failures
}

// osFS reads input files by their paths on disk, relative to the working
// directory or absolute, as the command line gave them.
type osFS struct{}

// Open opens a file by its path on disk.
func (osFS) Open(name string) (fs.File, error) { return os.Open(name) } //nolint:gosec,wrapcheck,humaneerror // fs.FS fixes the signature; input files are named by test files the user asked to run

func runTests(ctx context.Context, out io.Writer, o *options, configFile string, src project.Sources, run string, verbose bool) humane.Error {
	var filter *regexp.Regexp
	if run != "" {
		re, err := regexp.Compile(run)
		if err != nil {
			return humane.Wrap(err, "--run isn't a valid regular expression", "--run takes a Go regular expression matched against case names")
		}
		filter = re
	}
	if len(src.Paths) == 0 {
		src.Paths = []string{"."}
	}
	sources, tests, err := find(src.Paths)
	if err != nil {
		return err
	}
	if len(tests) == 0 {
		return humane.New("no test files among "+strings.Join(src.Paths, ", "), "test files are YAML files named *_test.yaml, next to the policies they test")
	}
	src.Paths = sources
	if err = config.Apply(configFile, ".", &src); err != nil {
		return err
	}
	p, err := project.Load(src, o.kinds)
	if err != nil {
		return err
	}
	p.Check()
	var results []SuiteResult
	for _, file := range tests {
		results = append(results, runSuite(ctx, p, file, filter))
	}
	return write(out, results, *o.output, verbose)
}

// runSuite runs one test file's cases against the policy it names, with
// that policy's kind.
func runSuite(ctx context.Context, p *project.Project, file string, filter *regexp.Regexp) SuiteResult {
	res := SuiteResult{File: file}
	src, err := os.ReadFile(file) //nolint:gosec // the path was found under the command line's paths
	if err != nil {
		res.Error = file + " couldn't be read: " + err.Error()
		return res
	}
	s, err := testsuite.Parse(file, src)
	if err != nil {
		res.Error = err.Error()
		switch e := err.(type) {
		case *testsuite.Error:
			res.problems = []*testsuite.Error{e}
		case testsuite.Errors:
			res.problems = e
		}
		return res
	}
	res.Policy = s.Policy
	g := p.Group(s.Policy)
	if g == nil {
		// The policy may be missing because its document or kind doesn't
		// check, which the project's diagnostics then say.
		errs := p.Errors()
		if errs == nil {
			errs = diag.ErrorList{noPolicy(p, s.Policy)}
		}
		return failSuite(res, p, errs)
	}
	// Only the policy, what it uses and the kinds count: an error in a
	// document it doesn't use doesn't stop its tests.
	scope := p.ScopeOf([]string{s.Policy})
	if errs := scope.Keep(p.Errors()); errs != nil {
		return failSuite(res, p, errs)
	}
	b := scope.Bundle(g)
	runner := &testsuite.Runner{Kind: g.Kind.Model, Binding: g.Kind.Binding, FS: osFS{}}
	if errs := s.Validate(runner.Kind); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
			if e.Help != "" {
				msgs[i] += " (" + e.Help + ")"
			}
		}
		res.Error = strings.Join(msgs, "\n")
		res.problems = errs
		return res
	}
	base, berr := runner.Bind(s, nil)
	if berr != nil {
		res.Error, res.problems = berr.Display(), []*testsuite.Error{berr}
		return res
	}
	prog, errs := b.Compile(s.Policy, bundle.Options{Binding: base})
	if errs != nil {
		return failSuite(res, p, errs)
	}
	for _, c := range s.Cases {
		if filter != nil && !filter.MatchString(c.Name) {
			continue
		}
		res.Cases = append(res.Cases, runCase(ctx, p, b, runner, s, c, prog))
	}
	return res
}

// runCase runs one case against prog, the policy compiled with the test
// file's stubs, or, when the case stubs host functions of its own, the
// policy compiled again with them: host functions are bound when a
// policy compiles.
func runCase(ctx context.Context, p *project.Project, b *bundle.Bundle, runner *testsuite.Runner, s *testsuite.Suite, c *testsuite.Case, prog *eval.Policy) CaseResult {
	cr := CaseResult{Name: c.Name, Line: c.Line}
	if len(c.Stubs) > 0 {
		cb, berr := runner.Bind(s, c)
		if berr != nil {
			return caseError(cr, berr)
		}
		var errs diag.ErrorList
		if prog, errs = b.Compile(s.Policy, bundle.Options{Binding: cb}); errs != nil {
			return caseError(cr, &testsuite.Error{File: s.File, Line: c.Line, Case: c.Name, Msg: "the policy doesn't compile with the case's stubs: " + p.Render(errs)})
		}
	}
	r := runner.RunCase(ctx, s, c, evaluator(prog))
	cr.Passed, cr.diffs = r.Passed(), r.Failures
	for _, f := range r.Failures {
		cr.Failures = append(cr.Failures, f.Text)
	}
	if r.Err != nil {
		return caseError(cr, r.Err)
	}
	return cr
}

// caseError records why a case couldn't run.
func caseError(cr CaseResult, err *testsuite.Error) CaseResult {
	cr.Passed, cr.problem, cr.Error = false, err, err.Msg
	if err.Help != "" {
		cr.Error += " (" + err.Help + ")"
	}
	return cr
}

// failSuite records why a test file's policy couldn't run: the
// diagnostics behind it.
func failSuite(res SuiteResult, p *project.Project, errs diag.ErrorList) SuiteResult {
	res.Error = p.Render(errs)
	res.diags, res.src = p.Resolve(errs), p.SourceOf
	return res
}

// noPolicy describes a test file's policy that isn't in the project,
// listing what is.
func noPolicy(p *project.Project, name string) *diag.Error {
	help := "the bundle defines no policies"
	if policies := p.Policies(); len(policies) > 0 {
		help = "the bundle defines: " + strings.Join(policies, ", ")
	}
	return &diag.Error{Msg: "bundle has no policy " + name, Help: help}
}

// evaluator evaluates the compiled policy for the runner.
func evaluator(prog *eval.Policy) testsuite.Eval {
	return func(_ context.Context, input reflect.Value) *testsuite.Outcome {
		return outcome(result.Evaluate(prog, input.Interface()))
	}
}

// outcome converts an evaluation into what the runner compares.
func outcome(res *result.Result) *testsuite.Outcome {
	out := &testsuite.Outcome{}
	switch f := res.Failure; {
	case f == nil:
		for _, e := range res.Outcome {
			out.Entries = append(out.Entries, testsuite.Got{Decision: e.Decision, Reason: e.Reason, Payload: e.Payload, Position: e.Position.String()})
		}
	case f.Runtime != nil:
		out.Err, out.Runtime, out.Help = "a runtime error", f.Runtime.Msg, f.Runtime.Help
		out.Detail = f.Runtime.Position.String() + ": " + f.Runtime.Msg
	case f.Conflict != nil:
		out.Err = "a conflict (" + f.Conflict.Msg + ")"
	default:
		for _, a := range f.Asserts {
			out.Asserts = append(out.Asserts, a.Reason)
		}
	}
	return out
}

// find expands the paths for .sigil files and test files, by the rules
// every command shares, and splits them. A file named on the command line
// is a test file by its name, and a .sigil file otherwise.
func find(paths []string) (sources, tests []string, err humane.Error) {
	files, err := project.Expand(paths, func(name string) bool {
		return project.IsSigil(name) || testsuite.IsTestFile(name)
	})
	if err != nil {
		return nil, nil, err
	}
	for _, f := range files {
		if testsuite.IsTestFile(f) {
			tests = append(tests, f)
		} else {
			sources = append(sources, f)
		}
	}
	sort.Strings(tests)
	return sources, tests, nil
}

// tally counts what happened across every test file.
type tally struct {
	files, broken, cases, failed int
}

func count(results []SuiteResult) tally {
	var n tally
	n.files = len(results)
	for _, s := range results {
		if s.Error != "" {
			n.broken++
		}
		for _, c := range s.Cases {
			n.cases++
			if !c.Passed {
				n.failed++
			}
		}
	}
	return n
}

// failure sums up what went wrong, or returns "" when nothing did. With
// files, it says how many files the cases came from.
func (n tally) failure(files bool) string {
	var parts []string
	if n.failed > 0 {
		cases := fmt.Sprintf("%d of %d test cases failed", n.failed, n.cases)
		if files {
			cases += fmt.Sprintf(" in %d %s", n.files, plural(n.files, "file", "files"))
		}
		parts = append(parts, cases)
	}
	if n.broken > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d test files couldn't run", n.broken, n.files))
	}
	return strings.Join(parts, ", and ")
}

// write prints the results and, in text, a line that sums them up, and
// fails when a case failed or a test file couldn't run.
func write(out io.Writer, results []SuiteResult, format output.Format, verbose bool) humane.Error {
	n := count(results)
	var err error
	switch format {
	case output.JSON:
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		err = enc.Encode(results)
	case output.YAML:
		enc := yaml.NewEncoder(out)
		enc.SetIndent(2)
		err = enc.Encode(results)
	default:
		p := pretty.New(out)
		if werr := p.Print(text(results, p.Theme(), verbose)); werr != nil {
			return werr
		}
		if werr := summarize(p, n); werr != nil {
			return werr
		}
	}
	if err != nil {
		return humane.Wrap(err, "the results couldn't be written", "check where the output is going")
	}
	if msg := n.failure(false); msg != "" {
		return pretty.Fail(msg, "each failure above says what the policy decided and what the case expected, or why the file couldn't run")
	}
	return nil
}

// summarize prints the closing line:
//
//	✓ 12 cases passed in 3 files
//	✗ 2 of 12 cases failed in 3 files
//	✗ 1 of 3 test files couldn't run
func summarize(p *pretty.Printer, n tally) humane.Error {
	switch {
	case n.failed > 0 || n.broken > 0:
		return p.Fail(n.failure(true))
	case n.cases == 0:
		return p.Warning("no test cases ran", "the --run pattern matched no case names")
	}
	return p.Ok(fmt.Sprintf("%d %s passed in %d %s", n.cases, plural(n.cases, "case", "cases"), n.files, plural(n.files, "file", "files")))
}

// text renders the results the way go test does.
func text(results []SuiteResult, t pretty.Theme, verbose bool) string {
	var b strings.Builder
	for _, s := range results {
		writeSuite(&b, t, s, verbose)
	}
	return b.String()
}

// writeSuite renders one test file: its failing cases, every case with
// verbose, and a summary line.
func writeSuite(b *strings.Builder, t pretty.Theme, s SuiteResult, verbose bool) {
	if s.Error != "" {
		detail := s.Error
		switch {
		case s.diags != nil:
			detail = diag.RenderAll(s.diags, s.src, t.Diagnostics())
		case s.problems != nil:
			parts := make([]string, len(s.problems))
			for i, e := range s.problems {
				parts[i] = problem(t, e.Error(), e.Help)
			}
			detail = strings.Join(parts, "\n")
		}
		fmt.Fprintf(b, "%s  %s\n%s\n", t.Fail("FAIL"), s.File, indent(detail, "      "))
		return
	}
	failed := 0
	for _, c := range s.Cases {
		if c.Passed {
			if verbose {
				fmt.Fprintf(b, "%s %s: %s\n", t.Ok("--- PASS:"), t.Location(fmt.Sprintf("%s:%d", s.File, c.Line)), c.Name)
			}
			continue
		}
		failed++
		fmt.Fprintf(b, "%s %s: %s\n", t.Fail("--- FAIL:"), t.Location(fmt.Sprintf("%s:%d", s.File, c.Line)), t.Bold(c.Name))
		if c.problem != nil {
			b.WriteString(indent(problem(t, c.problem.Msg, c.problem.Help), "      ") + "\n")
		}
		for _, f := range c.diffs {
			if f.Want == "" {
				b.WriteString(indent(f.Text, "      ") + "\n")
				continue
			}
			b.WriteString("      " + t.Key("want") + " " + f.Want + "\n")
			b.WriteString("      " + t.Key("got ") + " " + t.Fail(f.Got) + "\n")
			if f.Detail != "" {
				b.WriteString(indent(problem(t, f.Detail, f.Help), "           ") + "\n")
			}
		}
	}
	if failed > 0 {
		fmt.Fprintf(b, "%s  %s  %d of %d cases failed\n", t.Fail("FAIL"), s.File, failed, len(s.Cases))
		return
	}
	fmt.Fprintf(b, "%s    %s  %d %s\n", t.Ok("ok"), s.File, len(s.Cases), plural(len(s.Cases), "case", "cases"))
}

// problem renders a message with its hint on the line below, the way a
// diagnostic does.
func problem(t pretty.Theme, msg, help string) string {
	if help == "" {
		return msg
	}
	return msg + "\n  " + t.Help("= help:") + " " + help
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
