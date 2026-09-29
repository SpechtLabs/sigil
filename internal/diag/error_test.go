package diag_test

import (
	"errors"
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/token"
)

func at(offset, line, col int) token.Pos {
	return token.Pos{Offset: offset, Line: line, Column: col}
}

func TestErrorString(t *testing.T) {
	tests := []struct {
		err  *diag.Error
		want string
	}{
		{&diag.Error{Msg: "unterminated string literal", Pos: at(40, 12, 21)}, "12:21: unterminated string literal"},
		{&diag.Error{File: "deploy/production.sigil", Msg: "expected `{`", Pos: at(0, 1, 1)}, "deploy/production.sigil:1:1: expected `{`"},
		{&diag.Error{File: "x.sigil", Msg: "no position"}, "x.sigil: no position"},
		{&diag.Error{Msg: "no position or file"}, "no position or file"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("registry unreachable")
	tests := []struct {
		name string
		err  *diag.Error
		want error
	}{
		{"compiler error", &diag.Error{Msg: "unknown name `team`"}, nil},
		{"host function error", &diag.Error{Msg: "host function owner failed: registry unreachable", Cause: cause}, cause},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Unwrap(); got != tt.want { //nolint:errorlint // the cause itself, not a match
				t.Errorf("Unwrap() = %v, want %v", got, tt.want)
			}
			if tt.want != nil && !errors.Is(tt.err, tt.want) {
				t.Errorf("errors.Is(%v, cause) = false", tt.err)
			}
		})
	}
}

func TestSeverityString(t *testing.T) {
	if got := diag.SeverityError.String(); got != "error" {
		t.Errorf("SeverityError = %q", got)
	}
	if got := diag.SeverityWarning.String(); got != "warning" {
		t.Errorf("SeverityWarning = %q", got)
	}
}
