package pretty

import (
	"errors"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/diag"
)

// Failed is the error of a command that found what it looked for and
// already reported it: a check with errors, a failing test run, an
// evaluation that didn't produce an outcome. The command's own summary
// line says so, so the error handler prints nothing more and the process
// exits with status 1.
type Failed struct {
	msg    string
	advice []string
}

// Fail returns a Failed error. The message and advice are for callers
// that read the error, such as tests; a terminal never sees them.
func Fail(msg string, advice ...string) *Failed {
	return &Failed{msg: msg, advice: advice}
}

// Error implements the error interface. It returns the summary.
func (f *Failed) Error() string { return f.msg }

// Display implements [humane.Error]. It returns the summary with its
// advice, as humane errors do.
func (f *Failed) Display() string {
	return display(f.msg, f.advice)
}

// Advice implements [humane.Error]. It returns what to do about it.
func (f *Failed) Advice() []string { return f.advice }

// Cause implements [humane.Error]. It returns nil: a Failed error has no
// cause beneath it.
func (f *Failed) Cause() error { return nil } //nolint:humaneerror // humane.Error's own Cause() returns error

// Reported reports whether err is a Failed error, or wraps one.
func Reported(err error) bool {
	_, ok := errors.AsType[*Failed](err)
	return ok
}

// Diagnostics is an error that carries diagnostics: a command that needs a
// bundle that checks, given one that doesn't. The handler renders the
// diagnostics, styled, before the error itself.
type Diagnostics struct {
	Errs   diag.ErrorList // the diagnostics, in the order they print
	Src    diag.Sources   // finds the source line each diagnostic quotes
	msg    string
	advice []string
}

// Diagnose returns a Diagnostics error for errs, whose source lines src
// finds. msg says what the diagnostics prevented, and advice what to do.
func Diagnose(errs diag.ErrorList, src diag.Sources, msg string, advice ...string) *Diagnostics {
	return &Diagnostics{Errs: errs, Src: src, msg: msg, advice: advice}
}

// Error implements the error interface. It returns the diagnostics in
// their plain form, then the message.
func (d *Diagnostics) Error() string {
	rendered := diag.RenderAll(d.Errs, d.Src, diag.Plain)
	if rendered == "" {
		return d.msg
	}
	return rendered + "\n\n" + d.msg
}

// Display implements [humane.Error]. It returns the diagnostics, the
// message and the advice.
func (d *Diagnostics) Display() string {
	return display(d.Error(), d.advice)
}

// Advice implements [humane.Error]. It returns what to do about it.
func (d *Diagnostics) Advice() []string { return d.advice }

// Cause implements [humane.Error]. It returns nil: the diagnostics are the
// whole story.
func (d *Diagnostics) Cause() error { return nil } //nolint:humaneerror // humane.Error's own Cause() returns error

// Message returns what the diagnostics prevented, without them.
func (d *Diagnostics) Message() string { return d.msg }

func display(msg string, advice []string) string {
	var b strings.Builder
	b.WriteString(msg)
	for _, a := range advice {
		b.WriteString("\n  - " + a)
	}
	return b.String()
}

// Both types satisfy humane.Error, so commands return them where they
// return any other error.
var (
	_ humane.Error = (*Failed)(nil)
	_ humane.Error = (*Diagnostics)(nil)
)
