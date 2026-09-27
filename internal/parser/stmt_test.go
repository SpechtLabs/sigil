package parser

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
)

// TestParseFileStatements checks the tree each statement form builds, via
// the dump. Spans are stripped so the tables stay readable; the golden
// tests pin them.
func TestParseFileStatements(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// Files and documents.
		{name: "empty file", src: "", want: ""},
		{name: "comments only", src: "// nothing\n\n// here\n", want: ""},
		{name: "separators only", src: "---\n---\n", want: ""},
		{name: "policy header only", src: "policy a.b: K", want: "policy a.b: K"},
		{name: "module header only", src: "module a: K", want: "module a: K"},
		{name: "kind header only", src: "kind K version 3", want: "kind K version 3"},
		{name: "deep policy name", src: "policy a.b.c.d: K", want: "policy a.b.c.d: K"},
		{name: "two documents without separator", src: "policy a: K\nmodule b: K", want: "policy a: K\n---\nmodule b: K"},
		{name: "separators around documents", src: "---\npolicy a: K\n---\n---\nmodule b: K\n---", want: "policy a: K\n---\nmodule b: K"},
		{name: "comment above header", src: "// a\npolicy a: K\n// b\nmodule b: K", want: "policy a: K\n---\nmodule b: K"},

		// Imports.
		{name: "use whole", src: "policy a: K\nuse deploy.common", want: "policy a: K\n  use deploy.common"},
		{name: "use alias", src: "policy a: K\nuse deploy.production as approvals", want: "policy a: K\n  use deploy.production as approvals"},
		{name: "use selective", src: "policy a: K\nuse deploy.common.{cleared, owns_service}", want: "policy a: K\n  use deploy.common.{cleared, owns_service}"},
		{name: "use selective alias", src: "policy a: K\nuse deploy.common.{owns_service as owner}", want: "policy a: K\n  use deploy.common.{owns_service as owner}"},
		{name: "use selective trailing comma", src: "policy a: K\nuse deploy.common.{a, b,}", want: "policy a: K\n  use deploy.common.{a, b}"},
		{name: "use single segment", src: "policy a: K\nuse common", want: "policy a: K\n  use common"},
		{name: "use space before brace", src: "policy a: K\nuse deploy.common. {a}", want: "policy a: K\n  use deploy.common.{a}"},
		{name: "several uses", src: "module a: K\nuse x\nuse y.{z}\nlet a = z", want: "module a: K\n  use x\n  use y.{z}\n  let a = z"},

		// Params, lets.
		{name: "param required", src: "policy a: K\nparam approvers: list<string>", want: "policy a: K\n  param approvers: list<string>"},
		{name: "param default", src: "policy a: K\nparam tiers: list<string> = [\"standard\"]", want: "policy a: K\n  param tiers: list<string> = [\"standard\"]"},
		{name: "param glued >=", src: "policy a: K\nparam m: map<string, int>= {}", want: "policy a: K\n  param m: map<string, int> = {}"},
		{name: "param nested glued >=", src: "policy a: K\nparam m: list<list<int>>= []", want: "policy a: K\n  param m: list<list<int>> = []"},
		{name: "param optional type parses", src: "policy a: K\nparam t: ?string", want: "policy a: K\n  param t: ?string"},
		{name: "let", src: "policy a: K\nlet x = a and b", want: "policy a: K\n  let x = (a and b)"},
		{name: "let ends at next statement", src: "policy a: K\nlet x = a let y = b", want: "policy a: K\n  let x = a\n  let y = b"},
		{name: "let ends at invocation", src: "policy a: K\nlet x = a f(b: 1)", want: "policy a: K\n  let x = a\n  f(b: 1)"},
		{name: "let ends at separator", src: "policy a: K\nlet x = a\n---\nmodule b: K", want: "policy a: K\n  let x = a\n---\nmodule b: K"},

		// Rules.
		{name: "when empty body", src: "policy a: K\nwhen x {}", want: "policy a: K\n  when x {\n  }"},
		{name: "when map literal condition", src: "policy a: K\nwhen labels has {\"team\": \"payments\"} { deny(\"x\") }",
			want: "policy a: K\n  when (labels has {\"team\": \"payments\"}) {\n    deny(\"x\")\n  }"},
		{name: "when nested", src: "policy a: K\nwhen a { when b { when c { deny(\"x\") } } }",
			want: "policy a: K\n  when a {\n    when b {\n      when c {\n        deny(\"x\")\n      }\n    }\n  }"},
		{name: "when several statements", src: "policy a: K\nwhen a { deny(\"x\") assert b, \"r\" f() when c {} }",
			want: "policy a: K\n  when a {\n    deny(\"x\")\n    assert b, \"r\"\n    f()\n    when c {\n    }\n  }"},
		{name: "assert quantifier ends at comma", src: "policy a: K\nassert all g in grants: g != \"root\", \"no_root\"",
			want: "policy a: K\n  assert (all g in grants: (g != \"root\")), \"no_root\""},
		{name: "assert outcome", src: "policy a: K\nassert [a, b] exclusive in outcome, \"sod\"",
			want: "policy a: K\n  assert ([a, b] exclusive in outcome), \"sod\""},

		// Calls.
		{name: "call no args", src: "policy a: K\nbaseline()", want: "policy a: K\n  baseline()"},
		{name: "call reason only", src: "policy a: K\nwhen x { deny(\"r\") }", want: "policy a: K\n  when x {\n    deny(\"r\")\n  }"},
		{name: "call reason and payload", src: "policy a: K\nwhen x { review(\"r\", approvers: a, bake: 1h) }", want: "policy a: K\n  when x {\n    review(\"r\", approvers: a, bake: 1h)\n  }"},
		{name: "call named only", src: "policy a: K\nproduction(approvers: [\"a\"], tiers: t)", want: "policy a: K\n  production(approvers: [\"a\"], tiers: t)"},
		{name: "call trailing comma", src: "policy a: K\nproduction(\n  approvers: [\"a\"],\n)", want: "policy a: K\n  production(approvers: [\"a\"])"},
		{name: "call keyword argument name", src: "policy a: K\nwhen x { review(\"r\", kind: \"k\", type: t) }", want: "policy a: K\n  when x {\n    review(\"r\", kind: \"k\", type: t)\n  }"},
		{name: "call non-literal reason parses", src: "policy a: K\nwhen x { deny(service.tier) }", want: "policy a: K\n  when x {\n    deny(service.tier)\n  }"},
		{name: "call named arg with quantifier", src: "policy a: K\nf(ok: any r in xs: r, n: 1)", want: "policy a: K\n  f(ok: (any r in xs: r), n: 1)"},

		// Kind declarations.
		{name: "type empty", src: "kind K version 1\ntype T {}", want: "kind K version 1\n  type T {\n  }"},
		{name: "type keyword fields", src: "kind K version 1\ntype T { kind: string type: ?int policy: list<string> }",
			want: "kind K version 1\n  type T {\n    kind: string\n    type: ?int\n    policy: list<string>\n  }"},
		{name: "type field named list", src: "kind K version 1\ntype T { list: list<string> map: map<string, string> }",
			want: "kind K version 1\n  type T {\n    list: list<string>\n    map: map<string, string>\n  }"},
		{name: "input", src: "kind K version 1\ninput now: timestamp", want: "kind K version 1\n  input now: timestamp"},
		{name: "fn no params", src: "kind K version 1\nfn now() -> timestamp", want: "kind K version 1\n  fn now() -> timestamp"},
		{name: "fn params", src: "kind K version 1\nfn split(string, string) -> list<string>", want: "kind K version 1\n  fn split(string, string) -> list<string>"},
		{name: "fn composite params", src: "kind K version 1\nfn f(list<string>, map<string, int>, ?Release) -> int", want: "kind K version 1\n  fn f(list<string>, map<string, int>, ?Release) -> int"},
		{name: "fn trailing comma", src: "kind K version 1\nfn f(int,) -> int", want: "kind K version 1\n  fn f(int) -> int"},
		{name: "decision reason only", src: "kind K version 1\ndecision deny(reason: string)", want: "kind K version 1\n  decision deny(reason: string)"},
		{name: "decision defaults", src: "kind K version 1\ndecision approve(reason: string, bake: duration = 1h, detail: string = \"\",)",
			want: "kind K version 1\n  decision approve(reason: string, bake: duration = 1h, detail: string = \"\")"},
		{name: "precedence one", src: "kind K version 1\nprecedence allow", want: "kind K version 1\n  precedence allow"},
		{name: "precedence chain", src: "kind K version 1\nprecedence deny > review > approve", want: "kind K version 1\n  precedence deny > review > approve"},
		{name: "collect all", src: "kind K version 1\ncollect all", want: "kind K version 1\n  collect all"},
		{name: "default", src: "kind K version 1\ndefault deny(\"none\", detail: \"\")", want: "kind K version 1\n  default deny(\"none\", detail: \"\")"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, errs := ParseFile("test.sigil", []byte(tt.src))
			if errs != nil {
				t.Fatalf("ParseFile() errors:\n%v", errs)
			}
			if got := stripSpans(ast.Dump(f)); got != tt.want {
				t.Errorf("ParseFile() =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

// stripSpans removes the trailing " [l:c-l:c]" from every dump line.
func stripSpans(dump string) string {
	lines := strings.Split(strings.TrimSuffix(dump, "\n"), "\n")
	for i, l := range lines {
		if j := strings.LastIndex(l, " ["); j >= 0 && strings.HasSuffix(l, "]") {
			lines[i] = l[:j]
		}
	}
	return strings.Join(lines, "\n")
}

// TestParseFileErrors pins the statement-level messages: what each says,
// and that parsing continues afterwards where it should. Errors are
// written as "line:col: message"; help is checked where it's the point.
func TestParseFileErrors(t *testing.T) {
	tests := []struct {
		name string
		src  string
		errs []string
		help string // of the first error, when given
		want string // dump without spans, when given
	}{
		// Headers.
		{name: "policy without name", src: "policy", errs: []string{"1:7: expected a name after `policy`, found end of file"}},
		{name: "policy without colon", src: "policy a b", errs: []string{"1:10: expected `:`, found `b`"}, help: "a policy starts with `policy name: Kind`"},
		{name: "policy without kind", src: "policy a:", errs: []string{"1:10: expected the kind's name after `:`, found end of file"}},
		{name: "policy kind not identifier", src: "policy a: 1", errs: []string{"1:11: expected the kind's name after `:`, found `1`"}},
		{name: "policy keyword name part", src: "policy a.default: K", errs: []string{"1:10: expected a name after `.`, found `default`"}},
		{name: "kind without version", src: "kind K", errs: []string{"1:7: expected `version`, found end of file"}, help: "a kind starts with `kind Name version N`"},
		{name: "kind version not a number", src: "kind K version x", errs: []string{"1:16: expected a version number, found `x`"}},
		{name: "kind version is a float", src: "kind K version 1.0", errs: []string{"1:16: expected a version number, found `1.0`"}},
		{name: "broken header loses only its document", src: "policy : K\nlet a = b\nmodule m: K\nlet c = d",
			errs: []string{"1:8: expected a name after `policy`, found `:`"}, want: "module m: K\n  let c = d"},

		// Names.
		{name: "space before dot", src: "policy a: K\nuse deploy .common", errs: []string{"2:12: a name can't have spaces around `.`"}, help: "remove the space before the `.`"},
		{name: "space after dot", src: "policy a: K\nuse deploy. common", errs: []string{"2:11: a name can't have spaces around `.`"}, help: "remove the space after the `.`"},
		{name: "trailing dot", src: "policy a: K\nuse deploy.", errs: []string{"2:12: expected a name after `.`, found end of file"}},
		{name: "dot then number", src: "policy a: K\nuse deploy.1", errs: []string{"2:12: expected a name after `.`, found `1`"}},

		// Imports.
		{name: "use without name", src: "policy a: K\nuse", errs: []string{"2:4: expected a policy or module name after `use`, found end of file"}},
		{name: "use as without name", src: "policy a: K\nuse a as", errs: []string{"2:9: expected a name after `as`, found end of file"}},
		{name: "use empty selection", src: "policy a: K\nuse a.{}", errs: []string{"2:7: a selective import needs at least one name"}},
		{name: "use selection not a name", src: "policy a: K\nuse a.{1}", errs: []string{"2:8: expected a name to import, found `1`"}},
		{name: "use selection unclosed", src: "policy a: K\nuse a.{x", errs: []string{"2:9: expected `}`, found end of file"}, help: "to close the `{` at 2:7"},
		{name: "use selection missing comma", src: "policy a: K\nuse a.{x y}", errs: []string{"2:10: expected `}`, found `y`"}},
		{name: "use after param", src: "policy a: K\nparam x: int\nuse b", errs: []string{"3:1: `use` must come before every other statement"}, want: "policy a: K\n  use b\n  param x: int"},
		{name: "use after use is fine", src: "policy a: K\nuse b\nuse c", want: "policy a: K\n  use b\n  use c"},

		// Params and lets.
		{name: "param without name", src: "policy a: K\nparam", errs: []string{"2:6: expected a name after `param`, found end of file"}},
		{name: "param without colon", src: "policy a: K\nparam x", errs: []string{"2:8: expected `:`, found end of file"}, help: "a param is written `param name: type` or `param name: type = default`"},
		{name: "param without type", src: "policy a: K\nparam x:", errs: []string{"2:9: expected a type, found end of file"}},
		{name: "param without default after equals", src: "policy a: K\nparam x: int =", errs: []string{"2:15: expected an expression, found end of file"}},
		{name: "let without equals", src: "policy a: K\nlet x", errs: []string{"2:6: expected `=`, found end of file"}, help: "a let is written `let name = expression`"},
		{name: "let keyword name", src: "policy a: K\nlet type = 1", errs: []string{"2:5: expected a name after `let`, found `type`"}},

		// Types.
		{name: "list without arguments", src: "policy a: K\nparam x: list", errs: []string{"2:14: expected `<`, found end of file"}, help: "a list type is written `list<T>`"},
		{name: "list unclosed", src: "policy a: K\nparam x: list<int", errs: []string{"2:18: expected `>`, found end of file"}, help: "to close the `<` at 2:14"},
		{name: "map one argument", src: "policy a: K\nparam x: map<string>", errs: []string{"2:20: expected `,`, found `>`"}, help: "a map type is written `map<K, V>`"},
		{name: "optional without type", src: "policy a: K\nparam x: ?", errs: []string{"2:11: expected a type, found end of file"}},
		{name: "optional nested glued", src: "policy a: K\nparam x: ??string", errs: []string{"2:10: optional types don't nest"}, help: "write `?T` with a single `?`"},
		{name: "optional nested spaced", src: "policy a: K\nparam x: ? ?string", errs: []string{"2:10: optional types don't nest"}},
		// The keyword then starts a statement, whose own error follows.
		{name: "type is a keyword", src: "policy a: K\nparam x: when", errs: []string{"2:10: expected a type, found `when`", "2:14: expected an expression, found end of file"}},

		// Rules.
		// `{}` is a map literal, so the condition is fine and the body is missing.
		{name: "when without condition", src: "policy a: K\nwhen {}", errs: []string{"2:8: expected `{` after the condition, found end of file"}},
		{name: "when without body", src: "policy a: K\nwhen x", errs: []string{"2:7: expected `{` after the condition, found end of file"}, help: "a rule is written `when condition { ... }`"},
		{name: "when unclosed", src: "policy a: K\nwhen x {\n  deny(\"r\")\n", errs: []string{"4:1: expected `}`, found end of file"}, help: "to close the `{` at 2:8"},
		{name: "when closed by separator", src: "policy a: K\nwhen x {\n---\nmodule m: K", errs: []string{"3:1: expected `}`, found `---`"}, want: "policy a: K\n---\nmodule m: K"},
		{name: "when body keeps later statements", src: "policy a: K\nwhen x {\n  deny(\"r\"\n  approve(\"ok\")\n}",
			errs: []string{"4:3: expected `)`, found `approve`"}, want: "policy a: K\n  when x {\n    approve(\"ok\")\n  }"},
		{name: "let in body", src: "policy a: K\nwhen x { let y = 1 deny(\"r\") }",
			errs: []string{"2:10: expected a decision constructor, invocation, `when` or `assert`, found `let`"},
			help: "`let` is only allowed at the top level; move it outside the `when` block",
			want: "policy a: K\n  when x {\n    deny(\"r\")\n  }"},
		{name: "param in body", src: "policy a: K\nwhen x { param y: int }",
			errs: []string{"2:10: expected a decision constructor, invocation, `when` or `assert`, found `param`"}},
		{name: "use in body", src: "policy a: K\nwhen x { use y }",
			errs: []string{"2:10: expected a decision constructor, invocation, `when` or `assert`, found `use`"},
			help: "`use` is only allowed right after the header; move it up"},
		{name: "expression in body", src: "policy a: K\nwhen x { a == b }",
			errs: []string{"2:10: expected a decision constructor, invocation, `when` or `assert`, found `a`"}},
		{name: "bare name at top level", src: "policy a: K\nguardrails",
			errs: []string{"2:1: expected a statement, found `guardrails`"},
			help: "a bare name isn't a statement; an invocation needs parentheses, like `guardrails()`"},
		{name: "stray closing brace", src: "policy a: K\n}\nlet x = 1",
			errs: []string{"2:1: expected `param`, `let`, `when`, `assert` or an invocation, found `}`"}, want: "policy a: K\n  let x = 1"},
		{name: "kind declaration in policy", src: "policy a: K\ntype T {}",
			errs: []string{"2:1: expected `param`, `let`, `when`, `assert` or an invocation, found `type`"}},

		// Asserts.
		{name: "assert without comma", src: "policy a: K\nassert x", errs: []string{"2:9: expected `,`, found end of file"}, help: "an assert is written `assert condition, \"reason\"`"},
		{name: "assert without reason", src: "policy a: K\nassert x,", errs: []string{"2:10: expected a string literal reason, found end of file"}},
		{name: "assert raw reason", src: "policy a: K\nassert x, `r`", errs: []string{"2:11: the reason must be a double-quoted string"}, help: "raw strings are for patterns; write the reason as \"...\""},
		{name: "assert computed reason", src: "policy a: K\nassert x, r", errs: []string{"2:11: expected a string literal reason, found `r`"}},

		// Calls.
		{name: "call unclosed", src: "policy a: K\nf(", errs: []string{"2:3: expected an expression, found end of file"}},
		{name: "call unclosed after arg", src: "policy a: K\nf(a", errs: []string{"2:4: expected `)`, found end of file"}, help: "to close the `(` at 2:2"},
		{name: "call missing comma", src: "policy a: K\nf(a b)", errs: []string{"2:5: expected `)`, found `b`"}},
		{name: "call second positional", src: "policy a: K\nf(a, b)", errs: []string{"2:6: expected a named argument, found `b`"}, help: "after the reason, every argument is written `name: value`, like `approvers: [\"payments-leads\"]`"},
		{name: "call named without value", src: "policy a: K\nf(a: )", errs: []string{"2:6: expected an expression, found `)`"}},
		{name: "call leading comma", src: "policy a: K\nf(, a)", errs: []string{"2:3: expected an expression, found `,`"}},
		{name: "call double comma", src: "policy a: K\nf(a: 1,, b: 2)", errs: []string{"2:8: expected a named argument, found `,`"}},

		// Modules.
		{name: "module with when", src: "module m: K\nwhen x { deny(\"r\") }\nlet a = 1",
			errs: []string{"2:1: a module can't contain `when`"}, want: "module m: K\n  let a = 1"},
		{name: "module with param", src: "module m: K\nparam x: int", errs: []string{"2:1: a module can't contain `param`"}},
		{name: "module with assert", src: "module m: K\nassert x, \"r\"", errs: []string{"2:1: a module can't contain `assert`"}},
		{name: "module with invocation", src: "module m: K\nf()", errs: []string{"2:1: a module can't contain an invocation"},
			help: "a module holds only imports and lets; rules, params and invocations belong in a policy"},
		{name: "module use after let", src: "module m: K\nlet a = 1\nuse b", errs: []string{"3:1: `use` must come before every other statement"}},

		// Kind declarations.
		{name: "type without name", src: "kind K version 1\ntype {}", errs: []string{"2:6: expected a type name after `type`, found `{`"}},
		{name: "type without body", src: "kind K version 1\ntype T", errs: []string{"2:7: expected `{`, found end of file"}, help: "a type is written `type Name { field: type }`"},
		{name: "type field without colon", src: "kind K version 1\ntype T { a }", errs: []string{"2:12: expected `:`, found `}`"}, help: "a field is written `name: type`"},
		{name: "type field without type", src: "kind K version 1\ntype T { a: }", errs: []string{"2:13: expected a type, found `}`"}},
		{name: "type field default", src: "kind K version 1\ntype T { a: int = 1 }", errs: []string{"2:17: a field in a `type` body can't have a default"}, help: "defaults belong to decision payload fields"},
		{name: "type unclosed at end of file", src: "kind K version 1\ntype T { a: int", errs: []string{"2:16: expected `}`, found end of file"}, help: "to close the `{` at 2:8"},
		{name: "type unclosed before declaration", src: "kind K version 1\ntype T { a: int\ninput x: int", errs: []string{"3:1: expected `}`, found `input`"}, want: "kind K version 1\n  input x: int"},
		{name: "type unclosed before header", src: "kind K version 1\ntype T { a: int\npolicy p: K", errs: []string{"3:1: expected `}`, found `policy`"}, want: "kind K version 1\n---\npolicy p: K"},
		{name: "input without type", src: "kind K version 1\ninput x", errs: []string{"2:8: expected `:`, found end of file"}, help: "an input is written `input name: type`"},
		{name: "fn without parens", src: "kind K version 1\nfn f -> int", errs: []string{"2:6: expected `(`, found `->`"}, help: "a function is written `fn name(type, type) -> type`"},
		{name: "fn named param", src: "kind K version 1\nfn f(s: string) -> int", errs: []string{"2:6: expected a parameter type, found a name"}, help: "parameters have types only; policies pass arguments by position"},
		{name: "fn param keyword", src: "kind K version 1\nfn f(type) -> int", errs: []string{"2:6: expected a type, found `type`", "2:10: expected a type name after `type`, found `)`"}},
		{name: "fn param not a type", src: "kind K version 1\nfn f(1) -> int", errs: []string{"2:6: expected a type, found `1`"}},
		{name: "fn without arrow", src: "kind K version 1\nfn f() int", errs: []string{"2:8: expected `->`, found `int`"}},
		{name: "fn without result", src: "kind K version 1\nfn f() ->", errs: []string{"2:10: expected a type, found end of file"}},
		{name: "decision without fields", src: "kind K version 1\ndecision d()", errs: []string{"2:12: expected a payload field like `reason: string`, found `)`"}},
		{name: "decision field without type", src: "kind K version 1\ndecision d(reason)", errs: []string{"2:18: expected `:`, found `)`"}, help: "a field is written `name: type` or `name: type = default`"},
		{name: "decision unclosed", src: "kind K version 1\ndecision d(reason: string", errs: []string{"2:26: expected `)`, found end of file"}, help: "to close the `(` at 2:11"},
		{name: "precedence without names", src: "kind K version 1\nprecedence", errs: []string{"2:11: expected a decision name after `precedence`, found end of file"}, help: "precedence is written `precedence deny > review > approve`, highest first"},
		{name: "precedence trailing arrow", src: "kind K version 1\nprecedence a >", errs: []string{"2:15: expected a decision name after `>`, found end of file"}},
		{name: "precedence wrong operator", src: "kind K version 1\nprecedence a < b", errs: []string{"2:14: expected a declaration (`type`, `input`, `fn`, `decision`, `precedence`, `collect` or `default`), found `<`"}},
		{name: "collect without all", src: "kind K version 1\ncollect", errs: []string{"2:8: expected `all`, found end of file"}, help: "a collecting kind is declared with `collect all`"},
		{name: "collect any", src: "kind K version 1\ncollect any", errs: []string{"2:9: expected `all`, found `any`"}},
		{name: "default without constructor", src: "kind K version 1\ndefault", errs: []string{"2:8: expected a decision constructor, found end of file"}, help: "the default is written `default deny(\"no_rule_matched\")`"},
		{name: "default bare name", src: "kind K version 1\ndefault deny", errs: []string{"2:9: expected a decision constructor, found `deny`"}},
		{name: "policy statement in kind", src: "kind K version 1\nwhen x {}", errs: []string{"2:1: expected a declaration (`type`, `input`, `fn`, `decision`, `precedence`, `collect` or `default`), found `when`"}},
		{name: "broken declaration keeps the rest", src: "kind K version 1\ninput x\ninput y: int", errs: []string{"3:1: expected `:`, found `input`"}, want: "kind K version 1\n  input y: int"},

		// Document boundaries.
		{name: "statement before header", src: "let a = 1\npolicy p: K", errs: []string{"1:1: expected a document header (`policy`, `module` or `kind`), found `let`"}, want: "policy p: K"},
		{name: "operand after separator", src: "policy p: K\nlet a = b\n---\n1h", errs: []string{"3:1: `---` separates documents and can't appear inside an expression"}},
		{name: "keyword after separator", src: "policy p: K\n---\nlet a = b", errs: []string{"3:1: expected a document header (`policy`, `module` or `kind`), found `let`"}},
		{name: "separator inside expression", src: "policy p: K\nlet a = b - ---c", errs: []string{"2:13: `---` separates documents and can't appear inside an expression"}},
		{name: "lexical error only once", src: "policy p: K\nlet a = 1h1h\nlet b = 2", errs: []string{"2:9: unit `h` appears twice in `1h1h`"}, want: "policy p: K\n  let b = 2"},
		{name: "lexical errors after a parse error", src: "policy p: K\nlet a =\nlet b = @", errs: []string{"3:1: expected an expression, found `let`", "3:9: unexpected character `@`"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, errs := ParseFile("test.sigil", []byte(tt.src))
			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Pos.String() + ": " + e.Msg
			}
			if g, w := strings.Join(got, "\n"), strings.Join(tt.errs, "\n"); g != w {
				t.Errorf("errors:\n%s\nwant:\n%s", g, w)
			}
			if tt.help != "" && (len(errs) == 0 || errs[0].Help != tt.help) {
				var h string
				if len(errs) > 0 {
					h = errs[0].Help
				}
				t.Errorf("help = %q\nwant   %q", h, tt.help)
			}
			if tt.want != "" {
				if got := stripSpans(ast.Dump(f)); got != tt.want {
					t.Errorf("ParseFile() =\n%s\nwant\n%s", got, tt.want)
				}
			}
		})
	}
}

func TestParseFileNilCondition(t *testing.T) {
	f, errs := ParseFile("test.sigil", []byte("policy p: K\nwhen a < b < c { deny(\"r\") }"))
	if len(errs) != 1 {
		t.Fatalf("got %d errors, want 1: %v", len(errs), errs)
	}
	doc := f.Docs[0].(*ast.PolicyDoc)
	w := doc.Stmts[0].(*ast.WhenStmt)
	bad, ok := w.Cond.(*ast.BadExpr)
	if !ok {
		t.Fatalf("Cond = %T, want *ast.BadExpr", w.Cond)
	}
	if got := bad.Pos().String() + "-" + bad.End().String(); got != "2:6-2:16" {
		t.Errorf("BadExpr span = %s, want 2:6-2:16", got)
	}
	if len(w.Body) != 1 {
		t.Errorf("body has %d statements, want 1", len(w.Body))
	}
}
