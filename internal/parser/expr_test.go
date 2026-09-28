package parser

import (
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
)

// TestParseExpr checks the shape of the tree: every operator application is
// printed in parentheses, so precedence and associativity are visible.
func TestParseExpr(t *testing.T) {
	tests := []struct {
		src  string
		want string
	}{
		// Operands.
		{"a", "a"},
		{"42", "42"},
		{"0.5", "0.5"},
		{"1h30m", "1h30m"},
		{`"x"`, `"x"`},
		{"`^re$`", "`^re$`"},
		{"true", "true"},
		{"false", "false"},
		{"outcome", "outcome"},
		{"(a)", "(a)"},
		{"((a))", "((a))"},

		// Boolean operators: `and` binds tighter than `or`, both left.
		{"a or b and c", "(a or (b and c))"},
		{"a and b or c", "((a and b) or c)"},
		{"a or b or c", "((a or b) or c)"},
		{"a and b and c", "((a and b) and c)"},
		{"(a or b) and c", "(((a or b)) and c)"},
		{"a xor b", "(a xor b)"},
		{"(a xor b) xor c", "(((a xor b)) xor c)"},
		{"a and b xor c and d", "((a and b) xor (c and d))"},

		// `not` binds looser than comparisons and tighter than `and`.
		{"not a", "(not a)"},
		{"not a == b", "(not (a == b))"},
		{"not a and b", "((not a) and b)"},
		{"a and not b", "(a and (not b))"},
		{"a or not b == c", "(a or (not (b == c)))"},
		{"not not a", "(not (not a))"},
		{`not "admin" in actor.roles`, `(not ("admin" in actor.roles))`},
		{"a == (not b)", "(a == ((not b)))"},

		// Comparisons and membership.
		{"a == b and c == d", "((a == b) and (c == d))"},
		{"a != b", "(a != b)"},
		{"a < b", "(a < b)"},
		{"a <= b", "(a <= b)"},
		{"a > b", "(a > b)"},
		{"a >= b", "(a >= b)"},
		{`"deployer" in actor.roles`, `("deployer" in actor.roles)`},
		{`"admin" not in actor.roles`, `("admin" not in actor.roles)`},
		{"actor.teams any in service.owners", "(actor.teams any in service.owners)"},
		{"xs all in ys", "(xs all in ys)"},
		{"xs one in ys", "(xs one in ys)"},
		{"xs exclusive in ys", "(xs exclusive in ys)"},
		{`service.labels has {"team": "payments"}`, `(service.labels has {"team": "payments"})`},
		{`service.labels has "env"`, `(service.labels has "env")`},
		{`service.name like "payments-*"`, `(service.name like "payments-*")`},
		{"service.labels[\"team\"] matches `^team-[a-z]+$`", "(service.labels[\"team\"] matches `^team-[a-z]+$`)"},
		{"[customer_data_writer, development_environment_writer] exclusive in outcome",
			"([customer_data_writer, development_environment_writer] exclusive in outcome)"},
		{`customer_data_writer not in outcome or actor.clearance == "pii"`,
			`((customer_data_writer not in outcome) or (actor.clearance == "pii"))`},

		// `??` sits between comparisons and arithmetic, right-associative.
		{`owner ?? "unknown" == "team-a"`, `((owner ?? "unknown") == "team-a")`},
		{"a ?? b ?? c", "(a ?? (b ?? c))"},
		{"a ?? b + c", "(a ?? (b + c))"},
		{"a + b ?? c", "((a + b) ?? c)"},

		// Arithmetic, left-associative, with unary minus tightest.
		{"a + b - c", "((a + b) - c)"},
		{"-a + b", "((-a) + b)"},
		{"-a.b", "(-a.b)"},
		{"- -a", "(-(-a))"},
		{"a - -b", "(a - (-b))"},
		{"a - --b", "(a - (-(-b)))"},
		{"-3", "(-3)"},
		{"-1h", "(-1h)"},
		{"release.soak + 2h >= min_soak", "((release.soak + 2h) >= min_soak)"},
		{"now - release.built_at > 2h", "((now - release.built_at) > 2h)"},

		// Postfix forms.
		{"a.b.c", "a.b.c"},
		{"a?.b.c", "a?.b.c"},
		{"present release?.parent and x", "((present release?.parent) and x)"},
		{"not present release.ticket", "(not (present release.ticket))"},
		{"present a.b[0] == c", "((present a.b[0]) == c)"},
		{"a?.b?.c[0]", "a?.b?.c[0]"},
		{"release?.soak ?? 5m < 1h", "((release?.soak ?? 5m) < 1h)"},
		{"release?.type", "release?.type"},
		{"service.type", "service.type"},
		{"resource.kind == \"x\"", "(resource.kind == \"x\")"},
		{"a[0][1]", "a[0][1]"},
		{"f(a, b)", "f(a, b)"},
		{"f()", "f()"},
		{"f(a,)", "f(a)"},
		{"a.b(c)[d].e", "a.b(c)[d].e"},
		{`split(service.labels["regions"], ",") all in actor.regions`, `(split(service.labels["regions"], ",") all in actor.regions)`},
		{"f(a or b, not c)", "f((a or b), (not c))"},

		// List and map literals, with trailing commas.
		{"[1, 2, 3]", "[1, 2, 3]"},
		{"[]", "[]"},
		{"[1, 2,]", "[1, 2]"},
		{"[[1], [2]]", "[[1], [2]]"},
		{"{}", "{}"},
		{`{"a": 1, "b": 2,}`, `{"a": 1, "b": 2}`},
		{"{a ?? b: c or d}", "{(a ?? b): (c or d)}"},
		{`{
  "app.kubernetes.io/managed-by": "argocd",
  "platform.example.com/lifecycle": "ga",
}`, `{"app.kubernetes.io/managed-by": "argocd", "platform.example.com/lifecycle": "ga"}`},

		// Quantifiers: the body extends as far right as possible.
		{`any r in actor.roles: r like "sre-*"`, `(any r in actor.roles: (r like "sre-*"))`},
		{`all r in actor.roles: r != "admin"`, `(all r in actor.roles: (r != "admin"))`},
		{`any r in actor.roles: r like "sre-*" and eligible`, `(any r in actor.roles: ((r like "sre-*") and eligible))`},
		{`(any r in actor.roles: r like "sre-*") and eligible`, `(((any r in actor.roles: (r like "sre-*"))) and eligible)`},
		{"a and any r in xs: p", "(a and (any r in xs: p))"},
		{"a or all r in xs: p or q", "(a or (all r in xs: (p or q)))"},
		{"not any r in xs: p", "(not (any r in xs: p))"},
		{"all a in xs: any b in ys: a == b", "(all a in xs: (any b in ys: (a == b)))"},
		{"any r in xs ?? []: r", "(any r in (xs ?? []): r)"},
		{"any r in a + b: r", "(any r in (a + b): r)"},
		{"x == (all r in xs: p)", "(x == ((all r in xs: p)))"},
		{"f(any r in xs: p, q)", "f((any r in xs: p), q)"},
		{"[any r in xs: p, q]", "[(any r in xs: p), q]"},

		// Comments and newlines are invisible.
		{"a // c\n and b", "(a and b)"},
		{"a\n  and b\n  or c", "((a and b) or c)"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			x, errs := ParseExpr("test.sigil", []byte(tt.src))
			if errs != nil {
				t.Fatalf("ParseExpr() error:\n%v", errs)
			}
			if got := ast.Sprint(x); got != tt.want {
				t.Errorf("ParseExpr() =\n  %s\nwant\n  %s", got, tt.want)
			}
		})
	}
}

// TestParseExprErrors pins every message the expression parser can produce:
// what it says, the hint it gives and what it underlines.
func TestParseExprErrors(t *testing.T) {
	const closeParen = "to close the `(` at 1:1"
	const quantAny = "a quantifier is written `any x in xs: condition`"
	const chain = "add parentheses to say which comparison happens first, or join two comparisons with `and`"
	const wrap = "wrap it in parentheses"

	tests := []struct {
		src  string
		msg  string
		help string
		span string
	}{
		// Non-associative comparisons.
		{"a < b < c", "`<` can't follow `<`: comparisons don't chain", chain, "1:7-1:8"},
		{"a == b == c", "`==` can't follow `==`: comparisons don't chain", chain, "1:8-1:10"},
		{"a in b == c", "`==` can't follow `in`: comparisons don't chain", chain, "1:8-1:10"},
		{"x in xs == true", "`==` can't follow `in`: comparisons don't chain", chain, "1:9-1:11"},
		{"a like \"x\" not in b", "`not in` can't follow `like`: comparisons don't chain", chain, "1:12-1:15"},

		// `xor` doesn't chain or mix with `or`.
		{"a xor b xor c", "`xor` can't be chained", "add parentheses to say which pair is compared first, or use `one in` for exactly one of several", "1:9-1:12"},
		{"a or b xor c", "`or` and `xor` can't be mixed without parentheses", "add parentheses to say which grouping you mean", "1:8-1:11"},
		{"a xor b or c", "`or` and `xor` can't be mixed without parentheses", "add parentheses to say which grouping you mean", "1:9-1:11"},
		{"a or b or c xor d", "`or` and `xor` can't be mixed without parentheses", "add parentheses to say which grouping you mean", "1:13-1:16"},

		// Loose prefix forms in tight positions.
		{"x == all r in xs: p", "a quantifier can't be an operand of `==`", wrap, "1:6-1:9"},
		{"x == any r in xs: p", "a quantifier can't be an operand of `==`", wrap, "1:6-1:9"},
		{"x in any r in xs: p", "a quantifier can't be an operand of `in`", wrap, "1:6-1:9"},
		{"a == not b", "`not` can't be an operand of `==`", wrap, "1:6-1:9"},
		{"a - not b", "`not` can't be an operand of `-`", wrap, "1:5-1:8"},
		{"a ?? not b", "`not` can't be an operand of `??`", wrap, "1:6-1:9"},
		{"-not a", "`not` can't be an operand of `-`", wrap, "1:2-1:5"},
		{"any r in not x: p", "`not` can't be an operand of `in`", wrap, "1:10-1:13"},
		{"{not a: 1}", "`not` can't appear here", wrap, "1:2-1:5"},

		// Quantifier shape.
		{"one r in xs: p", "`one` is an operator, not a quantifier", "write `a one in b`; only `any` and `all` start a quantifier", "1:1-1:4"},
		{"exclusive in xs", "`exclusive` is an operator, not a quantifier", "write `a exclusive in b`; only `any` and `all` start a quantifier", "1:1-1:10"},
		{"any in xs: p", "expected a variable name after `any`, found `in`", quantAny, "1:5-1:7"},
		{"all type in xs: p", "expected a variable name after `all`, found `type`", "a quantifier is written `all x in xs: condition`", "1:5-1:9"},
		{"any r xs: p", "expected `in` after the variable `r`, found `xs`", quantAny, "1:7-1:9"},
		{"any r in xs p", "expected `:`, found `p`", quantAny, "1:13-1:14"},
		{"any r in xs:", "expected an expression, found end of file", "", "1:13-1:13"},

		// Compound operators after an operand.
		{"a not b", "expected `in` after `not`", "after an operand, `not` is only valid as part of `not in`", "1:3-1:6"},
		{"a all b", "expected `in` after `all`", "after an operand, `all` is only valid as part of `all in`", "1:3-1:6"},
		{"a any", "expected `in` after `any`", "after an operand, `any` is only valid as part of `any in`", "1:3-1:6"},
		{"a one in", "expected an expression, found end of file", "", "1:9-1:9"},

		// Unclosed and malformed delimiters.
		{"(a", "expected `)`, found end of file", closeParen, "1:3-1:3"},
		{"(a b", "expected `)`, found `b`", closeParen, "1:4-1:5"},
		{"[1, 2", "expected `]`, found end of file", "to close the `[` at 1:1", "1:6-1:6"},
		{"[1 2]", "expected `]`, found `2`", "to close the `[` at 1:1", "1:4-1:5"},
		{"f(a b)", "expected `)`, found `b`", "to close the `(` at 1:2", "1:5-1:6"},
		{"f(a,,)", "expected an expression, found `,`", "", "1:5-1:6"},
		{`{"a" 1}`, "expected `:`, found `1`", "a map entry is written `key: value`", "1:6-1:7"},
		{`{"a": 1`, "expected `}`, found end of file", "to close the `{` at 1:1", "1:8-1:8"},
		{`{"a": }`, "expected an expression, found `}`", "", "1:7-1:8"},
		{"a[", "expected an expression, found end of file", "", "1:3-1:3"},
		{"a[1", "expected `]`, found end of file", "to close the `[` at 1:2", "1:4-1:4"},
		{"a.", "expected a field name after `.`, found end of file", "", "1:3-1:3"},
		{"a.1", "expected a field name after `.`, found `1`", "", "1:3-1:4"},

		// Missing or extra operands.
		{"", "expected an expression, found end of file", "", "1:1-1:1"},
		{")", "expected an expression, found `)`", "", "1:1-1:2"},
		{"let", "expected an expression, found `let`", "", "1:1-1:4"},
		{".5", "expected an expression, found `.`", "", "1:1-1:2"},
		{"a ==", "expected an expression, found end of file", "", "1:5-1:5"},
		{"a and", "expected an expression, found end of file", "", "1:6-1:6"},
		{"a b", "expected end of the expression, found `b`", "", "1:3-1:4"},
		{"a let", "expected end of the expression, found `let`", "", "1:3-1:6"},
		{"a {", "expected end of the expression, found `{`", "", "1:3-1:4"},

		// The document separator.
		{"min_soak---1h", "`---` separates documents and can't appear inside an expression", "if you meant arithmetic, put spaces between the minus signs", "1:9-1:12"},
		{"--- a", "`---` separates documents and can't appear inside an expression", "if you meant arithmetic, put spaces between the minus signs", "1:1-1:4"},
		{"a - ---b", "`---` separates documents and can't appear inside an expression", "if you meant arithmetic, put spaces between the minus signs", "1:5-1:8"},

		// Lexical errors are reported once, by the lexer; the parser stays quiet.
		{"5.", "a float literal needs digits on both sides of the point", "write `5.0`", "1:1-1:3"},
		{"a $ b", "unexpected character `$`", "", "1:3-1:4"},
		{"a < b $", "unexpected character `$`", "", "1:7-1:8"},
		{`"abc`, "unterminated string literal", "close it with `\"`", "1:1-1:5"},
		{"x && y", "unexpected character `&&`", "use `and`", "1:3-1:5"},
		{"[1h1h]", "unit `h` appears twice in `1h1h`", "each unit may appear once in a duration; add the components together", "1:2-1:6"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			x, errs := ParseExpr("test.sigil", []byte(tt.src))
			if errs == nil {
				t.Fatalf("ParseExpr() = %s, want an error", ast.Sprint(x))
			}
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1:\n%v", len(errs), errs)
			}
			e := errs[0]
			if e.Msg != tt.msg {
				t.Errorf("Msg  = %q\nwant   %q", e.Msg, tt.msg)
			}
			if e.Help != tt.help {
				t.Errorf("Help = %q\nwant   %q", e.Help, tt.help)
			}
			if got := e.Pos.String() + "-" + e.End.String(); got != tt.span {
				t.Errorf("span = %s, want %s", got, tt.span)
			}
			if e.File != "test.sigil" {
				t.Errorf("File = %q, want %q", e.File, "test.sigil")
			}
		})
	}
}

// TestParseExprAllErrors checks that a parse error doesn't hide lexical
// errors later in the source, and that the list comes out in source order.
func TestParseExprAllErrors(t *testing.T) {
	_, errs := ParseExpr("test.sigil", []byte("a == b == c $ 1h1h"))
	want := strings.Join([]string{
		"test.sigil:1:8: `==` can't follow `==`: comparisons don't chain",
		"test.sigil:1:13: unexpected character `$`",
		"test.sigil:1:15: unit `h` appears twice in `1h1h`",
	}, "\n")
	if errs.Error() != want {
		t.Errorf("ParseExpr() error =\n%v\nwant\n%s", errs, want)
	}
}

func TestParseExprLiteralValues(t *testing.T) {
	parse := func(t *testing.T, src string) ast.Expr {
		t.Helper()
		x, errs := ParseExpr("test.sigil", []byte(src))
		if errs != nil {
			t.Fatalf("ParseExpr() error: %v", errs)
		}
		return x
	}

	t.Run("int", func(t *testing.T) {
		tests := []struct {
			src  string
			want int64
		}{
			{"0", 0}, {"42", 42}, {"007", 7}, {"9223372036854775807", 9223372036854775807},
		}
		for _, tt := range tests {
			lit, ok := parse(t, tt.src).(*ast.IntLit)
			if !ok || lit.Value != tt.want || lit.Text != tt.src {
				t.Errorf("%s = %+v, want IntLit{Text: %q, Value: %d}", tt.src, lit, tt.src, tt.want)
			}
		}
	})

	t.Run("float", func(t *testing.T) {
		lit, ok := parse(t, "0.5").(*ast.FloatLit)
		if !ok || lit.Value != 0.5 || lit.Text != "0.5" {
			t.Errorf("got %+v, want FloatLit{Text: \"0.5\", Value: 0.5}", lit)
		}
	})

	t.Run("duration", func(t *testing.T) {
		lit, ok := parse(t, "1h30m").(*ast.DurationLit)
		if !ok || lit.Value != 90*time.Minute || lit.Text != "1h30m" {
			t.Errorf("got %+v, want DurationLit{Text: \"1h30m\", Value: 1h30m}", lit)
		}
	})

	t.Run("string", func(t *testing.T) {
		tests := []struct {
			src  string
			want string
			raw  bool
		}{
			{`"deployer"`, "deployer", false},
			{`"a\nb"`, "a\nb", false},
			{`"say \"hi\""`, `say "hi"`, false},
			{"`a\\nb`", `a\nb`, true},
			{"`^team-[a-z]+$`", `^team-[a-z]+$`, true},
			{"`multi\nline`", "multi\nline", true},
		}
		for _, tt := range tests {
			lit, ok := parse(t, tt.src).(*ast.StringLit)
			if !ok || lit.Value != tt.want || lit.Raw != tt.raw || lit.Text != tt.src {
				t.Errorf("%s = %+v, want StringLit{Text: %q, Value: %q, Raw: %v}", tt.src, lit, tt.src, tt.want, tt.raw)
			}
		}
	})

	t.Run("bool", func(t *testing.T) {
		for _, tt := range []struct {
			src  string
			want bool
		}{{"true", true}, {"false", false}} {
			lit, ok := parse(t, tt.src).(*ast.BoolLit)
			if !ok || lit.Value != tt.want {
				t.Errorf("%s = %+v, want BoolLit{Value: %v}", tt.src, lit, tt.want)
			}
		}
	})

	t.Run("negative int is unary minus", func(t *testing.T) {
		u, ok := parse(t, "-3").(*ast.UnaryExpr)
		if !ok || u.Op != ast.OpNeg {
			t.Fatalf("got %+v, want UnaryExpr{Op: -}", u)
		}
		if lit, ok := u.X.(*ast.IntLit); !ok || lit.Value != 3 {
			t.Errorf("operand = %+v, want IntLit{Value: 3}", u.X)
		}
	})
}

func TestParseExprPositions(t *testing.T) {
	tests := []struct {
		src  string
		span string // of the whole expression
	}{
		{"a", "1:1-1:2"},
		{"not a", "1:1-1:6"},
		{"-a", "1:1-1:3"},
		{"a and b", "1:1-1:8"},
		{"a not in b", "1:1-1:11"},
		{"a.b", "1:1-1:4"},
		{"a[0]", "1:1-1:5"},
		{"f(a)", "1:1-1:5"},
		{"f( a , )", "1:1-1:9"},
		{"(a)", "1:1-1:4"},
		{"[1, 2]", "1:1-1:7"},
		{`{"a": 1}`, "1:1-1:9"},
		{"any r in xs: r", "1:1-1:15"},
		{"a and\n  b", "1:1-2:4"},
		{"`a\nb`", "1:1-2:3"},
		{"a\n  or b\n  and c", "1:1-3:8"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			x, errs := ParseExpr("test.sigil", []byte(tt.src))
			if errs != nil {
				t.Fatalf("ParseExpr() error: %v", errs)
			}
			if got := x.Pos().String() + "-" + x.End().String(); got != tt.span {
				t.Errorf("span = %s, want %s", got, tt.span)
			}
		})
	}

	t.Run("children", func(t *testing.T) {
		x, errs := ParseExpr("test.sigil", []byte("ab not in cd"))
		if errs != nil {
			t.Fatalf("ParseExpr() error: %v", errs)
		}
		bin := x.(*ast.BinaryExpr)
		if got := bin.X.Pos().String() + "-" + bin.X.End().String(); got != "1:1-1:3" {
			t.Errorf("X span = %s, want 1:1-1:3", got)
		}
		if got := bin.OpPos.String(); got != "1:4" {
			t.Errorf("OpPos = %s, want 1:4", got)
		}
		if got := bin.Y.Pos().String() + "-" + bin.Y.End().String(); got != "1:11-1:13" {
			t.Errorf("Y span = %s, want 1:11-1:13", got)
		}
	})
}
