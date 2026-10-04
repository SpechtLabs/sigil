package parser_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	// Nesting past MaxNesting, which fails with one diagnostic instead
	// of recursing.
	f.Add([]byte("policy a.b: K@1\n\nwhen " + strings.Repeat("(", 20_000) + "x" + strings.Repeat(")", 20_000) + " {\n  allow(reason: y)\n}\n"))
	f.Add([]byte("policy a.b: K@1\n\n" + strings.Repeat("when x {\n", parser.MaxNesting+1) + strings.Repeat("}\n", parser.MaxNesting+1)))
	f.Fuzz(func(t *testing.T, src []byte) {
		a, errs := parser.ParseFile("fuzz.sigil", src)
		b, again := parser.ParseFile("fuzz.sigil", src)
		if !reflect.DeepEqual(a, b) || !reflect.DeepEqual(errs, again) { //nolint:govet // deepequalerrors: diagnostics compare field by field, and a parse error has no Cause
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
	for _, src := range []string{"", "1 + 2 - 3", "not false and true", "all x in [1, 2]: x > 0", "filter x in [1, 2]: x > 0", "a?.b[0] ?? 1", `{"a": [1]}["a"]`, `"abc" like "a*"`, "a one in b", "a | b", "a and: b", "(((", "\xff"} {
		f.Add(src)
	}
	f.Add(strings.Repeat("[", parser.MaxNesting) + strings.Repeat("]", parser.MaxNesting))
	f.Add(strings.Repeat("not ", 20_000) + "a")
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
			// Sprint wraps every operator in parentheses, which nest a level
			// of their own, so a tree more than half MaxNesting deep may print
			// past the limit, though nothing else may stop it parsing.
			if 2*exprDepth(x) > parser.MaxNesting && len(errs) == 1 && strings.Contains(errs[0].Msg, "levels deep") {
				return
			}
			t.Fatalf("printed expression %q does not parse: %v", printed, errs)
		}
		if !reflect.DeepEqual(exprShape(x), exprShape(y)) {
			t.Fatalf("printing changed expression structure: %q -> %q", src, printed)
		}
	})
}

// exprDepth returns how many levels the tree under x nests, x included.
func exprDepth(x ast.Expr) int {
	deepest := 0
	ast.Inspect(x, func(n ast.Expr) bool {
		if n == x {
			return true
		}
		deepest = max(deepest, exprDepth(n))
		return false // exprDepth(n) went below n
	})
	return 1 + deepest
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
		case *ast.IndexExpr, *ast.FilterExpr:
		default:
			value = ast.Sprint(n)
		}
		shape = append(shape, fmt.Sprintf("%T:%s", n, value))
		return true
	})
	return shape
}
