package eval_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
)

// lateCtx is done from the start but says so only from its second Err
// call on, so the check EvalContext makes before it starts passes and
// the first poll inside the evaluation ends it: a deterministic way to
// reach the polls before rules and asserts.
type lateCtx struct {
	context.Context
	done  chan struct{}
	calls atomic.Int32
}

func newLateCtx() *lateCtx {
	c := &lateCtx{Context: context.Background(), done: make(chan struct{})}
	close(c.done)
	return c
}

func (c *lateCtx) Done() <-chan struct{} { return c.done }

func (c *lateCtx) Err() error {
	if c.calls.Add(1) == 1 {
		return nil
	}
	return context.Canceled
}

// TestLoopsPoll evaluates one expression per kind of loop in a frame
// whose context is done, which the loop's first step must notice, and in
// frames whose context isn't, where the loop runs to its value.
func TestLoopsPoll(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, cancelExpired := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelExpired()
	live := t.Context() // can be done, but isn't while the test runs

	tests := []struct {
		name string
		src  string
		want bool
		loop bool // whether the expression steps through a list or map
	}{
		{name: "all quantifier", src: `all r in actor.regions: r != ""`, want: true, loop: true},
		{name: "any quantifier", src: `any r in actor.regions: r == "none"`, want: false, loop: true},
		{name: "filter", src: `any r in (filter t in actor.regions: true): r == "none"`, want: false, loop: true},
		{name: "in", src: `"ap-1" in actor.regions`, want: true, loop: true},
		{name: "not in", src: `"x" not in actor.teams`, want: true, loop: true},
		{name: "all in", src: `actor.teams all in service.owners`, want: false, loop: true},
		{name: "any in", src: `actor.teams any in service.owners`, want: true, loop: true},
		{name: "one in", src: `actor.teams one in service.owners`, want: true, loop: true},
		{name: "exclusive in", src: `actor.teams exclusive in service.owners`, want: true, loop: true},
		{name: "has map", src: `service.labels has {"team": "payments"}`, want: true, loop: true},
		{name: "no loop", src: `service.tier == "critical" and service.labels has "team"`, want: true},
	}
	contexts := []struct {
		name string
		ctx  context.Context //nolint:containedctx // one table row's context
		err  error           // what a loop ends with, nil when it runs to its value
	}{
		{name: "canceled", ctx: canceled, err: context.Canceled},
		{name: "deadline", ctx: expired, err: context.DeadlineExceeded},
		{name: "live", ctx: live},
		{name: "background", ctx: context.Background()},
		{name: "bare frame"},
	}
	for _, tt := range tests {
		for _, c := range contexts {
			t.Run(tt.name+"/"+c.name, func(t *testing.T) {
				e, f := setup(t, tt.src, false)
				if c.ctx != nil {
					eval.WithContext(f, c.ctx)
				}
				got, err := runPolled(e, f)
				want := c.err
				if !tt.loop {
					want = nil
				}
				if !errors.Is(err, want) || (want == nil && err != nil) {
					t.Fatalf("error = %v, want %v", err, want)
				}
				if want == nil && got != tt.want {
					t.Errorf("value = %v, want %v", got, tt.want)
				}
			})
		}
	}
}

// TestEvalContext checks what EvalContext returns when its context is
// done before the evaluation, at a rule or at an assert, and that a
// context that stays live changes nothing.
func TestEvalContext(t *testing.T) {
	already, cancel := context.WithCancel(context.Background())
	cancel()
	live := t.Context() // can be done, but isn't while the test runs

	tests := []struct {
		name string
		src  string
		ctx  func() context.Context
		err  error  // nil when the evaluation completes
		want string // describe(out), when it completes
	}{
		{
			name: "done before it starts",
			src:  "policy p: Test@1\nwhen true { deny(reason: a) }",
			ctx:  func() context.Context { return already },
			err:  context.Canceled,
		},
		{
			name: "done at the first rule",
			src:  "policy p: Test@1\nwhen true { deny(reason: a) }",
			ctx:  func() context.Context { return newLateCtx() },
			err:  context.Canceled,
		},
		{
			name: "done at an input assert",
			src:  "policy p: Test@1\nassert(\"named\", actor.name != \"\")\nwhen true { deny(reason: a) }",
			ctx:  func() context.Context { return newLateCtx() },
			err:  context.Canceled,
		},
		{
			name: "done at an outcome assert",
			src:  "policy p: Test@1\nassert(\"denied\", deny in outcome)",
			ctx:  func() context.Context { return newLateCtx() },
			err:  context.Canceled,
		},
		{
			name: "live",
			src:  "policy p: Test@1\nassert(\"named\", actor.name != \"\")\nwhen all r in actor.regions: r != \"\" { deny(reason: a) }",
			ctx:  func() context.Context { return live },
			want: "deny a 3:40 [all r in actor.regions: r != \"\"] *",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := compilePolicy(t, tt.src, false, nil)
			out, err := p.EvalContext(tt.ctx(), &input)
			if tt.err != nil {
				if !errors.Is(err, tt.err) || out != nil {
					t.Fatalf("EvalContext() = %v, %v; want no outcome and %v", out, err, tt.err)
				}
				if errors.Unwrap(err) != nil {
					t.Errorf("error %T wraps something; want the context's own error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("EvalContext() error = %v", err)
			}
			if got := describe(out); got != tt.want {
				t.Errorf("candidates:\n%s\nwant:\n%s", got, tt.want)
			}
		})
	}
}

// TestEvalStatic checks that a policy compiled for explanation only
// refuses to run, through Eval and EvalContext alike.
func TestEvalStatic(t *testing.T) {
	src := "policy p: Test@1\nwhen true { deny(reason: a) }"
	k := compileKind(t)
	f, perrs := parser.ParseFile("p.sigil", []byte(src))
	if perrs != nil {
		t.Fatal(perrs)
	}
	doc := f.Docs[0].(*ast.PolicyDoc)
	c := check.New("p.sigil")
	c.Policy(doc, k)
	p, cerr := eval.CompilePolicy(&eval.Source{Doc: doc, Info: c.Info(), File: "p.sigil", Src: []byte(src)}, k, nil, nil, eval.Options{Static: true})
	if cerr != nil {
		t.Fatal(cerr)
	}
	if out, err := p.Eval(&input); out != nil || err == nil || err.Msg != "policy was compiled for explanation only and can't be evaluated" {
		t.Errorf("Eval() = %v, %v", out, err)
	}
	if out, err := p.EvalContext(context.Background(), &input); out != nil || err == nil {
		t.Errorf("EvalContext() = %v, %v", out, err)
	}
}

// TestHostPanic checks the cause a recovered host panic becomes: its
// message names the function and the value, and an error value is
// reachable through it.
func TestHostPanic(t *testing.T) {
	empty := 0
	rtErr := func() (err error) {
		defer func() { err, _ = recover().(error) }()
		_ = make([]bool, empty)[empty] // index out of range, a runtime.Error
		return nil
	}()
	tests := []struct {
		name   string
		value  any
		msg    string
		unwrap error
	}{
		{name: "string", value: "kaboom", msg: "host function owner panicked: kaboom"},
		{name: "error", value: rtErr, msg: "host function owner panicked: " + rtErr.Error(), unwrap: rtErr},
		{name: "int", value: 7, msg: "host function owner panicked: 7"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &eval.HostPanic{Value: tt.value, Func: "owner", Stack: []byte("stack")}
			if got := p.Error(); got != tt.msg {
				t.Errorf("Error() = %q, want %q", got, tt.msg)
			}
			if got := p.Unwrap(); got != tt.unwrap { //nolint:errorlint // the value itself, not a match
				t.Errorf("Unwrap() = %v, want %v", got, tt.unwrap)
			}
			if tt.unwrap != nil {
				if _, ok := errors.AsType[runtime.Error](p); !ok {
					t.Errorf("errors.As(runtime.Error) = false")
				}
			}
		})
	}
}

// runPolled runs e in f and returns its bool, or the context's error
// when a poll ended it. Any other panic fails the caller's test by
// propagating.
func runPolled(e eval.Expr, f *eval.Frame) (got bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			if err = eval.Canceled(r); err == nil {
				panic(r)
			}
		}
	}()
	v, rerr := eval.Run(e, f)
	if rerr != nil {
		return false, fmt.Errorf("runtime error: %w", rerr)
	}
	return eval.Bool(v), nil
}

// compileKind builds a Test kind with one decision, for a policy that
// needs no binding.
func compileKind(t *testing.T) *kind.Kind {
	t.Helper()
	k, _, errs := gokind.Build(gokind.Options{
		Name: "Test", Version: 1, Input: typeOf[Input](), Ranked: true,
		Decisions: []gokind.Decision{{Name: "deny", Payload: typeOf[None](), Reasons: []string{"a"}}},
		Default:   &gokind.Default{Decision: "deny", Reason: "a"},
	})
	if errs != nil {
		t.Fatal(errs)
	}
	return k
}
