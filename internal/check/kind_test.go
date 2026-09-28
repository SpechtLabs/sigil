package check_test

import (
	"os"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
)

const deploy = `kind DeployApproval version 1

type Release {
  soak: duration
  hotfix: bool
}
type Service {
  name: string
  tier: string
  owners: list<string>
  labels: map<string, string>
}
type Actor {
  name: string
  teams: list<string>
  roles: list<string>
  regions: list<string>
}

input release: Release
input service: Service
input actor: Actor
input environment: string

fn split(string, string) -> list<string>

decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)

collect one
precedence deny > review > approve
default deny("no_rule_matched")
`

// TestLoadKind checks that clean kind files load into the model by printing
// the model back as canonical source, and pins the message, hint and
// position of every error the loader and the model's rules produce.
func TestLoadKind(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string   // canonical source, when the file is valid
		errs []string // "line:col: msg", when it isn't
		help string   // of the first error, when it's the point
	}{
		{name: "README kind round-trips", src: deploy, want: deploy},
		{name: "collecting kind", src: `kind AccessGrant version 1
type Actor { name: string groups: list<string> clearance: string }
input actor: Actor
decision read(reason: string)
decision admin(reason: string, ttl: duration = 8h)
collect all
`, want: `kind AccessGrant version 1

type Actor {
  name: string
  groups: list<string>
  clearance: string
}

input actor: Actor

decision read(reason: string)
decision admin(reason: string, ttl: duration = 8h)

collect all
`},
		{name: "kind that accepts older versions", src: "kind K version 3, accepts: 2\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			want: "kind K version 3, accepts: 2\n\ndecision d(reason: string)\n\ncollect one\nprecedence d\ndefault d(\"x\")\n"},
		{name: "accepts 1 is the default", src: "kind K version 3, accepts: 1\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			want: "kind K version 3\n\ndecision d(reason: string)\n\ncollect one\nprecedence d\ndefault d(\"x\")\n"},
		{name: "collecting kind with a default", src: "kind K version 1\ndecision read(reason: string)\ncollect all\ndefault read(\"everyone\")",
			want: "kind K version 1\n\ndecision read(reason: string)\n\ncollect all\ndefault read(\"everyone\")\n"},
		{name: "declarations in any order", src: `kind K version 3
default allow("none", tags: ["x"], weight: 1 + 1)
collect one
precedence allow
decision allow(reason: string, weight: int = 0, tags: list<string> = [])
input a: A
type A { b: B next: ?A2 }
type A2 { n: int }
type B { m: map<int, list<string>> }
`, want: `kind K version 3

type A {
  b: B
  next: ?A2
}
type A2 {
  n: int
}
type B {
  m: map<int, list<string>>
}

input a: A

decision allow(reason: string, weight: int = 0, tags: list<string> = [])

collect one
precedence allow
default allow("none", weight: 2, tags: ["x"])
`},
		{name: "keyword field and payload names", src: "kind K version 1\ntype R { kind: string type: int }\ninput r: R\ndecision d(reason: string, kind: string = \"\")\ncollect one\nprecedence d\ndefault d(\"x\", kind: \"cluster\")\n",
			want: "kind K version 1\n\ntype R {\n  kind: string\n  type: int\n}\n\ninput r: R\n\ndecision d(reason: string, kind: string = \"\")\n\ncollect one\nprecedence d\ndefault d(\"x\", kind: \"cluster\")\n"},
		{name: "constant defaults of every shape", src: `kind K version 1
decision d(reason: string, i: int = -3, f: float = 0.5 + 0.25, s: string = "a", b: bool = true, dur: duration = 1h + 30m, l: list<int> = [1, -2], m: map<string, list<int>> = {"a": [1]}, o: ?string = "x")
collect one
precedence d
default d("x")
`, want: `kind K version 1

decision d(reason: string, i: int = -3, f: float = 0.75, s: string = "a", b: bool = true, dur: duration = 1h30m, l: list<int> = [1, -2], m: map<string, list<int>> = {"a": [1]}, o: ?string = "x")

collect one
precedence d
default d("x")
`},

		// File shape.
		{name: "empty file", src: "", errs: []string{"1:1: file has no kind document"}, help: "a kind file starts with `kind Name version N`"},
		{name: "two documents", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")\n---\npolicy p: K",
			errs: []string{"7:1: a kind file holds exactly one document"}, help: "move the other documents to their own files"},
		{name: "policy instead of kind", src: "policy deploy.production: K@1\nlet a = 1", errs: []string{"1:1: expected a kind document, found policy `deploy.production`"}},
		{name: "module instead of kind", src: "module deploy.common: K@1", errs: []string{"1:1: expected a kind document, found module `deploy.common`"}},
		{name: "parse errors come alone", src: "kind K version 1\ntype T { a }\ndecision d()", errs: []string{"2:12: expected `:`, found `}`", "3:12: expected a payload field like `reason: string`, found `)`"}},

		// Header.
		{name: "accepts above the version", src: "kind K version 2, accepts: 3\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"1:28: kind K at version 2 can't accept version 3"}, help: "`accepts` names the oldest version policies may still pin, between 1 and the version"},
		{name: "version zero", src: "kind K version 0\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"1:16: invalid kind version 0"}, help: "the version is a positive integer that changes when the contract does"},

		// Types.
		{name: "unknown type with suggestion", src: "kind K version 1\ntype Release { soak: duration }\ninput r: Relaese\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:10: unknown type `Relaese`"}, help: "did you mean `Release`?"},
		{name: "unknown type suggests a built-in", src: "kind K version 1\ninput r: strng\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:10: unknown type `strng`"}, help: "did you mean `string`?"},
		{name: "unknown type without suggestion", src: "kind K version 1\ninput r: list<Ticket>\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:15: unknown type `Ticket`"}, help: "declare it with `type Ticket { ... }`, or use a built-in type"},
		{name: "unknown type reported once per use", src: "kind K version 1\ntype R { a: T b: T }\ninput r: R\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:13: unknown type `T`", "2:18: unknown type `T`"}},
		{name: "type declared twice keeps both fields sets apart", src: "kind K version 1\ntype R { a: int }\ntype R { b: int }\ninput r: R\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:6: type \"R\" is declared twice"}, help: "give each type one declaration"},
		{name: "type shadows a built-in", src: "kind K version 1\ntype string { a: int }\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:6: type \"string\" shadows a built-in type"}},
		{name: "field declared twice", src: "kind K version 1\ntype R { a: int a: string }\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:17: type R: field \"a\" is declared twice"}},
		{name: "recursive type", src: "kind K version 1\ntype Node { next: ?Node }\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:6: type Node is recursive: Node -> Node"}},
		{name: "mutually recursive types", src: "kind K version 1\ntype A { b: B }\ntype B { a: list<A> }\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:6: type A is recursive: A -> B -> A", "3:6: type B is recursive: B -> A -> B"}},
		{name: "optional list", src: "kind K version 1\ninput tags: ?list<string>\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:13: input \"tags\": a list can't be optional"}},
		{name: "optional map", src: "kind K version 1\ninput labels: ?map<string, string>\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:15: input \"labels\": a map can't be optional"}},
		{name: "map with a list key", src: "kind K version 1\ninput m: map<list<string>, int>\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:14: list<string> can't be a map key"}},
		{name: "map with a struct key", src: "kind K version 1\ntype R { a: int }\ninput m: map<R, int>\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:14: R can't be a map key"}},
		{name: "map with an unknown key type", src: "kind K version 1\ninput m: map<Ticket, int>\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:14: unknown type `Ticket`"}},

		// Inputs and functions.
		{name: "input declared twice", src: "kind K version 1\ninput a: int\ninput a: string\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:7: input \"a\" collides with input \"a\""}},
		{name: "function collides with input", src: "kind K version 1\ninput split: int\nfn split(string) -> list<string>\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:4: function \"split\" collides with input \"split\""}, help: "inputs and host functions share one namespace"},
		{name: "function with optional result", src: "kind K version 1\nfn f() -> ?string\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:11: function f: the result can't be optional"}},
		{name: "function parameter of unknown type", src: "kind K version 1\nfn f(int, Ticket) -> int\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:11: unknown type `Ticket`"}},

		// Decisions.
		{name: "decision without reason", src: "kind K version 1\ndecision d(approvers: list<string>)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:10: decision d must declare `reason: string` as its first field", "5:1: default: field \"approvers\" is required and has no value"}, help: "every decision takes a literal reason first; payload fields follow it"},
		{name: "decision with reason second", src: "kind K version 1\ndecision d(approvers: list<string>, reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:10: decision d must declare `reason: string` as its first field", "2:37: decision d, field \"reason\": reason is implied and can't be declared as a payload field", "5:1: default: field \"approvers\" is required and has no value"}},
		{name: "reason of the wrong type", src: "kind K version 1\ndecision d(reason: int)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:20: reason must be a string, not int"}, help: "the reason is a stable identifier, so it's always a string"},
		{name: "reason with a default", src: "kind K version 1\ndecision d(reason: string = \"x\")\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:29: reason can't have a default"}},
		{name: "decision declared twice", src: "kind K version 1\ndecision d(reason: string)\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:10: decision \"d\" is declared twice"}},
		{name: "payload field declared twice", src: "kind K version 1\ndecision d(reason: string, a: int, a: int)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:36: decision d, field \"a\": declared twice", "5:1: default: field \"a\" is required and has no value"}},
		{name: "payload default of the wrong type", src: "kind K version 1\ndecision d(reason: string, bake: duration = 1)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:45: expected duration, found int"}},
		{name: "payload default not constant", src: "kind K version 1\ninput a: int\ndecision d(reason: string, n: int = a + 1)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:37: `a` isn't a constant"}, help: "a constant is a literal, a list or map of literals, or `+` and `-` applied to those"},
		{name: "payload default overflows", src: "kind K version 1\ndecision d(reason: string, n: int = 9223372036854775807 + 1)\ncollect one\nprecedence d\ndefault d(\"x\")",
			errs: []string{"2:37: integer overflow in constant"}},

		// Resolution.
		{name: "no collect", src: "kind K version 1\ndecision d(reason: string)\ndefault d(\"x\")",
			errs: []string{"1:6: kind K doesn't declare how many decisions it returns"}, help: "declare `collect one` with a `precedence`, or `collect all`"},
		{name: "precedence without collect", src: "kind K version 1\ndecision d(reason: string)\nprecedence d\ndefault d(\"x\")",
			errs: []string{"3:1: kind K has precedence but no collect"}, help: "declare `collect one` to return the highest-ranked decision"},
		{name: "collect one without precedence", src: "kind K version 1\ndecision d(reason: string)\ncollect one\ndefault d(\"x\")",
			errs: []string{"3:1: kind K collects one decision but has no precedence"}, help: "`collect one` returns the highest-ranked decision; rank them with `precedence deny > review > approve`, highest first"},
		{name: "collect all with precedence is reserved", src: "kind K version 1\ndecision d(reason: string)\ncollect all\nprecedence d",
			errs: []string{"4:1: kind K has both precedence and collect all"}, help: "`collect all` with `precedence` is reserved; remove `precedence` to return every decision that fired"},
		{name: "collect one and collect all", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\ncollect all\ndefault d(\"x\")",
			errs: []string{"5:1: collect is declared twice"}, help: "a kind declares `collect` once; remove one"},
		{name: "precedence twice", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\nprecedence d\ndefault d(\"x\")",
			errs: []string{"5:1: precedence is declared twice"}, help: "a kind declares `precedence` once; remove one"},
		{name: "collect twice", src: "kind K version 1\ndecision d(reason: string)\ncollect all\ncollect all",
			errs: []string{"4:1: collect is declared twice"}},
		{name: "default twice", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"x\")\ndefault d(\"y\")",
			errs: []string{"6:1: default is declared twice"}},
		{name: "precedence misses a decision", src: "kind K version 1\ndecision a(reason: string)\ndecision b(reason: string)\ncollect one\nprecedence a\ndefault a(\"x\")",
			errs: []string{"5:1: precedence doesn't name decision \"b\""}, help: "list every decision exactly once, highest first"},
		{name: "precedence names a decision twice", src: "kind K version 1\ndecision a(reason: string)\ncollect one\nprecedence a > a\ndefault a(\"x\")",
			errs: []string{"4:16: precedence names \"a\" twice"}},
		{name: "precedence names an unknown decision", src: "kind K version 1\ndecision a(reason: string)\ncollect one\nprecedence a > b\ndefault a(\"x\")",
			errs: []string{"4:16: precedence names undeclared decision \"b\""}},
		{name: "no default", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d",
			errs: []string{"1:6: kind K has no default decision"}, help: "declare `default <decision>(\"<reason>\")` for the case where no rule fires"},
		{name: "default names an unknown decision", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault deny(\"x\", a: 1)",
			errs: []string{"5:1: default names undeclared decision \"deny\""}},
		{name: "default without reason", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d()",
			errs: []string{"5:9: the default needs a reason"}, help: "the default is written `default deny(\"no_rule_matched\")`"},
		{name: "default with a computed reason", src: "kind K version 1\ninput r: string\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(r)",
			errs: []string{"6:11: the default's reason must be a string literal"}},
		{name: "default with a raw reason", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(`x`)",
			errs: []string{"5:11: the default's reason must be a double-quoted string"}},
		{name: "default with an empty reason", src: "kind K version 1\ndecision d(reason: string)\ncollect one\nprecedence d\ndefault d(\"\")",
			errs: []string{"5:11: default d has an empty reason"}},
		{name: "default with an unknown field", src: "kind K version 1\ndecision approve(reason: string, bake: duration = 1h)\ncollect one\nprecedence approve\ndefault approve(\"x\", bak: 15m)",
			errs: []string{"5:22: decision approve has no payload field \"bak\""}, help: "approve is declared as: decision approve(reason: string, bake: duration = 1h)"},
		{name: "default with a wrong value", src: "kind K version 1\ndecision approve(reason: string, bake: duration = 1h)\ncollect one\nprecedence approve\ndefault approve(\"x\", bake: \"15m\")",
			errs: []string{"5:28: expected duration, found string"}},
		{name: "default gives a field twice", src: "kind K version 1\ndecision approve(reason: string, bake: duration = 1h)\ncollect one\nprecedence approve\ndefault approve(\"x\", bake: 1h, bake: 2h)",
			errs: []string{"5:32: field \"bake\" is given twice"}},
		{name: "default misses a required field", src: "kind K version 1\ndecision review(reason: string, approvers: list<string>)\ncollect one\nprecedence review\ndefault review(\"x\")",
			errs: []string{"5:1: default: field \"approvers\" is required and has no value"}, help: "review is declared as: decision review(reason: string, approvers: list<string>)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, errs := check.LoadKind("k.sigil", []byte(tt.src))
			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Pos.String() + ": " + e.Msg
				if e.File != "k.sigil" {
					t.Errorf("error %d has File %q, want k.sigil", i, e.File)
				}
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
				if k == nil {
					t.Fatal("LoadKind() returned no kind")
				}
				if got := k.Source(); got != tt.want {
					t.Errorf("Source() =\n%s\nwant\n%s", got, tt.want)
				}
			} else if k != nil {
				t.Errorf("LoadKind() returned a kind alongside errors")
			}
		})
	}
}

// TestKindRoundTrip checks that loading what a kind prints gives the same
// kind, which is the property the exporter will be held to.
func TestKindRoundTrip(t *testing.T) {
	first, errs := check.LoadKind("k.sigil", []byte(deploy))
	if errs != nil {
		t.Fatal(errs)
	}
	second, errs := check.LoadKind("k.sigil", []byte(first.Source()))
	if errs != nil {
		t.Fatal(errs)
	}
	if first.Source() != second.Source() {
		t.Errorf("round trip differs:\n%s\n%s", first.Source(), second.Source())
	}
}

// TestKindParserTestdata loads the kind documents from the parser's golden
// inputs, so the two packages agree on what a kind file is.
func TestKindParserTestdata(t *testing.T) {
	for _, name := range []string{"deploy_approval", "access"} {
		src, err := os.ReadFile("../parser/testdata/" + name + ".sigil")
		if err != nil {
			t.Fatal(err)
		}
		// access.sigil holds policies after the kind; take the first document.
		first := strings.SplitN(string(src), "\n---\n", 2)[0]
		if _, errs := check.LoadKind(name+".sigil", []byte(first)); errs != nil {
			t.Errorf("%s: %v", name, errs)
		}
	}
}
