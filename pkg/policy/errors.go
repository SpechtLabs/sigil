package policy

import "strings"

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
// or a host function that returned an error. The result that comes with it
// holds the kind's default.
type RuntimeError struct {
	Message  string   // what failed, such as "index 3 out of range for a list of 2"
	Policy   string   // the policy being evaluated
	Position Position // the expression that failed
}

// ConflictError is what [Policy.Eval] returns when resolution can't stand:
// two members of a [WithExclusive] set fired, or a `collect one` kind has
// several candidates at its top rank, such as two reasons of a decision
// without a [WithReasonPrecedence], or one reason with different payloads.
// It names the candidates on each side, and the result that comes with it
// holds the kind's default. A conflict is a defect in the policy rather
// than in the input; count it apart from assert failures.
type ConflictError struct {
	Message    string      // what conflicts
	Policy     string      // the policy being evaluated
	Candidates []Candidate // the candidates that conflict
}

// AssertionError is what [Policy.Eval] returns when an assert's condition
// was false. Asserts run in two phases: input asserts, which read only the
// inputs, run before any rule, and outcome asserts, which read `outcome`,
// run once the outcome exists. Every failing assert of the phase that
// stopped the evaluation is listed, sorted by position. The result that
// comes with it holds the kind's default for a kind with precedence and
// an empty outcome for a collecting kind.
type AssertionError struct {
	Failures []AssertFailure
}

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

// Location renders the failure's call chain and position, as
// [Candidate.Location] does.
func (f AssertFailure) Location() string {
	return chain(f.CallChain, f.Position)
}

func strconvQuote(s string) string { return `"` + s + `"` }
