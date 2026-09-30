package policytest

import (
	"context"
	"fmt"
	"io/fs"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/testsuite"
	"github.com/spechtlabs/sigil/pkg/policy"
)

type (
	// runInput is the input of runKind.
	runInput struct {
		User string `policy:"user"`
	}

	// fake is a [tester] that records what failed. Fatal and FailNow end
	// the goroutine the test runs in, as testing.T's do, so every test
	// and subtest runs in its own.
	fake struct {
		log    *[]string // every failure, as "name: message", shared with the subtests
		name   string
		failed bool
	}

	// brokenFS is a file system whose every open fails.
	brokenFS struct{}
)

var (
	runAllow = policy.NewDecision[policy.None]("allow", "ok", "no_rule_matched")
	// runKind is a kind of one input and one host function, which says
	// every name is its own owner.
	runKind = policy.NewKind[runInput]("Run",
		policy.WithVersion(1),
		policy.WithDecisions(runAllow),
		policy.WithDefault(runAllow.Reason("no_rule_matched")),
		policy.WithFunc("owner", func(s string) string { return s }),
	)
)

// TestRunFailures runs test files that fail in every way Run reports,
// against a fake test, and checks what it records.
func TestRunFailures(t *testing.T) {
	const pol = "policy p: Run@1\n\nwhen owner(user) == \"ada\" {\n  allow(reason: ok)\n}\n"
	file := func(src string) fstest.MapFS {
		return fstest.MapFS{"p.sigil": {Data: []byte(pol)}, "p_test.yaml": {Data: []byte(src)}}
	}
	tests := []struct {
		name string
		kind *policy.Kind[runInput]
		fsys fs.FS
		want []string // the failures, in order, each as "test: message" with the message cut short
	}{
		{name: "no kind", fsys: file(""), want: []string{"run: policytest: Run needs a kind"}},
		{name: "an unreadable directory", kind: runKind, fsys: brokenFS{}, want: []string{"run: policytest: directory . couldn't be read"}},
		{name: "no test files", kind: runKind, fsys: fstest.MapFS{"p.sigil": {Data: []byte(pol)}}, want: []string{"run: policytest: no test files"}},
		{name: "a file that doesn't parse", kind: runKind, fsys: file("policy: [\n"), want: []string{"run/p_test.yaml: p_test.yaml: not a valid test file"}},
		{
			name: "a file that doesn't validate", kind: runKind,
			fsys: file("policy: p\ncases:\n  - name: a\n    input: {}\n    expect: {decision: alow, reason: ok}\n  - name: b\n    input: {}\n    expect: {}\n"),
			want: []string{`run/p_test.yaml: p_test.yaml:3: case "a": the kind has no decision "alow"`, `run/p_test.yaml: p_test.yaml:6: case "b": case must expect exactly one of`},
		},
		{
			name: "a policy that doesn't load", kind: runKind,
			fsys: fstest.MapFS{"p.sigil": {Data: []byte("policy p: Run@1\n\nwhen nope {\n  allow(reason: ok)\n}\n")}, "p_test.yaml": {Data: []byte("policy: p\ncases: []\n")}},
			want: []string{"run/p_test.yaml: policy p doesn't load:"},
		},
		{
			name: "cases that fail", kind: runKind,
			fsys: file("policy: p\ncases:\n  - name: a typo\n    input: {usr: ada}\n    expect: {decision: allow, reason: ok}\n  - name: a wrong reason\n    input: {user: bob}\n    expect: {decision: allow, reason: ok}\n  - name: a stubbed owner\n    input: {user: bob}\n    stubs: {owner: {returns: ada}}\n    expect: {decision: allow, reason: no_rule_matched}\n"),
			want: []string{
				`run/p_test.yaml/a typo: p_test.yaml:3: case "a typo": input usr: unknown input "usr"`,
				"run/p_test.yaml/a wrong reason: p_test.yaml:6: got allow(reason: no_rule_matched), want allow(reason: ok)",
				"run/p_test.yaml/a stubbed owner: p_test.yaml:9: got allow(reason: ok), want allow(reason: no_rule_matched)",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := record(func(ft tester) { run(ft, tt.kind, tt.fsys, nil) })
			if len(got) != len(tt.want) {
				t.Fatalf("failures =\n  %s\nwant\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
			for i, w := range tt.want {
				if !strings.HasPrefix(got[i], w) {
					t.Errorf("failure %d = %q, want it to start with %q", i, got[i], w)
				}
			}
		})
	}
}

// TestRunFileUnreadable covers a test file that disappears between
// listing and reading.
func TestRunFileUnreadable(t *testing.T) {
	got := record(func(ft tester) { runFile(ft, runKind, nil, fstest.MapFS{}, "gone_test.yaml", nil) })
	if len(got) != 1 || !strings.Contains(got[0], "gone_test.yaml") {
		t.Errorf("failures = %q, want the file not found", got)
	}
}

// TestEvaluatorWrongType covers an input of another type than the
// kind's, which the runner never decodes.
func TestEvaluatorWrongType(t *testing.T) {
	p, err := runKind.Compile("policy p: Run@1\n", "p")
	if err != nil {
		t.Fatal(err)
	}
	got := record(func(ft tester) { evaluator(ft, p)(context.Background(), reflect.ValueOf(42)) })
	if len(got) != 1 || !strings.Contains(got[0], "policytest: decoded int, want the input type") {
		t.Errorf("failures = %q", got)
	}
}

// TestOutcome checks how each way an evaluation fails reads to the
// runner.
func TestOutcome(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want testsuite.Outcome
	}{
		{name: "a conflict", err: &policy.ConflictError{Message: "two at the top"}, want: testsuite.Outcome{Err: "a conflict (two at the top)"}},
		{name: "a runtime error", err: &policy.RuntimeError{Message: "boom", Help: "fix it"}, want: testsuite.Outcome{Err: "a runtime error", Runtime: "boom", Detail: "-: boom", Help: "fix it"}},
		{name: "the context ending", err: context.Canceled, want: testsuite.Outcome{Err: "a runtime error (context canceled)"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := outcome(nil, tt.err); !reflect.DeepEqual(*got, tt.want) {
				t.Errorf("outcome() = %#v, want %#v", *got, tt.want)
			}
		})
	}
}

// TestTestingT checks that the adapter runs subtests on the real test.
func TestTestingT(t *testing.T) {
	ran := false
	if !(testingT{t}).Subtest("sub", func(sub tester) { ran = sub.Context() != nil }) || !ran {
		t.Error("testingT.Run didn't run the subtest")
	}
}

// Helper implements tester.
func (f *fake) Helper() {}

// Context implements tester.
func (f *fake) Context() context.Context { return context.Background() }

// Error implements tester by recording the failure.
func (f *fake) Error(args ...any) {
	f.failed = true
	*f.log = append(*f.log, f.name+": "+fmt.Sprint(args...))
}

// Errorf implements tester.
func (f *fake) Errorf(format string, args ...any) { f.Error(fmt.Sprintf(format, args...)) }

// Fatal implements tester: it records the failure and ends the test.
func (f *fake) Fatal(args ...any) {
	f.Error(args...)
	runtime.Goexit()
}

// Fatalf implements tester.
func (f *fake) Fatalf(format string, args ...any) { f.Fatal(fmt.Sprintf(format, args...)) }

// FailNow implements tester.
func (f *fake) FailNow() {
	f.failed = true
	runtime.Goexit()
}

// Subtest implements tester: it runs fn as a subtest in a goroutine of its
// own and waits for it.
func (f *fake) Subtest(name string, fn func(t tester)) bool {
	sub := &fake{log: f.log, name: f.name + "/" + name}
	inGoroutine(sub, fn)
	f.failed = f.failed || sub.failed
	return !sub.failed
}

// Open implements fs.FS by failing.
func (brokenFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

// record runs fn against a fake test called "run" and returns what
// failed.
func record(fn func(t tester)) []string {
	var log []string
	inGoroutine(&fake{log: &log, name: "run"}, fn)
	return log
}

// inGoroutine runs fn in a goroutine of its own, which Fatal may end, and
// waits for it.
func inGoroutine(t *fake, fn func(t tester)) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(t)
	}()
	<-done
}
