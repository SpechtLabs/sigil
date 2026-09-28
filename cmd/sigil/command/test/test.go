// Package test implements the `sigil test` command.
package test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/testsuite"
)

// NewCommand returns the test command.
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
whole outcome of a collect all kind under outcome:, in any order; or the
reasons of the asserts that fail. Inputs follow the rules of sigil eval.

Every PATH is a file or a directory, searched recursively. The .sigil files
found form one bundle, and every test file found runs against it. With no
paths, test searches the current directory. Like eval, a case that calls a
host function needs a host binary with the functions linked in.`,
		Example: `# Run every test case under the current directory
sigil test --kind deploy_approval.sigil

# Run only the test cases for the production policies
sigil test --kind deploy_approval.sigil deploy/

# Run the test cases whose name matches a regular expression
sigil test --kind deploy_approval.sigil --run 'freeze'`,
		Args:              cobra.ArbitraryArgs,
		ValidArgsFunction: complete.SigilFiles,
		RunE: func(cmd *cobra.Command, args []string) error {
			kindFile, _ := cmd.Flags().GetString("kind")
			run, _ := cmd.Flags().GetString("run")
			verbose, _ := cmd.Flags().GetBool("verbose")
			if len(args) == 0 {
				args = []string{"."}
			}
			return runTests(cmd.Context(), cmd.OutOrStdout(), o, kindFile, run, verbose, args)
		},
	}

	cmd.Flags().StringP("kind", "k", "", "Kind file the policies are written against; optional in a binary with the kind linked in")
	cmd.Flags().String("run", "", "Only run test cases whose name matches this regular expression")
	cmd.Flags().BoolP("verbose", "v", false, "List every test case, not only the ones that fail")
	// These only fail for an undefined flag, which the tests would catch.
	_ = cmd.MarkFlagFilename("kind", "sigil")
	_ = cmd.RegisterFlagCompletionFunc("run", cobra.NoFileCompletions)

	return cmd
}

// SuiteResult is one test file's run.
type SuiteResult struct {
	File   string       `json:"file" yaml:"file"`
	Policy string       `json:"policy" yaml:"policy"`
	Error  string       `json:"error,omitempty" yaml:"error,omitempty"`
	Cases  []CaseResult `json:"cases" yaml:"cases"`
}

// CaseResult is one test case's run.
type CaseResult struct {
	Name     string   `json:"name" yaml:"name"`
	Error    string   `json:"error,omitempty" yaml:"error,omitempty"`
	Failures []string `json:"failures,omitempty" yaml:"failures,omitempty"`
	Line     int      `json:"line" yaml:"line"`
	Passed   bool     `json:"passed" yaml:"passed"`
}

// osFS reads input files by their paths on disk, relative to the working
// directory or absolute, as the command line gave them.
type osFS struct{}

// Open opens a file by its path on disk.
func (osFS) Open(name string) (fs.File, error) { return os.Open(name) } //nolint:gosec,wrapcheck,humaneerror // fs.FS fixes the signature; input files are named by test files the user asked to run

func runTests(ctx context.Context, out io.Writer, o *options, kindFile, run string, verbose bool, paths []string) humane.Error {
	var filter *regexp.Regexp
	if run != "" {
		re, err := regexp.Compile(run)
		if err != nil {
			return humane.Wrap(err, "--run isn't a valid regular expression", "--run takes a Go regular expression matched against case names")
		}
		filter = re
	}
	k, err := project.LoadKind(kindFile, o.kinds)
	if err != nil {
		return err
	}
	sources, tests, err := find(paths)
	if err != nil {
		return err
	}
	if len(tests) == 0 {
		return humane.New("no test files among "+strings.Join(paths, ", "), "test files are YAML files named *_test.yaml, next to the policies they test")
	}
	b, err := k.Bundle(project.Sources{Paths: sources})
	if err != nil {
		return err
	}
	b.Check()
	if errs := b.Errors(); errs != nil {
		return humane.New(b.Render(errs), "fix the documents above; test needs a bundle that checks")
	}
	runner := &testsuite.Runner{Kind: k.Model, Binding: k.Binding, FS: osFS{}}
	var results []SuiteResult
	for _, file := range tests {
		results = append(results, runSuite(ctx, runner, b, file, filter))
	}
	if err := write(out, results, *o.output, verbose); err != nil {
		return err
	}
	return summary(results)
}

// runSuite runs one test file's cases.
func runSuite(ctx context.Context, runner *testsuite.Runner, b *bundle.Bundle, file string, filter *regexp.Regexp) SuiteResult {
	res := SuiteResult{File: file}
	src, err := os.ReadFile(file) //nolint:gosec // the path was found under the command line's paths
	if err != nil {
		res.Error = file + " couldn't be read: " + err.Error()
		return res
	}
	s, err := testsuite.Parse(file, src)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	res.Policy = s.Policy
	if errs := s.Validate(runner.Kind); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
			if e.Help != "" {
				msgs[i] += " (" + e.Help + ")"
			}
		}
		res.Error = strings.Join(msgs, "\n")
		return res
	}
	prog, errs := b.Compile(s.Policy, bundle.Options{Binding: runner.Binding})
	if errs != nil {
		res.Error = b.Render(errs)
		return res
	}
	evaluate := evaluator(prog)
	for _, c := range s.Cases {
		if filter != nil && !filter.MatchString(c.Name) {
			continue
		}
		r := runner.RunCase(ctx, s, c, evaluate)
		cr := CaseResult{Name: c.Name, Line: c.Line, Passed: r.Passed(), Failures: r.Failures}
		if r.Err != nil {
			cr.Error = r.Err.Msg
			if r.Err.Help != "" {
				cr.Error += " (" + r.Err.Help + ")"
			}
		}
		res.Cases = append(res.Cases, cr)
	}
	return res
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
		out.Err = "a runtime error (" + f.Runtime.Position.String() + ": " + f.Runtime.Msg + ")"
	case f.Conflict != nil:
		out.Err = "a conflict (" + f.Conflict.Msg + ")"
	default:
		for _, a := range f.Asserts {
			out.Asserts = append(out.Asserts, a.Reason)
		}
	}
	return out
}

// find walks the paths for .sigil files and test files. Entries whose
// names start with `.` are skipped, as the loader does.
func find(paths []string) (sources, tests []string, err humane.Error) {
	for _, p := range paths {
		info, serr := os.Stat(p)
		if serr != nil {
			return nil, nil, humane.Wrap(serr, p+" can't be read", "name a directory, a .sigil file or a test file")
		}
		if !info.IsDir() {
			if testsuite.IsTestFile(p) {
				tests = append(tests, p)
			} else {
				sources = append(sources, p)
			}
			continue
		}
		werr := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if path != p && strings.HasPrefix(d.Name(), ".") {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			switch {
			case d.IsDir():
			case strings.HasSuffix(path, ".sigil"):
				sources = append(sources, path)
			case testsuite.IsTestFile(path):
				tests = append(tests, path)
			}
			return nil
		})
		if werr != nil {
			return nil, nil, humane.Wrap(werr, p+" couldn't be searched", "check the directory's permissions")
		}
	}
	sort.Strings(tests)
	return sources, tests, nil
}

// summary fails the command when a case failed or a test file couldn't
// run.
func summary(results []SuiteResult) humane.Error {
	var broken, failed, total int
	for _, s := range results {
		if s.Error != "" {
			broken++
		}
		for _, c := range s.Cases {
			total++
			if !c.Passed {
				failed++
			}
		}
	}
	var parts []string
	if failed > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d test cases failed", failed, total))
	}
	if broken > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d test files couldn't run", broken, len(results)))
	}
	if len(parts) == 0 {
		return nil
	}
	return humane.New(strings.Join(parts, ", and "), "each failure above says what the policy decided and what the case expected, or why the file couldn't run")
}

// write prints the results.
func write(out io.Writer, results []SuiteResult, format output.Format, verbose bool) humane.Error {
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
		_, err = io.WriteString(out, text(results, verbose))
	}
	if err != nil {
		return humane.Wrap(err, "the results couldn't be written", "check where the output is going")
	}
	return nil
}

// text renders the results the way go test does.
func text(results []SuiteResult, verbose bool) string {
	var b strings.Builder
	for _, s := range results {
		writeSuite(&b, s, verbose)
	}
	return b.String()
}

// writeSuite renders one test file: its failing cases, every case with
// verbose, and a summary line.
func writeSuite(b *strings.Builder, s SuiteResult, verbose bool) {
	if s.Error != "" {
		fmt.Fprintf(b, "FAIL  %s\n%s\n", s.File, indent(s.Error, "      "))
		return
	}
	failed := 0
	for _, c := range s.Cases {
		if c.Passed {
			if verbose {
				fmt.Fprintf(b, "--- PASS: %s:%d: %s\n", s.File, c.Line, c.Name)
			}
			continue
		}
		failed++
		fmt.Fprintf(b, "--- FAIL: %s:%d: %s\n", s.File, c.Line, c.Name)
		for _, msg := range append([]string{c.Error}, c.Failures...) {
			if msg != "" {
				b.WriteString(indent(msg, "      ") + "\n")
			}
		}
	}
	if failed > 0 {
		fmt.Fprintf(b, "FAIL  %s  %d of %d cases failed\n", s.File, failed, len(s.Cases))
		return
	}
	fmt.Fprintf(b, "ok    %s  %d %s\n", s.File, len(s.Cases), plural(len(s.Cases)))
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}

func plural(n int) string {
	if n == 1 {
		return "case"
	}
	return "cases"
}
