package build

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
)

// Site is a builder call in Go code: the file and line it's on, and the
// builder function or method called, named the way Go names it:
// "build.Field", "Expr.Eq" or "(*Block).When". Errors and the
// diagnostics of [Check] point there, since that's where to fix a
// document built in Go.
type Site struct {
	File string // the Go file, as the runtime reports it
	Call string // the builder function or method
	Line int
}

// Error is a mistake in the Go code that builds a document, found when
// the builder call was made or when the document rendered: the call, and
// what's wrong with it.
type Error struct {
	Msg string // what's wrong, one sentence without a trailing period
	Site
}

// Errors is every [Error] a document collected. The builder never stops
// at the first problem, so one run reports them all.
type Errors []*Error

// String formats s as `freeze.go:31: build.Field`, with the base name of
// the Go file.
func (s Site) String() string {
	return fmt.Sprintf("%s:%d: %s", filepath.Base(s.File), s.Line, s.Call)
}

// Error formats e as `freeze.go:31: build.Field: message`.
func (e *Error) Error() string {
	return e.String() + ": " + e.Msg
}

// Error formats every error on a line of its own.
func (es Errors) Error() string {
	lines := make([]string, len(es))
	for i, e := range es {
		lines[i] = e.Error()
	}
	return strings.Join(lines, "\n")
}

// callSite returns the site of the call to the exported builder function
// that called it, named call. Exported functions call it directly, so the
// frame two up is their caller's.
func callSite(call string) Site {
	_, file, line, _ := runtime.Caller(2)
	return Site{File: file, Line: line, Call: call}
}

// errorf builds an Error at s.
func (s Site) errorf(format string, args ...any) *Error {
	return &Error{Site: s, Msg: fmt.Sprintf(format, args...)}
}

// at formats s as `freeze.go:31`, for a message that points at it.
func (s Site) at() string {
	return fmt.Sprintf("%s:%d", filepath.Base(s.File), s.Line)
}
