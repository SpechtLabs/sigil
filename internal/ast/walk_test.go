package ast_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/parser"
)

// TestInspect checks that every kind of node is walked, in depth-first
// order, and that returning false prunes a subtree.
func TestInspect(t *testing.T) {
	tests := []struct {
		src   string
		prune string // node type name at which to stop descending, if any
		want  string // node type names in visit order
	}{
		{src: "a", want: "Ident"},
		{src: "not (a or -b)", want: "UnaryExpr ParenExpr BinaryExpr Ident UnaryExpr Ident"},
		{src: `[1, "x"]`, want: "ListLit IntLit StringLit"},
		{src: `{"k": 1.5, "d": 1h}`, want: "MapLit StringLit FloatLit StringLit DurationLit"},
		{src: "a.b[0]", want: "IndexExpr SelectorExpr Ident Ident IntLit"},
		{src: "f(a, true)", want: "CallExpr Ident Ident BoolLit"},
		{src: "any r in xs: r == outcome", want: "QuantExpr Ident Ident BinaryExpr Ident Outcome"},
		{src: "filter r in xs: r != a", want: "FilterExpr Ident Ident BinaryExpr Ident Ident"},
		{src: "filter r in xs: r != a", prune: "FilterExpr", want: "FilterExpr"},
		{src: "f(a) and [b, c]", prune: "CallExpr", want: "BinaryExpr CallExpr ListLit Ident Ident"},
		{src: "a and b", prune: "BinaryExpr", want: "BinaryExpr"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			x, errs := parser.ParseExpr("x.sigil", []byte(tt.src))
			if errs != nil {
				t.Fatal(errs)
			}
			var got []string
			ast.Inspect(x, func(n ast.Expr) bool {
				name := strings.TrimPrefix(fmt.Sprintf("%T", n), "*ast.")
				got = append(got, name)
				return name != tt.prune
			})
			if g := strings.Join(got, " "); g != tt.want {
				t.Errorf("visited %q, want %q", g, tt.want)
			}
		})
	}
	ast.Inspect(nil, func(ast.Expr) bool { t.Error("fn called for a nil node"); return true })
}
