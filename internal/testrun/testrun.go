// Package testrun runs test files against a project the way `sigil test`
// does, apart from any filesystem or terminal: the sigil CLI reads the
// files from disk, and the WebAssembly module from the virtual files of a
// request, so both run the same cases and print the same records.
//
// [Run] runs one test file's cases against the policy the file names,
// with that policy's kind. [SuiteResult] and [CaseResult] are the records
// `sigil test` prints as JSON and YAML; their fields without a JSON name
// hold what's behind an error, for a text report to render.
package testrun

import (
	"context"
	"io/fs"
	"reflect"
	"regexp"
	"strings"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/testsuite"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// SuiteResult is one test file's run. When Error is set, none of the
// file's cases ran.
type SuiteResult struct { //nolint:govet // the field order is the JSON's
	File   string       `json:"file" yaml:"file"`
	Policy string       `json:"policy" yaml:"policy"`                   // the policy the file tests
	Error  string       `json:"error,omitempty" yaml:"error,omitempty"` // an unreadable or invalid test file, or a policy that doesn't compile
	Cases  []CaseResult `json:"cases" yaml:"cases"`                     // the cases the filter selects, in file order
	// What's behind Error, for a text report to render with each hint
	// on its own line: the test file's problems, or the policy's compile
	// errors with Sources finding their source lines.
	Problems    []*testsuite.Error `json:"-" yaml:"-"`
	Diagnostics diag.ErrorList     `json:"-" yaml:"-"`
	Sources     diag.Sources       `json:"-" yaml:"-"`
}

// CaseResult is one test case's run.
type CaseResult struct { //nolint:govet // the field order is the JSON's
	Name     string              `json:"name" yaml:"name"`
	Error    string              `json:"error,omitempty" yaml:"error,omitempty"`       // why the case couldn't run: its input can't be read or doesn't fit the kind
	Failures []string            `json:"failures,omitempty" yaml:"failures,omitempty"` // how the evaluation differs from what the case expects
	Line     int                 `json:"line" yaml:"line"`                             // of the case in its test file
	Passed   bool                `json:"passed" yaml:"passed"`
	Problem  *testsuite.Error    `json:"-" yaml:"-"` // what's behind Error
	Diffs    []testsuite.Failure `json:"-" yaml:"-"` // what's behind Failures
}

// Unreadable is the result of a test file that couldn't be read.
func Unreadable(file string, err error) SuiteResult {
	return SuiteResult{File: file, Error: file + " couldn't be read: " + err.Error()}
}

// Run runs the cases of the test file named file, whose contents are src,
// against the policy it names, with that policy's kind, in p, which has
// been checked. Input files are read from inputs, by their paths relative
// to the test file's. With a filter, only the cases whose names it
// matches run. A test file that can't run is a result whose Error says
// why, and a case that fails is one whose Passed is false.
func Run(ctx context.Context, p *workspace.Project, file string, src []byte, inputs fs.FS, filter *regexp.Regexp) SuiteResult {
	res := SuiteResult{File: file}
	s, err := testsuite.Parse(file, src)
	if err != nil {
		res.Error = err.Error()
		switch e := err.(type) {
		case *testsuite.Error:
			res.Problems = []*testsuite.Error{e}
		case testsuite.Errors:
			res.Problems = e
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
	runner := &testsuite.Runner{Kind: g.Kind.Model, Binding: g.Kind.Binding, FS: inputs}
	if errs := s.Validate(runner.Kind); len(errs) > 0 {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
			if e.Help != "" {
				msgs[i] += " (" + e.Help + ")"
			}
		}
		res.Error = strings.Join(msgs, "\n")
		res.Problems = errs
		return res
	}
	// Validate has bound every stub, the file's and each case's, to a
	// binding synthesized from the kind: a kind file's own binding, and
	// for a linked kind one of the same Sigil types the host's Go types
	// carry. Binding them again, here and for a case, fails only if a
	// stub value fits one and not the other, a bug in sigil this reports
	// rather than hides.
	base, berr := runner.Bind(s, nil)
	if berr != nil {
		res.Error, res.Problems = berr.Display(), []*testsuite.Error{berr}
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
func runCase(ctx context.Context, p *workspace.Project, b *bundle.Bundle, runner *testsuite.Runner, s *testsuite.Suite, c *testsuite.Case, prog *eval.Policy) CaseResult {
	cr := CaseResult{Name: c.Name, Line: c.Line}
	if len(c.Stubs) > 0 {
		// Validate bound the case's stubs already, and a stub only replaces
		// a host function, which a compile only looks up, so neither error
		// below happens once the policy compiled with the file's stubs;
		// they guard against a bug in sigil.
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
	cr.Passed, cr.Diffs = r.Passed(), r.Failures
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
	cr.Passed, cr.Problem, cr.Error = false, err, err.Msg
	if err.Help != "" {
		cr.Error += " (" + err.Help + ")"
	}
	return cr
}

// failSuite records why a test file's policy couldn't run: the
// diagnostics behind it.
func failSuite(res SuiteResult, p *workspace.Project, errs diag.ErrorList) SuiteResult {
	res.Error = p.Render(errs)
	res.Diagnostics, res.Sources = p.Resolve(errs), p.SourceOf
	return res
}

// noPolicy describes a test file's policy that isn't in the project,
// listing what is.
func noPolicy(p *workspace.Project, name string) *diag.Error {
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
