package policy_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// HaltInput is the input of the Halt kind: a list for quantifiers to
// walk.
type (
	HaltInput struct {
		Items []int64 `policy:"items"`
	}
	AllowData struct{}
)

var (
	haltDeny  = policy.NewDecision[policy.None]("deny", "no_rule_matched", "blocked")
	haltAllow = policy.NewDecision[AllowData]("allow", "ok")
	errDown   = errors.New("registry down")
)

// halt is what the Halt kind's host functions act on: the cancel func
// of the evaluation's context, and that context for wait.
type halt struct {
	ctx    context.Context //nolint:containedctx // the context wait blocks on
	cancel context.CancelFunc
}

// TestEvalCancellation checks Eval under a context that's done before
// or during the evaluation: the context's error, unwrapped, with the
// fallback result and an empty trace, wherever the evaluation was.
func TestEvalCancellation(t *testing.T) {
	many := make([]int64, 5000)
	for i := range many {
		many[i] = int64(i)
	}
	tests := []struct {
		name    string
		src     string
		collect bool
		items   []int64
		timeout time.Duration // a deadline instead of a cancel
		before  bool          // cancel before calling Eval
		err     error         // nil when the evaluation completes
	}{
		{name: "done before it starts", src: "when true { allow(reason: ok) }", before: true, err: context.Canceled},
		{name: "canceled in a rule", src: "when stop(1) { allow(reason: ok) }", err: context.Canceled},
		{name: "canceled in a rule, collect all", src: "when stop(1) { allow(reason: ok) }", collect: true, err: context.Canceled},
		{name: "canceled after a candidate fired", src: "when true { allow(reason: ok) }\nwhen stop(1) { deny(reason: blocked) }", err: context.Canceled},
		{name: "canceled after a candidate fired, collect all", src: "when true { allow(reason: ok) }\nwhen stop(1) { deny(reason: blocked) }", collect: true, err: context.Canceled},
		{name: "canceled in an input assert", src: "assert(\"stops\", stop(1))\nwhen true { allow(reason: ok) }", err: context.Canceled},
		{name: "canceled in an outcome assert", src: "when true { allow(reason: ok) }\nassert(\"after\", allow in outcome and stop(1))", err: context.Canceled},
		{name: "canceled in a quantifier", src: "when all i in items: stop(i) { allow(reason: ok) }", items: []int64{1, 2, 3}, err: context.Canceled},
		{name: "deadline in a host function", src: "when wait(1) { allow(reason: ok) }", timeout: 10 * time.Millisecond, err: context.DeadlineExceeded},
		{name: "deadline in nested quantifiers", src: "when all a in items: any b in items: b == a { allow(reason: ok) }", items: many, timeout: 5 * time.Millisecond, err: context.DeadlineExceeded},
		{name: "live", src: "assert(\"listed\", all i in items: i >= 0)\nwhen all i in items: i >= 0 { allow(reason: ok) }", items: []int64{1, 2, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &halt{}
			if tt.timeout > 0 {
				h.ctx, h.cancel = context.WithTimeout(context.Background(), tt.timeout)
			} else {
				h.ctx, h.cancel = context.WithCancel(context.Background())
			}
			defer h.cancel()
			if tt.before {
				h.cancel()
			}
			p := compileHalt(t, haltKind(h, tt.collect), tt.src)
			res, err := p.Eval(h.ctx, HaltInput{Items: tt.items})
			if res == nil {
				t.Fatal("Eval returned a nil result")
			}
			if tt.err == nil {
				if err != nil || res.Decision != "allow" && !tt.collect {
					t.Fatalf("Eval() = %s, %v; want allow(reason: ok)", res.Decision, err)
				}
				return
			}
			if err != tt.err { //nolint:errorlint // the context's own error, unwrapped
				t.Fatalf("error = %v (%T), want %v unwrapped", err, err, tt.err)
			}
			checkFallback(t, res, tt.collect)
		})
	}
}

// TestRecoverHostPanics checks a host function's panic with and without
// WithRecoverHostPanics: recovered, it's a *RuntimeError caused by a
// *HostPanicError, and the policy fails closed; otherwise it propagates.
func TestRecoverHostPanics(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		recover bool
		collect bool
		value   string // the panic value as text; empty when nothing panics
		runtime bool   // the panic value is a runtime.Error
		assert  bool   // the panic is an assert's cause
		live    bool   // evaluate under a context that can be done, rather than context.Background()
	}{
		{name: "not recovered", src: "when boom(1) { allow(reason: ok) }", value: "kaboom"},
		{name: "not recovered, under a context that can be done", src: "when boom(1) { allow(reason: ok) }", value: "kaboom", live: true},
		{name: "recovered", src: "when boom(1) { allow(reason: ok) }", recover: true, value: "kaboom"},
		{name: "recovered, collect all", src: "when boom(1) { allow(reason: ok) }", recover: true, collect: true, value: "kaboom"},
		{name: "recovered runtime error", src: "when boom(0) { allow(reason: ok) }", recover: true, value: "runtime error: index out of range [0] with length 0", runtime: true},
		{name: "recovered in an assert", src: "assert(\"safe\", boom(1))\nwhen true { allow(reason: ok) }", recover: true, value: "kaboom", assert: true},
		{name: "recovered, no panic", src: "when boom(2) { allow(reason: ok) }", recover: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &halt{ctx: context.Background(), cancel: func() {}}
			k := haltKind(h, tt.collect)
			if tt.recover {
				k = haltKind(h, tt.collect, policy.WithRecoverHostPanics())
			}
			p := compileHalt(t, k, tt.src)
			if !tt.recover {
				defer func() {
					if r := recover(); fmt.Sprint(r) != tt.value {
						t.Errorf("panic = %v, want %q", r, tt.value)
					}
				}()
			}
			ctx := context.Background()
			if tt.live {
				ctx = t.Context()
			}
			res, err := p.Eval(ctx, HaltInput{})
			if !tt.recover {
				t.Fatalf("Eval() = %v, %v; want a panic", res, err)
			}
			if tt.value == "" {
				if err != nil {
					t.Fatalf("Eval() error = %v", err)
				}
				return
			}
			re := runtimeCause(t, err, tt.assert)
			if want := "host function boom panicked: " + tt.value; !strings.HasSuffix(re.Message, want) {
				t.Errorf("Message = %q, want %q", re.Message, want)
			}
			hp, ok := errors.AsType[*policy.HostPanicError](re)
			if !ok {
				t.Fatalf("errors.As(%v, *HostPanicError) = false", re)
			}
			if hp.Func != "boom" || fmt.Sprint(hp.Value) != tt.value || hp.Error() != "host function boom panicked: "+tt.value {
				t.Errorf("HostPanicError = %q, %v, %q", hp.Func, hp.Value, hp.Error())
			}
			if !strings.Contains(string(hp.Stack), "haltKind") || strings.Contains(re.Message, "goroutine") {
				t.Errorf("the stack is missing, or in the message:\nstack: %s\nmessage: %s", hp.Stack, re.Message)
			}
			if _, isRuntime := errors.AsType[runtime.Error](re); isRuntime != tt.runtime {
				t.Errorf("errors.As(runtime.Error) = %v, want %v", isRuntime, tt.runtime)
			}
			if !tt.assert {
				checkFallback(t, res, tt.collect)
			}
		})
	}
}

// TestRuntimeErrorCause checks that a *RuntimeError keeps what caused
// it: the host function's error, for errors.Is, and the evaluator's help.
// The message stays as it was.
func TestRuntimeErrorCause(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		items  []int64
		msg    string
		help   string
		cause  error // what errors.Is finds; nil for an error in the policy itself
		assert bool
	}{
		{name: "host error", src: "when lookup(1) { allow(reason: ok) }", msg: "host function lookup failed: lookup 1: registry down", cause: errDown},
		{name: "host error in an assert", src: "assert(\"up\", lookup(1))\nwhen true { allow(reason: ok) }", msg: "host function lookup failed: lookup 1: registry down", cause: errDown, assert: true},
		{name: "unbound host function", src: "when lookup(0) { allow(reason: ok) }", msg: "host function lookup failed: ", help: "this sigil binary has only lookup's signature from the kind file"},
		{name: "index out of range", src: "when items[5] > 0 { allow(reason: ok) }", items: []int64{1}, msg: "index 5 out of range for a list of 1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &halt{ctx: context.Background(), cancel: func() {}}
			p := compileHalt(t, haltKind(h, false), tt.src)
			res, err := p.Eval(context.Background(), HaltInput{Items: tt.items})
			re := runtimeCause(t, err, tt.assert)
			if !strings.HasPrefix(re.Message, tt.msg) {
				t.Errorf("Message = %q, want it to start with %q", re.Message, tt.msg)
			}
			if !strings.HasPrefix(re.Help, tt.help) || (tt.help == "" && re.Help != "") {
				t.Errorf("Help = %q, want %q", re.Help, tt.help)
			}
			if re.Error() != re.Position.String()+": "+re.Message {
				t.Errorf("Error() = %q, want the position and the message", re.Error())
			}
			switch {
			case tt.cause != nil && !errors.Is(re, tt.cause):
				t.Errorf("errors.Is(%v, %v) = false", re, tt.cause)
			case tt.cause == nil && tt.help == "" && re.Unwrap() != nil:
				t.Errorf("Unwrap() = %v, want nil for an error in the policy", re.Unwrap())
			}
			if !tt.assert {
				checkFallback(t, res, false)
			}
		})
	}
}

// TestAssertPhase checks that an *AssertionError says which phase
// failed, including an outcome assert that fails with nothing fired,
// whose trace is as empty as a failed input assert's.
func TestAssertPhase(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		collect bool
		items   []int64
		want    policy.AssertPhase
		label   string
	}{
		{name: "input assert", src: "assert(\"positive\", all i in items: i > 0)\nwhen true { allow(reason: ok) }", items: []int64{0}, want: policy.InputAsserts, label: "input"},
		{name: "input assert raising", src: "assert(\"third\", items[2] > 0)\nwhen true { allow(reason: ok) }", want: policy.InputAsserts, label: "input"},
		{name: "outcome assert", src: "when true { allow(reason: ok) }\nassert(\"no grant\", not (allow in outcome))", want: policy.OutcomeAsserts, label: "outcome"},
		{name: "outcome assert, nothing fired", src: "when all i in items: i > 9 { allow(reason: ok) }\nassert(\"granted\", allow in outcome)", items: []int64{1}, want: policy.OutcomeAsserts, label: "outcome"},
		{name: "outcome assert, nothing fired, collect all", src: "when all i in items: i > 9 { allow(reason: ok) }\nassert(\"granted\", allow in outcome)", collect: true, items: []int64{1}, want: policy.OutcomeAsserts, label: "outcome"},
		{name: "outcome assert raising", src: "when true { allow(reason: ok) }\nassert(\"third\", allow in outcome and items[2] > 0)", want: policy.OutcomeAsserts, label: "outcome"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &halt{ctx: context.Background(), cancel: func() {}}
			p := compileHalt(t, haltKind(h, tt.collect), tt.src)
			_, err := p.Eval(context.Background(), HaltInput{Items: tt.items})
			ae, ok := errors.AsType[*policy.AssertionError](err)
			if !ok {
				t.Fatalf("error = %v (%T), want an *AssertionError", err, err)
			}
			if ae.Phase != tt.want || ae.Phase.String() != tt.label {
				t.Errorf("Phase = %v (%d), want %v", ae.Phase, ae.Phase, tt.want)
			}
		})
	}
	if got := policy.AssertPhase(0).String(); got != "none" {
		t.Errorf("AssertPhase(0).String() = %q, want none", got)
	}
}

// haltKind builds the Halt kind, ranked or collecting, with host
// functions that act on h: stop cancels h's context, wait blocks until
// it's done, boom panics, and lookup fails.
func haltKind(h *halt, collect bool, opts ...policy.Option) *policy.Kind[HaltInput] {
	opts = append(opts,
		policy.WithVersion(1),
		policy.WithFunc("stop", func(int64) bool { h.cancel(); return true }),
		policy.WithFunc("wait", func(int64) bool { <-h.ctx.Done(); return true }),
		policy.WithFunc("boom", func(n int64) bool {
			switch n {
			case 0:
				return make([]bool, n)[n] // index out of range, a runtime.Error
			case 1:
				panic("kaboom")
			}
			return true
		}),
		policy.WithFunc("lookup", func(n int64) (bool, error) {
			if n == 0 {
				return false, &gokind.ErrUnbound{Name: "lookup"}
			}
			return false, fmt.Errorf("lookup %d: %w", n, errDown)
		}),
	)
	if collect {
		opts = append(opts, policy.WithCollect(haltDeny, haltAllow))
	} else {
		opts = append(opts, policy.WithDecisions(haltDeny, haltAllow), policy.WithDefault(haltDeny.Reason("no_rule_matched")))
	}
	return policy.NewKind[HaltInput]("Halt", opts...)
}

// compileHalt compiles the body of policy p against k.
func compileHalt(t *testing.T, k *policy.Kind[HaltInput], body string) *policy.Policy[HaltInput] {
	t.Helper()
	p, err := k.Compile("policy p: Halt@1\n"+body, "p")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// runtimeCause returns the *RuntimeError behind err: err itself, or the
// cause of the only failing assert.
func runtimeCause(t *testing.T, err error, assert bool) *policy.RuntimeError {
	t.Helper()
	if assert {
		ae, ok := errors.AsType[*policy.AssertionError](err)
		if !ok || len(ae.Failures) != 1 || ae.Failures[0].Cause == nil {
			t.Fatalf("error = %v, want one assert failed by a runtime error", err)
		}
		return ae.Failures[0].Cause
	}
	re, ok := errors.AsType[*policy.RuntimeError](err)
	if !ok {
		t.Fatalf("error = %v (%T), want a *RuntimeError", err, err)
	}
	return re
}

// checkFallback checks a failed evaluation's result: the default for a
// ranked kind, an empty outcome for a collecting one, and no trace.
func checkFallback(t *testing.T, res *policy.Result, collect bool) {
	t.Helper()
	switch {
	case collect && len(res.Outcome) != 0:
		t.Errorf("Outcome = %v, want empty", res.Outcome)
	case !collect && (res.Decision != "deny" || res.Reason != "no_rule_matched" || len(res.Outcome) != 1):
		t.Errorf("result = %s(%s), want the default deny(reason: no_rule_matched)", res.Decision, res.Reason)
	}
	if len(res.Trace.Candidates) != 0 {
		t.Errorf("Trace = %v, want empty", res.Trace.Candidates)
	}
}
