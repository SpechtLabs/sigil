package policy

import (
	"fmt"
	"strings"
)

// CompileError is what [Kind.Compile] and [Kind.Load] return when the
// bundle doesn't compile: every diagnostic found, in source order, each
// with a position and a fix hint. Its message quotes the offending lines
// the way the CLI does:
//
//	3:6 (gate): error: unknown name `teir`
//	  |
//	3 | when teir == "critical" {
//	  |      ^^^^
//	  = help: did you mean `tier`?
//
// Read Diagnostics to report the problems another way, for example as
// annotations in a review tool.
type CompileError struct {
	rendered    string
	Diagnostics []Diagnostic
}

// Diagnostic is one compile error of a [CompileError].
type Diagnostic struct {
	Message  string   // what's wrong, on one line
	Help     string   // how to fix it; empty when there's no obvious fix
	Position Position // start of the offending source; unknown for a rule of the kind with no source
	End      Position // just after the offending source
}

// RuntimeError is what [Policy.Eval] returns when a policy can't be
// evaluated against an input: a list index out of range, integer overflow,
// a Go value outside its enum ([WithEnum]), or a host function that
// returned an error, or panicked under [WithRecoverHostPanics]. The
// result that comes with it holds the kind's default.
//
// When a host function failed, Err holds what it returned, or a
// [*HostPanicError] when it panicked, and [errors.Is] and [errors.As]
// see through the RuntimeError to it:
//
//	if errors.Is(err, ErrRegistryDown) {
//		// the host function's own error, raised inside the policy
//	}
type RuntimeError struct {
	Err      error    // the host function's error or [*HostPanicError]; nil for an error in the policy itself
	Message  string   // what failed, such as "index 3 out of range for a list of 2"
	Help     string   // what to do about it, when the evaluator knows; empty otherwise
	Policy   string   // the policy being evaluated
	Position Position // the expression that failed
}

// HostPanicError is the Err of a [RuntimeError] when a host function
// panicked and the kind sets [WithRecoverHostPanics]. The RuntimeError's
// message names the function and the panic value; the stack is only
// here, for the host to log, since it's long and says nothing about the
// policy.
type HostPanicError struct {
	Value any    //nolint:emptyinterface // what the function passed to panic
	Func  string // the host function's name in the kind
	Stack []byte // the panicking goroutine's stack, as [runtime/debug.Stack] formats it
}

// ConflictError is what [Policy.Eval] returns when resolution can't stand:
// two members of a [WithExclusive] set fired, or a `collect one` kind has
// several candidates at its top rank, such as two reasons of a decision
// without a [WithReasonPrecedence], or one reason with different payloads.
// It names only the candidates that conflict: the top-rank tie, or the
// members of the exclusive set that fired; the result's trace has every
// candidate. The result that comes with it holds the kind's
// [WithConflict] outcome when it declares one, and its default otherwise,
// or an empty outcome for a collecting kind. A conflict is a defect in
// the policy rather than in the input; count it apart from assert
// failures, by the error, since without WithConflict the result's reason
// is the default's.
type ConflictError struct {
	Message    string      // what conflicts
	Policy     string      // the policy being evaluated
	Candidates []Candidate // only those that conflict, the top-rank tie or the exclusive set's members; the trace lists every candidate
}

// AssertionError is what [Policy.Eval] returns when an assert's condition
// was false. Asserts run in two phases: input asserts, which read only the
// inputs, run before any rule, and outcome asserts, which read `outcome`,
// run once the outcome exists. Every failing assert of the phase that
// stopped the evaluation is listed, sorted by position, and Phase says
// which phase that was. The result that comes with it holds the kind's
// default for a kind with precedence and an empty outcome for a
// collecting kind.
//
// The phase says whose fault the failure is: a failed input assert
// rejects the input, and a failed outcome assert means the policy
// produced an outcome it forbids, a defect in the policy. Read it from
// Phase rather than from the trace, which is empty both when an input
// assert failed and when an outcome assert failed with nothing fired.
type AssertionError struct {
	Failures []AssertFailure
	Phase    AssertPhase // which asserts failed: [InputAsserts] or [OutcomeAsserts]
}

// AssertPhase is the phase whose asserts an [AssertionError] reports.
type AssertPhase int

// The assert phases, in the order an evaluation runs them. The zero
// value is no phase, which no AssertionError from [Policy.Eval] has.
const (
	// InputAsserts are the asserts that read only the inputs, checked
	// before any rule runs.
	InputAsserts AssertPhase = iota + 1
	// OutcomeAsserts are the asserts that read `outcome`, checked once
	// the outcome exists.
	OutcomeAsserts
)

// AssertFailure is one assert of an [AssertionError] that didn't hold, or
// that couldn't be checked because its condition, or an enclosing one,
// raised a runtime error, which Cause then holds.
type AssertFailure struct {
	Cause     *RuntimeError // the runtime error that kept the assert from being checked; nil when its condition was false
	Reason    string        // the assert's reason, its first argument
	Policy    string        // the policy the assert is in
	CallChain []Position    // the invocations it was reached through; empty for an assert in the evaluated policy itself
	Outcome   []Candidate   // for an outcome assert, the candidates that formed the outcome it read
	Position  Position      // of the assert in its policy
}

// Error implements the error interface. It returns every diagnostic with
// the source lines it points at, as the CLI prints them.
func (e *CompileError) Error() string { return e.rendered }

// Error implements the error interface. It returns the position followed
// by the message.
func (e *RuntimeError) Error() string { return e.Position.String() + ": " + e.Message }

// Unwrap returns Err, the host function's error or panic.
func (e *RuntimeError) Unwrap() error { return e.Err }

// Error implements the error interface. It names the function and the
// panic value, without the stack.
func (e *HostPanicError) Error() string {
	return fmt.Sprintf("host function %s panicked: %v", e.Func, e.Value)
}

// Unwrap returns the panic value when it's an error, such as the
// [runtime.Error] of a nil dereference, so [errors.As] reaches it.
func (e *HostPanicError) Unwrap() error {
	err, _ := e.Value.(error)
	return err
}

// Error implements the error interface. It returns the message followed
// by one line per candidate, as [Candidate.String] renders it.
func (e *ConflictError) Error() string {
	var b strings.Builder
	b.WriteString("conflict: " + e.Message)
	for _, c := range e.Candidates {
		b.WriteString("\n  " + c.String())
	}
	return b.String()
}

// Error implements the error interface. It returns each failing assert's
// reason and location, with its runtime error when it has one.
func (e *AssertionError) Error() string {
	if len(e.Failures) == 1 {
		f := e.Failures[0]
		s := "assertion " + strconvQuote(f.Reason) + " failed at " + f.Location()
		if f.Cause != nil {
			s += ": " + f.Cause.Message
		}
		return s
	}
	var b strings.Builder
	b.WriteString("assertions failed:")
	for _, f := range e.Failures {
		b.WriteString("\n  " + strconvQuote(f.Reason) + " at " + f.Location())
		if f.Cause != nil {
			b.WriteString(": " + f.Cause.Message)
		}
	}
	return b.String()
}

// String returns "input" or "outcome", a label for metrics and logs, or
// "none" for the zero value.
func (p AssertPhase) String() string {
	switch p {
	case InputAsserts:
		return "input"
	case OutcomeAsserts:
		return "outcome"
	}
	return "none"
}

// Location renders the failure's call chain and position, as
// [Candidate.Location] does.
func (f AssertFailure) Location() string {
	return chain(f.CallChain, f.Position)
}

func strconvQuote(s string) string { return `"` + s + `"` }
