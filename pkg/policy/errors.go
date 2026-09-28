package policy

import "strings"

// CompileError is what Compile and Load return when the bundle doesn't
// compile: every diagnostic found, in source order, each with a position
// and a fix hint. The message quotes the offending lines the way the
// CLI does.
type CompileError struct {
	rendered    string
	Diagnostics []Diagnostic
}

// Diagnostic is one compile error.
type Diagnostic struct {
	Message  string
	Help     string   // how to fix it; empty when there's no obvious fix
	Position Position // start of the offending source; unknown for a rule of the kind with no source
	End      Position // just after the offending source
}

// RuntimeError is what Eval returns when a policy can't be evaluated
// against an input: a list index out of range, integer overflow, or a
// host function that returned an error. The result that comes with it
// holds the kind's default.
type RuntimeError struct {
	Message  string
	Policy   string   // the policy being evaluated
	Position Position // the expression that failed
}

// ConflictError is what Eval returns when resolution can't stand: two
// members of an exclusive set fired, or a `collect one` kind has several
// candidates at its top rank. It names the candidates on each side, and
// the result that comes with it holds the kind's default. A conflict is a
// defect in the policy rather than in the input; count it apart from
// assert failures.
type ConflictError struct {
	Message    string
	Policy     string      // the policy being evaluated
	Candidates []Candidate // the candidates that conflict
}

// AssertionError is what Eval returns when an assert's condition was
// false. Every failing assert of the phase that stopped the evaluation,
// input or outcome, is listed, sorted by position, and the result that
// comes with it holds the kind's default for a kind with precedence and
// an empty outcome for a collecting kind.
type AssertionError struct {
	Failures []AssertFailure
}

// AssertFailure is one assert that didn't hold, or that couldn't be
// checked because its condition, or an enclosing one, raised a runtime
// error, which Cause then holds.
type AssertFailure struct {
	Cause     *RuntimeError
	Reason    string
	Policy    string      // the policy the assert is in
	CallChain []Position  // the invocations it was reached through; empty until composition lands
	Outcome   []Candidate // for an outcome assert, the candidates that formed the outcome it read
	Position  Position    // of the assert in its policy
}

func (e *CompileError) Error() string { return e.rendered }

func (e *RuntimeError) Error() string { return e.Position.String() + ": " + e.Message }

func (e *ConflictError) Error() string {
	var b strings.Builder
	b.WriteString("conflict: " + e.Message)
	for _, c := range e.Candidates {
		b.WriteString("\n  " + c.String())
	}
	return b.String()
}

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

// Location renders the failure's call chain and position.
func (f AssertFailure) Location() string {
	return chain(append(append([]Position(nil), f.CallChain...), f.Position))
}

func strconvQuote(s string) string { return `"` + s + `"` }
