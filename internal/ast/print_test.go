package ast_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/token"
)

// unknown is a Node the printer doesn't know, to check it degrades
// instead of panicking.
type unknown struct{ ast.Span }

func TestSprintUnknownNode(t *testing.T) {
	if got, want := ast.Sprint(unknown{}), "<ast_test.unknown>"; got != want {
		t.Errorf("Sprint() = %q, want %q", got, want)
	}
}

func TestSpan(t *testing.T) {
	from := token.Pos{Offset: 3, Line: 1, Column: 4}
	to := token.Pos{Offset: 8, Line: 1, Column: 9}
	s := ast.Span{From: from, To: to}
	if s.Pos() != from {
		t.Errorf("Pos() = %v, want %v", s.Pos(), from)
	}
	if s.End() != to {
		t.Errorf("End() = %v, want %v", s.End(), to)
	}
}
