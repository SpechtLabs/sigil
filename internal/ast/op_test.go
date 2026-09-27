package ast_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
)

func TestOpString(t *testing.T) {
	tests := []struct {
		op   ast.Op
		want string
	}{
		{ast.OpOr, "or"},
		{ast.OpXor, "xor"},
		{ast.OpAnd, "and"},
		{ast.OpNot, "not"},
		{ast.OpEq, "=="},
		{ast.OpNotEq, "!="},
		{ast.OpLt, "<"},
		{ast.OpLtEq, "<="},
		{ast.OpGt, ">"},
		{ast.OpGtEq, ">="},
		{ast.OpIn, "in"},
		{ast.OpNotIn, "not in"},
		{ast.OpAllIn, "all in"},
		{ast.OpAnyIn, "any in"},
		{ast.OpOneIn, "one in"},
		{ast.OpExclusiveIn, "exclusive in"},
		{ast.OpHas, "has"},
		{ast.OpLike, "like"},
		{ast.OpMatches, "matches"},
		{ast.OpCoalesce, "??"},
		{ast.OpAdd, "+"},
		{ast.OpSub, "-"},
		{ast.OpNeg, "-"},
		{ast.OpAny, "any"},
		{ast.OpAll, "all"},
		{ast.OpInvalid, "op(0)"},
		{ast.Op(99), "op(99)"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.op.String(); got != tt.want {
				t.Errorf("Op(%d).String() = %q, want %q", tt.op, got, tt.want)
			}
		})
	}
}

func TestOpIsComparison(t *testing.T) {
	tests := []struct {
		op   ast.Op
		want bool
	}{
		{ast.OpOr, false},
		{ast.OpAnd, false},
		{ast.OpNot, false},
		{ast.OpEq, true},
		{ast.OpGtEq, true},
		{ast.OpIn, true},
		{ast.OpExclusiveIn, true},
		{ast.OpHas, true},
		{ast.OpMatches, true},
		{ast.OpCoalesce, false},
		{ast.OpAdd, false},
		{ast.OpNeg, false},
		{ast.OpAny, false},
	}

	for _, tt := range tests {
		t.Run(tt.op.String(), func(t *testing.T) {
			if got := tt.op.IsComparison(); got != tt.want {
				t.Errorf("%v.IsComparison() = %v, want %v", tt.op, got, tt.want)
			}
		})
	}
}
