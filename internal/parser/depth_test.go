package parser_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/parser"
)

// deepExprs builds, for each way an expression nests, one whose deepest
// part is at level n: the expression itself is level 1, and each
// parenthesis, operand, element, argument or index one more. at is the
// offset of the part at level k.
var deepExprs = []struct {
	name  string
	build func(n int) string
	at    func(k int) int
}{
	{name: "parentheses", at: func(k int) int { return k - 1 }, build: func(n int) string { return strings.Repeat("(", n-1) + "a" + strings.Repeat(")", n-1) }},
	{name: "not", at: func(k int) int { return 4 * (k - 1) }, build: func(n int) string { return strings.Repeat("not ", n-1) + "a" }},
	{name: "right-associative ??", at: func(k int) int { return 5 * (k - 1) }, build: func(n int) string { return strings.Repeat("a ?? ", n-1) + "a" }},
	{name: "lists", at: func(k int) int { return k - 1 }, build: func(n int) string { return strings.Repeat("[", n-1) + "a" + strings.Repeat("]", n-1) }},
	// A map's key is a level deeper than the map, like its value, and
	// comes first.
	{name: "maps", at: func(k int) int { return 6*(k-2) + 1 }, build: func(n int) string { return strings.Repeat(`{"k": `, n-1) + "1" + strings.Repeat("}", n-1) }},
	{name: "calls", at: func(k int) int { return 2 * (k - 1) }, build: func(n int) string { return strings.Repeat("f(", n-1) + "a" + strings.Repeat(")", n-1) }},
	{name: "indexes", at: func(k int) int { return 2 * (k - 1) }, build: func(n int) string { return strings.Repeat("a[", n-1) + "0" + strings.Repeat("]", n-1) }},
}

// TestNestingLimit checks every way an expression nests at, just below
// and just above MaxNesting, and far above it: up to the limit it parses,
// and past it the parse fails with one diagnostic, at the level that goes
// too deep instead of recursing, however deep the source goes.
func TestNestingLimit(t *testing.T) {
	for _, form := range deepExprs {
		for _, n := range []int{parser.MaxNesting - 1, parser.MaxNesting, parser.MaxNesting + 1, 20_000} {
			t.Run(fmt.Sprintf("%s/%d", form.name, n), func(t *testing.T) {
				src := form.build(n)
				x, errs := parser.ParseExpr("deep.sigil", []byte(src))
				if n <= parser.MaxNesting {
					if errs != nil || x == nil {
						t.Fatalf("ParseExpr() = %v, want %d levels to parse", errs, n)
					}
					return
				}
				if len(errs) != 1 {
					t.Fatalf("ParseExpr() = %v, want one diagnostic", errs)
				}
				want := fmt.Sprintf("this expression nests more than %d levels deep", parser.MaxNesting)
				if e := errs[0]; e.Msg != want || !strings.Contains(e.Help, "`let`") || e.Pos.Offset != form.at(parser.MaxNesting+1) {
					t.Errorf("diagnostic = %q (help %q) at offset %d, want %q at %d, where level %d starts", e.Msg, e.Help, e.Pos.Offset, want, form.at(parser.MaxNesting+1), parser.MaxNesting+1)
				}
			})
		}
	}
}

// TestNestingLimitInFiles checks the limit on `when` blocks and on
// types, which count toward it with the expressions inside them, and
// that a document that goes too deep doesn't stop the next one from
// parsing.
func TestNestingLimitInFiles(t *testing.T) {
	const next = "\n---\npolicy a.next: K@1\n"
	whens := func(n int) string {
		return "policy a.b: K@1\n\n" + strings.Repeat("when x {\n", n) + "allow(reason: y)\n" + strings.Repeat("}\n", n)
	}
	// condition is a policy with one rule, whose condition is cond, and a
	// second rule after it.
	condition := func(cond string) string {
		return "policy a.b: K@1\n\nwhen " + cond + " {\n  allow(reason: y)\n}\n\nwhen x {\n  allow(reason: z)\n}\n"
	}
	lists := func(n int) string {
		return "kind K version 1\n\ninput x: " + strings.Repeat("list<", n-1) + "int" + strings.Repeat(" >", n-1) + "\n"
	}
	tests := []struct {
		name string
		src  string
		want string // the diagnostic; empty when it parses
	}{
		// The deepest `when` holds a condition, one level further in.
		{name: "whens below the limit", src: whens(parser.MaxNesting - 1)},
		{name: "whens past the limit", src: whens(parser.MaxNesting), want: "this expression nests more than 256 levels deep"},
		{name: "whens far past the limit", src: whens(20_000), want: "this expression nests more than 256 levels deep"},
		{name: "a condition of maps far past the limit", src: condition(strings.Repeat(`{"k": `, 20_000) + "1" + strings.Repeat("}", 20_000) + ` == x`), want: "this expression nests more than 256 levels deep"},
		{name: "a condition of lists far past the limit", src: condition(strings.Repeat("[", 20_000) + "a" + strings.Repeat("]", 20_000) + " == []"), want: "this expression nests more than 256 levels deep"},
		{name: "types at the limit", src: lists(parser.MaxNesting)},
		{name: "types past the limit", src: lists(parser.MaxNesting + 1), want: "this type nests more than 256 levels deep"},
		{name: "types far past the limit", src: lists(20_000), want: "this type nests more than 256 levels deep"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, errs := parser.ParseFile("deep.sigil", []byte(tt.src+next))
			if tt.want == "" {
				if errs != nil {
					t.Fatalf("ParseFile() = %v, want no errors", errs)
				}
				return
			}
			if len(errs) != 1 || errs[0].Msg != tt.want {
				t.Fatalf("ParseFile() = %v, want %q", errs, tt.want)
			}
			if last, ok := f.Docs[len(f.Docs)-1].(*ast.PolicyDoc); !ok || last.Name.String() != "a.next" {
				t.Errorf("the document after the deep one wasn't parsed: %v", f.Docs)
			}
		})
	}
}
