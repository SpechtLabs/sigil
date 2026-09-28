package parser_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/parser"
)

func FuzzParseFile(f *testing.F) {
	files, err := filepath.Glob("testdata/*.sigil")
	if err != nil {
		f.Fatal(err)
	}
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(src)
	}
	f.Add([]byte{})
	f.Add([]byte("\xff\x00"))
	f.Fuzz(func(t *testing.T, src []byte) {
		a, errs := parser.ParseFile("fuzz.sigil", src)
		b, again := parser.ParseFile("fuzz.sigil", src)
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(errs, again) {
			t.Fatal("parsing is not deterministic")
		}
		// Partial trees and diagnostics are consumed by editors after errors.
		_ = ast.Dump(a)
		for _, e := range errs {
			if e.Pos.Offset < 0 || e.Pos.Offset > len(src) || e.End.Offset < e.Pos.Offset || e.End.Offset > len(src) {
				t.Fatalf("diagnostic outside source: %+v", e)
			}
			_ = diag.Render(e, src)
		}
	})
}

func FuzzParseExpr(f *testing.F) {
	for _, src := range []string{"", "1 + 2 - 3", "not false and true", "all x in [1, 2]: x > 0", "a?.b[0] ?? 1", `{"a": [1]}["a"]`, `"abc" like "a*"`, "a one in b", "(((", "\xff"} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		x, errs := parser.ParseExpr("fuzz.sigil", []byte(src))
		if errs != nil {
			if x != nil {
				t.Fatal("failed expression returned a tree")
			}
			return
		}
		printed := ast.Sprint(x)
		y, errs := parser.ParseExpr("printed.sigil", []byte(printed))
		if errs != nil {
			t.Fatalf("printed expression %q does not parse: %v", printed, errs)
		}
		if !reflect.DeepEqual(exprShape(x), exprShape(y)) {
			t.Fatalf("printing changed expression structure: %q -> %q", src, printed)
		}
	})
}

// Preorder node kinds, arities, operators and leaf values describe the tree
// without source positions or the parentheses Sprint adds around operators.
func exprShape(x ast.Expr) []string {
	var shape []string
	ast.Inspect(x, func(n ast.Expr) bool {
		value := ""
		switch n := n.(type) {
		case *ast.ParenExpr:
			return true
		case *ast.UnaryExpr:
			value = n.Op.String()
		case *ast.BinaryExpr:
			value = n.Op.String()
		case *ast.QuantExpr:
			value = n.Op.String()
		case *ast.ListLit:
			value = fmt.Sprint(len(n.Elems))
		case *ast.MapLit:
			value = fmt.Sprint(len(n.Entries))
		case *ast.CallExpr:
			value = fmt.Sprint(len(n.Args))
		case *ast.SelectorExpr:
			value = fmt.Sprint(n.Optional)
		case *ast.IndexExpr:
		default:
			value = ast.Sprint(n)
		}
		shape = append(shape, fmt.Sprintf("%T:%s", n, value))
		return true
	})
	return shape
}
