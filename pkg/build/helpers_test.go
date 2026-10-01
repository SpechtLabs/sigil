package build_test

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/pkg/build"
)

// moduleCase is a module of the Access kind and what it renders: the
// statements after the header, or an error at a line of the test.
type moduleCase struct {
	build func(m *build.ModuleDoc[Request], in *Request) int // returns the line an error is expected at, or 0
	name  string
	want  string // the statements, when the module renders
	err   string // part of the error message, when it doesn't
}

// policyCase is moduleCase for a policy.
type policyCase struct {
	build func(p *build.PolicyDoc[Request], in *Request) int
	name  string
	want  string
	err   string
}

// runModules renders each case's module.
func runModules(t *testing.T, tests []moduleCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var line int
			m := build.Module("test.m", Access, func(m *build.ModuleDoc[Request], in *Request) { line = tt.build(m, in) })
			src, err := m.Source()
			expect(t, src, err, line, tt.want, tt.err)
		})
	}
}

// runPolicies renders each case's policy.
func runPolicies(t *testing.T, tests []policyCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var line int
			p := build.Policy("test.p", Access, func(p *build.PolicyDoc[Request], in *Request) { line = tt.build(p, in) })
			src, err := p.Source()
			expect(t, src, err, line, tt.want, tt.err)
		})
	}
}

// expect checks a rendered document against a case: the statements after
// its header and imports-free header, or an error at line of this
// package's tests whose message contains msg.
func expect(t *testing.T, src []byte, err error, line int, want, msg string) {
	t.Helper()
	if msg != "" {
		if err == nil {
			t.Fatalf("rendered without error, want %q:\n%s", msg, src)
		}
		wantError(t, err, line, msg)
		return
	}
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := statements(src); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// wantError checks that err is build.Errors with an error at line whose
// message contains msg.
func wantError(t *testing.T, err error, line int, msg string) {
	t.Helper()
	var errs build.Errors
	if !errors.As(err, &errs) {
		t.Fatalf("error %T isn't build.Errors: %v", err, err)
	}
	for _, e := range errs {
		if strings.Contains(e.Msg, msg) {
			if line != 0 && (e.Line != line || !strings.HasSuffix(filepath.Base(e.File), "_test.go")) {
				t.Errorf("error %q is at %s:%d, want line %d", e.Msg, filepath.Base(e.File), e.Line, line)
			}
			return
		}
	}
	t.Errorf("no error contains %q:\n%v", msg, err)
}

// statements returns a rendered document without its header comment and
// header line.
func statements(src []byte) string {
	_, rest, _ := strings.Cut(string(src), ": Access@2\n")
	return strings.TrimSpace(rest)
}

// line returns the line it's called from, which is the line of a builder
// call made in its arguments.
func line(...any) int {
	var pcs [1]uintptr
	runtime.Callers(2, pcs[:])
	frame, _ := runtime.CallersFrames(pcs[:]).Next()
	return frame.Line
}
