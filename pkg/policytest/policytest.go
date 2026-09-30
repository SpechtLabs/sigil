// Package policytest runs the test files `sigil test` runs, from go test,
// against a host's own kind: inputs decode into the host's Go types and
// rules call its real host functions, unless the test file stubs them.
// Policies then ship with table tests next to the code that consumes
// them.
//
//	//go:embed policies
//	var policies embed.FS
//
//	func TestPolicies(t *testing.T) {
//		sub, _ := fs.Sub(policies, "policies")
//		policytest.Run(t, deploy.Kind, sub, policy.Require("deploy.guardrails"))
//	}
//
//	func TestKindFileIsCurrent(t *testing.T) {
//		policytest.Schema(t, deploy.Kind, "policies/deploy_approval.sigil")
//	}
//
// # Test files
//
// A test file is a YAML file named `*_test.yaml` next to the policies. It
// names the root policy and lists cases, each an input and what the
// evaluation must produce:
//
//	policy: access.main
//	cases:
//	  - name: admins get eight hours
//	    input:
//	      user: {name: ada, admin: true}
//	    expect:
//	      decision: allow
//	      reason: admin
//	      payload:
//	        ttl: 8h
//	  - name: platform members get the default ttl
//	    input_file: testdata/member.json
//	    expect:
//	      decision: allow
//	      reason: team_member
//	  - name: an unnamed user fails the assert
//	    input:
//	      user: {name: ""}
//	    expect:
//	      asserts: [named_user]
//
// The complete format is described in the test file reference at
// https://sigil.specht-labs.de/reference/test-files/. A test file
// can't expect a conflict; test one with [policy.Policy.Eval] and
// [errors.As] on a [*policy.ConflictError].
//
// A test file's `stubs:` replace host functions for its cases, and a
// case's own `stubs:` replace those, even though the kind links the real
// ones, so a test can pin a function whose result changes from run to
// run:
//
//	stubs:
//	  owner: {returns: ada}
//
// A file's policy loads once with the file's stubs, and again for each
// case with stubs of its own, with the same load options.
// A stub's `error:` fails the evaluation with a runtime error, which a
// case expects with `expect: {error: <text the message contains>}`.
//
// # Subtests
//
// Every test file in the [io/fs.FS] is a subtest named after its path, and
// every case a subtest of it, so `go test -run` selects them:
//
//	go test -run 'TestPolicies/access/main_test.yaml/admins'
package policytest

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/testsuite"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// tester is what running test files needs of a test: a *testing.T, as
// [testingT] adapts it, or a fake that records what failed.
type tester interface {
	Helper()
	Context() context.Context
	Error(args ...any)
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
	FailNow()
	Subtest(name string, f func(t tester)) bool
}

// testingT is a *testing.T as a [tester].
type testingT struct{ *testing.T }

// suiteRun is one test file's run: the kind, runner, source and
// options its policy loads with, and the policy loaded with the file's
// stubs.
type suiteRun[In any] struct {
	kind   *policy.Kind[In]
	runner *testsuite.Runner
	fsys   fs.FS
	suite  *testsuite.Suite
	policy *policy.Policy[In]
	opts   []policy.LoadOption
}

// Run runs every `*_test.yaml` file in fsys, in every directory, against
// the policies in fsys. Each file's policy is loaded with [policy.Kind.Load]
// and opts, the same options a host passes to Load, such as
// [policy.Require] and [policy.Params]. Entries whose names start with
// `.` are skipped, as Load skips them.
//
// A test file that can't be read, doesn't parse, or whose policy doesn't
// compile, fails its subtest; each case that doesn't get what it expects
// fails its own. Run fails t at once when k is nil or fsys holds no test
// files.
func Run[In any](t *testing.T, k *policy.Kind[In], fsys fs.FS, opts ...policy.LoadOption) {
	t.Helper()
	run(testingT{t}, k, fsys, opts)
}

// Schema fails the test when the kind file at file, on disk, isn't k's
// [policy.Kind.Schema], so a stale export fails go test instead of a
// policy repository's CI. The failure shows both versions. Regenerate the
// file with `sigil export --out file` in a host binary built with
// [github.com/spechtlabs/sigil/pkg/cli], or by writing Schema() to it.
func Schema[In any](t testing.TB, k *policy.Kind[In], file string) {
	t.Helper()
	if k == nil {
		t.Fatal("policytest: Schema needs a kind")
	}
	got, err := os.ReadFile(file) //nolint:gosec // the test names the file, which is the point
	if err != nil {
		t.Fatalf("policytest: the kind file couldn't be read: %v", err)
	}
	if want := k.Schema(); string(got) != want {
		t.Errorf("%s is stale: it isn't %s's Schema(); regenerate it, for example with `sigil export --out %s` in a host binary\n--- got ---\n%s\n--- want ---\n%s", file, k.Name(), file, got, want)
	}
}

// run is Run with its test behind [tester].
func run[In any](t tester, k *policy.Kind[In], fsys fs.FS, opts []policy.LoadOption) {
	t.Helper()
	if k == nil {
		t.Fatal("policytest: Run needs a kind")
	}
	files, err := testFiles(fsys, ".")
	if err != nil {
		t.Fatalf("policytest: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("policytest: no test files; they're YAML files named *_test.yaml")
	}
	c := k.Contract()
	runner := &testsuite.Runner{Kind: c.Model, Binding: c.Binding, FS: fsys}
	for _, file := range files {
		t.Subtest(file, func(t tester) { runFile(t, k, runner, fsys, file, opts) })
	}
}

// runFile runs one test file's cases, each as a subtest.
func runFile[In any](t tester, k *policy.Kind[In], runner *testsuite.Runner, fsys fs.FS, file string, opts []policy.LoadOption) {
	t.Helper()
	src, err := fs.ReadFile(fsys, file)
	if err != nil {
		t.Fatal(err)
	}
	s, herr := testsuite.Parse(file, src)
	if herr != nil {
		t.Fatal(herr.Display())
	}
	if errs := s.Validate(runner.Kind); len(errs) > 0 {
		for _, e := range errs {
			t.Error(e.Display())
		}
		t.FailNow()
	}
	base, berr := runner.Bind(s, nil)
	if berr != nil {
		t.Fatal(berr.Display())
	}
	r := &suiteRun[In]{kind: k, runner: runner, fsys: fsys, suite: s, opts: opts}
	r.policy = r.load(t, base)
	for _, c := range s.Cases {
		t.Subtest(c.Name, func(t tester) { r.runCase(t, c) })
	}
}

// runCase runs one case: against the file's policy, or, when the case
// has stubs of its own, the policy loaded again with them.
func (r *suiteRun[In]) runCase(t tester, c *testsuite.Case) {
	t.Helper()
	p := r.policy
	if len(c.Stubs) > 0 {
		b, berr := r.runner.Bind(r.suite, c)
		if berr != nil {
			t.Fatal(berr.Display())
		}
		p = r.load(t, b)
	}
	res := r.runner.RunCase(t.Context(), r.suite, c, evaluator(t, p))
	if res.Err != nil {
		t.Fatal(res.Err.Display())
	}
	for _, f := range res.Failures {
		t.Errorf("%s:%d: %s", r.suite.File, c.Line, f)
	}
}

// load loads the file's policy with the host functions b holds: with
// the host's kind itself when b is its own binding, or a Kind of the
// same model over b when the test file stubs some of them. Everything
// else about the load, the options included, is the host's.
func (r *suiteRun[In]) load(t tester, b *gokind.Binding) *policy.Policy[In] {
	t.Helper()
	k := r.kind
	if b != r.runner.Binding {
		k = k.Contract().Rebind(b).(*policy.Kind[In]) //nolint:forcetypeassert // Rebind returns a Kind of the input type it was called on
	}
	p, err := k.Load(r.fsys, r.suite.Policy, r.opts...)
	if err != nil {
		t.Fatalf("policy %s doesn't load:\n%v", r.suite.Policy, err)
	}
	return p
}

// evaluator evaluates p for the runner, failing t on an input of the
// wrong type, which the runner never decodes.
func evaluator[In any](t tester, p *policy.Policy[In]) testsuite.Eval {
	return func(ctx context.Context, input reflect.Value) *testsuite.Outcome {
		in, ok := reflect.TypeAssert[In](input)
		if !ok {
			t.Fatalf("policytest: decoded %v, want the input type", input.Type())
		}
		return outcome(p.Eval(ctx, in))
	}
}

// Subtest runs f as a subtest of t called name.
func (t testingT) Subtest(name string, f func(t tester)) bool {
	return t.Run(name, func(sub *testing.T) { f(testingT{sub}) })
}

// outcome converts an evaluation into what the runner compares.
func outcome(res *policy.Result, err error) *testsuite.Outcome {
	out := &testsuite.Outcome{}
	if ae, ok := errors.AsType[*policy.AssertionError](err); ok {
		for _, f := range ae.Failures {
			out.Asserts = append(out.Asserts, f.Reason)
		}
		return out
	}
	if ce, ok := errors.AsType[*policy.ConflictError](err); ok {
		out.Err = "a conflict (" + ce.Message + ")"
		return out
	}
	if re, ok := errors.AsType[*policy.RuntimeError](err); ok {
		out.Err, out.Runtime, out.Detail, out.Help = "a runtime error", re.Message, re.Error(), re.Help
		return out
	}
	if err != nil {
		out.Err = "a runtime error (" + err.Error() + ")"
		return out
	}
	for _, e := range res.Outcome {
		out.Entries = append(out.Entries, testsuite.Got{Decision: e.Decision, Reason: e.Reason, Payload: e.Payload, Position: e.Position.String()})
	}
	return out
}

// testFiles lists the test files under dir, skipping entries whose names
// start with `.`, as the loader does.
func testFiles(fsys fs.FS, dir string) ([]string, humane.Error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, humane.Wrap(err, "directory "+dir+" couldn't be read", "check that the fs.FS holds the policies and their test files")
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		p := path.Join(dir, e.Name())
		info, err := fs.Stat(fsys, p)
		if err != nil {
			return nil, humane.Wrap(err, p+" can't be read", "a symbolic link there may be dangling")
		}
		switch {
		case info.IsDir():
			below, herr := testFiles(fsys, p)
			if herr != nil {
				return nil, herr
			}
			out = append(out, below...)
		case testsuite.IsTestFile(e.Name()):
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out, nil
}
