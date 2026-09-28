// Package policytest runs the test files `sigil test` runs, from go test,
// against a host's own kind: inputs decode into the host's Go types and
// rules call its real host functions. Policies then ship with table
// tests next to the code that consumes them.
//
//	//go:embed policies
//	var policies embed.FS
//
//	func TestPolicies(t *testing.T) {
//		sub, _ := fs.Sub(policies, "policies")
//		policytest.Run(t, deploy.Kind, sub)
//	}
//
// Every `*_test.yaml` file in the fs.FS is a subtest named after its
// path, and every case a subtest of it, so `go test -run` selects them.
// The test file format is described in the CLI reference, under
// `sigil test`.
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

	"github.com/spechtlabs/sigil/internal/testsuite"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Run runs every test file in fsys against the policies in fsys, loaded
// with k.Load and opts, the same options a host passes to Load. A test
// file that can't be read, or whose policy doesn't compile, fails its
// subtest; each case that doesn't get what it expects fails its own.
func Run[In any](t *testing.T, k *policy.Kind[In], fsys fs.FS, opts ...policy.LoadOption) {
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
		t.Run(file, func(t *testing.T) { runFile(t, k, runner, fsys, file, opts) })
	}
}

// Schema fails the test when the kind file at path, on disk, isn't k's
// Schema(), so a stale export fails go test instead of a policy
// repository's CI. Regenerate the file with `sigil export --out path` in
// a host binary, or by writing Schema() to it.
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

// runFile runs one test file's cases, each as a subtest.
func runFile[In any](t *testing.T, k *policy.Kind[In], runner *testsuite.Runner, fsys fs.FS, file string, opts []policy.LoadOption) {
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
	p, err := k.Load(fsys, s.Policy, opts...)
	if err != nil {
		t.Fatalf("policy %s doesn't load:\n%v", s.Policy, err)
	}
	eval := func(ctx context.Context, input reflect.Value) *testsuite.Outcome {
		in, ok := reflect.TypeAssert[In](input)
		if !ok {
			t.Fatalf("policytest: decoded %v, want the input type", input.Type())
		}
		return outcome(p.Eval(ctx, in))
	}
	for _, c := range s.Cases {
		t.Run(c.Name, func(t *testing.T) {
			r := runner.RunCase(t.Context(), s, c, eval)
			if r.Err != nil {
				t.Fatal(r.Err.Display())
			}
			for _, f := range r.Failures {
				t.Errorf("%s:%d: %s", file, c.Line, f)
			}
		})
	}
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
