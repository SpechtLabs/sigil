package check_test

import (
	"os"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
)

const deploy = `kind DeployApproval version 1

enum Tier: critical | standard | internal
enum Plan: free | standard | enterprise

type Release {
  soak: duration
  hotfix: bool
}

type Service {
  name: string
  tier: Tier
  plans: list<Plan>
  owners: list<string>
  labels: map<string, string>
  by_tier: map<Tier, int>
  backup: ?Tier
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
fn tier_of(string) -> Tier

decision deny {
  reason: no_rule_matched | x | a | b | not_eligible | soak_too_short
}

decision review {
  reason: r | c | x | service_owner
  approvers: list<string>
}

decision approve {
  reason: ok | x | b | release_manager | sre_hotfix
  bake: duration = 1h
  tier: Tier = standard
}

collect one
precedence deny > review > approve

default deny(reason: no_rule_matched)
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

type Actor {
  name: string
  groups: list<string>
  clearance: string
}

input actor: Actor

decision read {
  reason: everyone
}

decision admin {
  reason: x
  ttl: duration = 8h
}

collect all
`, want: `kind AccessGrant version 1

type Actor {
  name: string
  groups: list<string>
  clearance: string
}

input actor: Actor

decision read {
  reason: everyone
}

decision admin {
  reason: x
  ttl: duration = 8h
}

collect all
`},
		{name: "kind that accepts older versions", src: "kind K version 3, accepts: 2\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			want: "kind K version 3, accepts: 2\n\ndecision d {\n  reason: a | x | y\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x)\n"},
		{name: "accepts 1 is the default", src: "kind K version 3, accepts: 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			want: "kind K version 3\n\ndecision d {\n  reason: a | x | y\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x)\n"},
		{name: "collecting kind with a default", src: "kind K version 1\ndecision read { reason: everyone }\ncollect all\ndefault read(reason: everyone)",
			want: "kind K version 1\n\ndecision read {\n  reason: everyone\n}\n\ncollect all\n\ndefault read(reason: everyone)\n"},
		{name: "declarations in any order", src: `kind K version 3

default allow(tags: ["x"], weight: 1 + 1, reason: none, level: high)

collect one
precedence allow

decision allow {
  weight: int = 0
  tags: list<string> = []
  reason: none
  level: Level
}

input a: A

type A {
  b: B
  next: ?A2
}

type A2 {
  n: int
  level: Level
}

type B {
  m: map<int, list<string>>
}

enum Level: low
  | high
`, want: `kind K version 3

enum Level: low | high

type A {
  b: B
  next: ?A2
}

type A2 {
  n: int
  level: Level
}

type B {
  m: map<int, list<string>>
}

input a: A

decision allow {
  reason: none
  weight: int = 0
  tags: list<string> = []
  level: Level
}

collect one
precedence allow

default allow(reason: none, weight: 2, tags: ["x"], level: high)
`},
		{name: "keyword field and payload names", src: "kind K version 1\n\ntype R {\n  kind: string\n  type: int\n}\n\ninput r: R\n\ndecision d {\n  reason: a | x | y\n  kind: string = \"\"\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x, kind: \"cluster\")\n",
			want: "kind K version 1\n\ntype R {\n  kind: string\n  type: int\n}\n\ninput r: R\n\ndecision d {\n  reason: a | x | y\n  kind: string = \"\"\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x, kind: \"cluster\")\n"},
		{name: "constant defaults of every shape", src: `kind K version 1

enum Tier: critical | standard

decision d {
  reason: a | x | y
  i: int = -3
  f: float = 0.5 + 0.25
  s: string = "a"
  b: bool = true
  dur: duration = 1h + 30m
  l: list<int> = [1, -2]
  m: map<string, list<int>> = {"a": [1]}
  o: ?string = "x"
  t: Tier = critical
  ot: ?Tier = (standard)
  lt: list<Tier> = [standard, critical]
  mt: map<Tier, list<Tier>> = {critical: [standard]}
}

collect one
precedence d

default d(reason: x)
`, want: `kind K version 1

enum Tier: critical | standard

decision d {
  reason: a | x | y
  i: int = -3
  f: float = 0.75
  s: string = "a"
  b: bool = true
  dur: duration = 1h30m
  l: list<int> = [1, -2]
  m: map<string, list<int>> = {"a": [1]}
  o: ?string = "x"
  t: Tier = critical
  ot: ?Tier = standard
  lt: list<Tier> = [standard, critical]
  mt: map<Tier, list<Tier>> = {critical: [standard]}
}

collect one
precedence d

default d(reason: x)
`},
		{name: "a reason named like an enum value", src: "kind K version 1\nenum Tier: critical | standard\ndecision page { reason: critical  tier: Tier = critical }\ncollect all\ndefault page(reason: critical)",
			want: "kind K version 1\n\nenum Tier: critical | standard\n\ndecision page {\n  reason: critical\n  tier: Tier = critical\n}\n\ncollect all\n\ndefault page(reason: critical)\n"},
		{name: "enums share a value", src: "kind K version 1\nenum Tier: standard | critical\nenum Plan: free | standard\ninput tier: Tier\ninput plan: Plan\ndecision d { reason: x }\ncollect all",
			want: "kind K version 1\n\nenum Tier: standard | critical\nenum Plan: free | standard\n\ninput tier: Tier\ninput plan: Plan\n\ndecision d {\n  reason: x\n}\n\ncollect all\n"},

		// File shape.
		{name: "empty file", src: "", errs: []string{"1:1: file has no kind document"}, help: "a kind file starts with `kind Name version N`"},
		{name: "two documents", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\n---\npolicy p: K",
			errs: []string{"7:1: a kind file holds exactly one document"}, help: "move the other documents to their own files"},
		{name: "policy instead of kind", src: "policy deploy.production: K@1\nlet a = 1", errs: []string{"1:1: expected a kind document, found policy `deploy.production`"}},
		{name: "module instead of kind", src: "module deploy.common: K@1", errs: []string{"1:1: expected a kind document, found module `deploy.common`"}},
		{name: "parse errors come alone", src: "kind K version 1\ntype T { a }\ndecision d()", errs: []string{"2:12: expected `:`, found `}`", "3:13: expected `{`, found end of file"}},

		// Header.
		{name: "accepts above the version", src: "kind K version 2, accepts: 3\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"1:28: kind K at version 2 can't accept version 3"}, help: "`accepts` names the oldest version policies may still pin, between 1 and the version"},
		{name: "accepts zero", src: "kind K version 1, accepts: 0\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"1:28: kind K accepts version 0, but versions start at 1"}, help: "leave `accepts` out to accept every version, or name the oldest version policies may still pin, between 1 and the version"},
		{name: "version zero", src: "kind K version 0\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"1:16: invalid kind version 0"}, help: "the version is a positive integer that changes when the contract does"},

		// Types.
		{name: "unknown type with suggestion", src: "kind K version 1\ntype Release { soak: duration }\ninput r: Relaese\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"3:10: unknown type `Relaese`"}, help: "did you mean `Release`?"},
		{name: "unknown type suggests a built-in", src: "kind K version 1\ninput r: strng\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:10: unknown type `strng`"}, help: "did you mean `string`?"},
		{name: "unknown type without suggestion", src: "kind K version 1\ninput r: list<Ticket>\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:15: unknown type `Ticket`"}, help: "declare it with `type Ticket { ... }`, or use a built-in type"},
		{name: "unknown type reported once per use", src: "kind K version 1\ntype R { a: T b: T }\ninput r: R\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:13: unknown type `T`", "2:18: unknown type `T`"}},
		{name: "type declared twice keeps both fields sets apart", src: "kind K version 1\ntype R { a: int }\ntype R { b: int }\ninput r: R\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"3:6: type \"R\" is declared twice"}, help: "give each type one declaration"},
		{name: "type shadows a built-in", src: "kind K version 1\ntype string { a: int }\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:6: type \"string\" shadows a built-in type"}},
		{name: "field declared twice", src: "kind K version 1\ntype R { a: int a: string }\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:17: type R: field \"a\" is declared twice"}},
		{name: "recursive type", src: "kind K version 1\ntype Node { next: ?Node }\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:6: type Node is recursive: Node -> Node"}},
		{name: "mutually recursive types", src: "kind K version 1\ntype A { b: B }\ntype B { a: list<A> }\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:6: type A is recursive: A -> B -> A", "3:6: type B is recursive: B -> A -> B"}},
		{name: "optional list", src: "kind K version 1\ninput tags: ?list<string>\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:13: input \"tags\": a list can't be optional"}},
		{name: "optional map", src: "kind K version 1\ninput labels: ?map<string, string>\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:15: input \"labels\": a map can't be optional"}},
		{name: "map with a list key", src: "kind K version 1\ninput m: map<list<string>, int>\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:14: list<string> can't be a map key"}},
		{name: "map with a struct key", src: "kind K version 1\ntype R { a: int }\ninput m: map<R, int>\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"3:14: R can't be a map key"}},
		{name: "map with an unknown key type", src: "kind K version 1\ninput m: map<Ticket, int>\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:14: unknown type `Ticket`"}},

		// Inputs and functions.
		{name: "input declared twice", src: "kind K version 1\ninput a: int\ninput a: string\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"3:7: input \"a\" collides with input \"a\""}},
		{name: "function collides with input", src: "kind K version 1\ninput split: int\nfn split(string) -> list<string>\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"3:4: function \"split\" collides with input \"split\""}},
		{name: "function with optional result", src: "kind K version 1\nfn f() -> ?string\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:11: function f: the result can't be optional"}},
		{name: "function parameter of unknown type", src: "kind K version 1\nfn f(int, Ticket) -> int\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:11: unknown type `Ticket`"}},

		// Enums. The model's rules find these; the loader points them at
		// the declaration.
		{name: "enum type with suggestion", src: "kind K version 1\nenum Tier: a | b\ninput t: Teir\ndecision d { reason: x }\ncollect all",
			errs: []string{"3:10: unknown type `Teir`"}, help: "did you mean `Tier`?"},
		{name: "enum declared twice", src: "kind K version 1\nenum Tier: a | b\nenum Tier: c\ndecision d { reason: x }\ncollect all",
			errs: []string{`3:6: type "Tier" is declared twice`}},
		{name: "enum named like a struct type", src: "kind K version 1\ntype Tier { a: int }\nenum Tier: a | b\ndecision d { reason: x }\ncollect all",
			errs: []string{`3:6: type "Tier" is declared twice`}},
		{name: "enum shadows a built-in", src: "kind K version 1\nenum string: a | b\ndecision d { reason: x }\ncollect all",
			errs: []string{`2:6: enum "string" shadows a built-in type`}},
		{name: "enum value declared twice", src: "kind K version 1\nenum Tier: a | b | a\ndecision d { reason: x }\ncollect all",
			errs: []string{`2:20: enum Tier: value "a" is declared twice`}},
		{name: "enum value collides with an input", src: "kind K version 1\nenum Tier: env | b\ninput env: string\ndecision d { reason: x }\ncollect all",
			errs: []string{`2:12: enum Tier: value "env" collides with input "env"`}, help: "inputs, host functions, decisions, enums and their values share one namespace; rename one of them"},
		{name: "enum named like an input", src: "kind K version 1\nenum Tier: a | b\ninput Tier: string\ndecision d { reason: x }\ncollect all",
			errs: []string{`2:6: enum "Tier" collides with input "Tier"`}, help: "inputs, host functions, decisions, enums and their values share one namespace; rename one of them"},
		{name: "enum value collides with a decision", src: "kind K version 1\nenum Tier: d | b\ndecision d { reason: x }\ncollect all",
			errs: []string{`2:12: enum Tier: value "d" collides with decision "d"`}},
		{name: "enum as a map value", src: "kind K version 1\nenum Tier: a | b\ninput m: map<string, Tier>\ndecision d { reason: x }\ncollect all",
			errs: []string{"3:10: input \"m\": map value type can't be enum Tier"}, help: "a missing key would read as the zero value, and an enum has none; key the map by the enum instead, or use a list"},
		{name: "enum payload default of another enum", src: "kind K version 1\nenum Tier: a | b\nenum Plan: c\ndecision d { reason: x  t: Tier = c }\ncollect all",
			errs: []string{"4:35: Tier has no value `c`"}, help: "did you mean `a`? Tier declares: a, b"},
		{name: "enum payload default as a string", src: "kind K version 1\nenum Tier: a | b\ndecision d { reason: x  t: Tier = \"a\" }\ncollect all",
			errs: []string{"3:35: expected Tier, found string"}, help: "an enum value is a bare name; write `a`"},

		// Decisions.
		{name: "old decision syntax", src: "kind K version 1\ndecision d(approvers: list<string>, bake: duration = 1h + 30m) { a x }\ncollect one\nprecedence d\ndefault d(reason: x, approvers: [])",
			errs: []string{"2:10: old decision syntax; write `decision d { reason: … approvers: … bake: … }`"}, help: "write `decision d { reason: a | x  approvers: list<string>  bake: duration = (1h + 30m) }`, or run `sigil fmt --write`"},
		{name: "old decision syntax without fields", src: "kind K version 1\ndecision deny {\n  no_rule_matched\n  soak_too_short\n}\ncollect all",
			errs: []string{"2:10: old decision syntax; write `decision deny { reason: … }`"}, help: "write `decision deny { reason: no_rule_matched | soak_too_short }`, or run `sigil fmt --write`"},
		{name: "decision without reasons", src: "kind K version 1\ndecision d { approvers: list<string> }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:12: decision d declares no reason"}},
		{name: "reason declared twice", src: "kind K version 1\ndecision d { reason: x | x }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{`2:26: decision d: reason "x" is declared twice`}},
		{name: "reason names an enum", src: "kind K version 1\nenum Reason: yes | no\ndecision d { reason: Reason }\ncollect all",
			errs: []string{"3:22: decision d: the reason can't name type Reason"}, help: "list the reasons inline, like `reason: yes | no`"},
		{name: "reason names a struct type", src: "kind K version 1\ntype R { a: int }\ndecision d { reason: R }\ncollect all",
			errs: []string{"3:22: decision d: the reason can't name type R"}, help: "list the reasons inline, like `reason: a | b`"},
		{name: "reason names a built-in type", src: "kind K version 1\ndecision d { reason: string }\ncollect all",
			errs: []string{"2:22: decision d: the reason can't name type string"}},
		{name: "reason named like a type among several", src: "kind K version 1\ndecision d { reason: string | int }\ncollect all",
			want: "kind K version 1\n\ndecision d {\n  reason: string | int\n}\n\ncollect all\n"},
		{name: "scoped precedence round-trips", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nprecedence d: y > x > a\ndefault d(reason: x)",
			want: "kind K version 1\n\ndecision d {\n  reason: a | x | y\n}\n\ncollect one\nprecedence d\nprecedence d: y > x > a\n\ndefault d(reason: x)\n"},
		{name: "scoped precedence on an undeclared decision", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nprecedence e: a > b\ndefault d(reason: x)",
			errs: []string{`5:12: precedence: undeclared decision "e"`}},
		{name: "scoped precedence twice", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nprecedence d: a > x > y\nprecedence d: y > x > a\ndefault d(reason: x)",
			errs: []string{"6:1: precedence d is declared twice"}},
		{name: "scoped precedence misses a reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nprecedence d: a > y\ndefault d(reason: x)",
			errs: []string{`5:1: precedence d: doesn't name reason "x"`}},
		{name: "exclusive round-trips", src: "kind K version 1\ndecision d { reason: a | x | y }\ndecision e { reason: z }\ncollect all\nexclusive d.a, e\nexclusive d, e",
			want: "kind K version 1\n\ndecision d {\n  reason: a | x | y\n}\n\ndecision e {\n  reason: z\n}\n\ncollect all\nexclusive d.a, e\nexclusive d, e\n"},
		{name: "exclusive names an undeclared reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nexclusive d.a, d.b\ndefault d(reason: x)",
			errs: []string{`5:16: exclusive: decision d has no reason "b"`}, help: "d declares: a, x, y"},
		{name: "exclusive names an undeclared decision", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nexclusive d, e\ndefault d(reason: x)",
			errs: []string{`5:14: exclusive: undeclared decision "e"`}},
		{name: "payload field declared twice", src: "kind K version 1\ndecision d { reason: a | x | y  a: int  a: int }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:41: decision d, field \"a\": declared twice", "5:1: default: field \"a\" is required and has no value"}},
		{name: "payload default of the wrong type", src: "kind K version 1\ndecision d { reason: a | x | y  bake: duration = 1 }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:50: expected duration, found int"}},
		{name: "payload default not constant", src: "kind K version 1\ninput a: int\ndecision d { reason: a | x | y  n: int = a + 1 }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"3:42: `a` isn't a constant"}},
		{name: "payload default overflows", src: "kind K version 1\ndecision d { reason: a | x | y  n: int = 9223372036854775807 + 1 }\ncollect one\nprecedence d\ndefault d(reason: x)",
			errs: []string{"2:42: integer overflow in constant"}},

		// Resolution.
		{name: "no collect", src: "kind K version 1\ndecision d { reason: a | x | y }\ndefault d(reason: x)",
			errs: []string{"1:6: kind K doesn't declare how many decisions it returns"}, help: "declare `collect one` with a `precedence`, or `collect all`"},
		{name: "precedence without collect", src: "kind K version 1\ndecision d { reason: a | x | y }\nprecedence d\ndefault d(reason: x)",
			errs: []string{"3:1: kind K has precedence but no collect"}, help: "declare `collect one` to return the highest-ranked decision"},
		{name: "collect one without precedence", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\ndefault d(reason: x)",
			errs: []string{"3:1: kind K collects one decision but has no precedence"}, help: "`collect one` returns the highest-ranked decision; rank them with `precedence deny > review > approve`, highest first"},
		{name: "collect all with precedence", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect all\nprecedence d",
			want: "kind K version 1\n\ndecision d {\n  reason: a | x | y\n}\n\ncollect all\nprecedence d\n"},
		{name: "collect one and collect all", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ncollect all\ndefault d(reason: x)",
			errs: []string{"5:1: collect is declared twice"}, help: "a kind declares `collect` once; remove one"},
		{name: "precedence twice", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nprecedence d\ndefault d(reason: x)",
			errs: []string{"5:1: precedence is declared twice"}, help: "a kind declares `precedence` once; remove one"},
		{name: "collect twice", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect all\ncollect all",
			errs: []string{"4:1: collect is declared twice"}},
		{name: "default twice", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\ndefault d(reason: y)",
			errs: []string{"6:1: default is declared twice"}},
		{name: "precedence misses a decision", src: "kind K version 1\ndecision a { reason: x }\ndecision b { reason: x }\ncollect one\nprecedence a\ndefault a(reason: x)",
			errs: []string{"5:1: precedence doesn't name decision \"b\""}, help: "list every decision exactly once, highest first"},
		{name: "precedence names a decision twice", src: "kind K version 1\ndecision a { reason: x }\ncollect one\nprecedence a > a\ndefault a(reason: x)",
			errs: []string{"4:16: precedence names \"a\" twice"}},
		{name: "precedence names an unknown decision", src: "kind K version 1\ndecision a { reason: x }\ncollect one\nprecedence a > b\ndefault a(reason: x)",
			errs: []string{"4:16: precedence names undeclared decision \"b\""}},
		{name: "no default", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d",
			errs: []string{"1:6: kind K has no default decision"}, help: "declare `default <decision>(reason: <reason>)` for the case where no rule fires"},
		{name: "default names an unknown decision", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault deny(reason: x, a: 1)",
			errs: []string{"5:1: default names undeclared decision \"deny\""}},
		{name: "default names an unknown decision without a reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault deny()",
			errs: []string{"5:1: default names undeclared decision \"deny\""}},
		{name: "default reason after the payload", src: "kind K version 1\ndecision d { reason: a | x  n: int }\ncollect one\nprecedence d\ndefault d(n: 1, reason: a)",
			want: "kind K version 1\n\ndecision d {\n  reason: a | x\n  n: int\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: a, n: 1)\n"},
		{name: "default without reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d()",
			errs: []string{"5:9: decision d needs a reason"}, help: "write `d(reason: <reason>)`; d declares: a, x, y"},
		{name: "default with a positional reason", src: "kind K version 1\ndecision d { reason: a | x | y  n: int = 0 }\ncollect one\nprecedence d\ndefault d(x, n: 2)",
			errs: []string{"5:11: the reason is a named argument"}, help: "write `d(reason: x, n: 2)`"},
		{name: "default with a positional string reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(`x`)",
			errs: []string{"5:11: the reason is a named argument"}, help: "write `d(reason: x)`"},
		{name: "default with a computed positional reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(1 + 2)",
			errs: []string{"5:11: the reason is a named argument"}, help: "write `d(reason: <reason>)`; d declares: a, x, y"},
		{name: "default with an undeclared reason", src: "kind K version 1\ninput r: string\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: r)",
			errs: []string{"6:19: decision d has no reason `r`"}, help: "did you mean `a`? d declares: a, x, y"},
		{name: "default with a string reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: \"x\")",
			errs: []string{"5:19: decision reason must be a bare name"}, help: "reasons are declared names, not strings; write `reason: x`"},
		{name: "default with a computed reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: [x])",
			errs: []string{"5:19: decision reason must be a bare name"}},
		{name: "default gives the reason twice", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x, reason: y)",
			errs: []string{"5:22: field \"reason\" is given twice"}},
		{name: "default with an unknown field", src: "kind K version 1\ndecision approve { reason: ok | x  bake: duration = 1h }\ncollect one\nprecedence approve\ndefault approve(reason: x, bak: 15m)",
			errs: []string{"5:28: decision approve has no payload field \"bak\""}, help: "did you mean \"bake\"? approve takes reason: ok | x, and bake: duration = 1h"},
		{name: "default with a wrong value", src: "kind K version 1\ndecision approve { reason: ok | x  bake: duration = 1h }\ncollect one\nprecedence approve\ndefault approve(reason: x, bake: \"15m\")",
			errs: []string{"5:34: expected duration, found string"}},
		{name: "default with an enum value", src: "kind K version 1\nenum Tier: a | b\ndecision d { reason: x  t: Tier  ts: list<Tier> = [] }\ncollect all\ndefault d(reason: x, t: b, ts: [a, b])",
			want: "kind K version 1\n\nenum Tier: a | b\n\ndecision d {\n  reason: x\n  t: Tier\n  ts: list<Tier> = []\n}\n\ncollect all\n\ndefault d(reason: x, t: b, ts: [a, b])\n"},
		{name: "default with a qualified reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: d.x)",
			errs: []string{"5:19: a reason is a bare name"}, help: "write `reason: x`"},
		{name: "default with a qualified enum value", src: "kind K version 1\nenum Tier: a | b\ndecision d { reason: x  t: Tier = Tier.a }\ncollect all\ndefault d(reason: x, t: Tier.b)",
			want: "kind K version 1\n\nenum Tier: a | b\n\ndecision d {\n  reason: x\n  t: Tier = a\n}\n\ncollect all\n\ndefault d(reason: x, t: b)\n"},
		{name: "default with a value of another enum", src: "kind K version 1\nenum Tier: a | b\ndecision d { reason: x  t: Tier }\ncollect all\ndefault d(reason: x, t: c)",
			errs: []string{"5:25: Tier has no value `c`"}},
		{name: "default gives a field twice", src: "kind K version 1\ndecision approve { reason: ok | x  bake: duration = 1h }\ncollect one\nprecedence approve\ndefault approve(reason: x, bake: 1h, bake: 2h)",
			errs: []string{"5:38: field \"bake\" is given twice"}},
		{name: "default misses a required field", src: "kind K version 1\ndecision review { reason: r | x  approvers: list<string> }\ncollect one\nprecedence review\ndefault review(reason: x)",
			errs: []string{"5:1: default: field \"approvers\" is required and has no value"}, help: "review takes reason: r | x, and approvers: list<string>"},

		// The conflict outcome is loaded like the default, and printed after it.
		{name: "conflict outcome round-trips", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\nconflict d(reason: y)\ndefault d(reason: x)",
			want: "kind K version 1\n\ndecision d {\n  reason: a | x | y\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x)\nconflict d(reason: y)\n"},
		{name: "conflict outcome before the decision it names", src: "kind K version 1\nconflict review(reason: y, approvers: [\"leads\"])\ndecision review { reason: x | y  approvers: list<string> }\ncollect one\nprecedence review\ndefault review(reason: x, approvers: [])",
			want: "kind K version 1\n\ndecision review {\n  reason: x | y\n  approvers: list<string>\n}\n\ncollect one\nprecedence review\n\ndefault review(reason: x, approvers: [])\nconflict review(reason: y, approvers: [\"leads\"])\n"},
		{name: "a type field may be called conflict", src: "kind K version 1\ntype R { conflict: int }\ninput r: R\ndecision d { reason: x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict d(reason: y)",
			want: "kind K version 1\n\ntype R {\n  conflict: int\n}\n\ninput r: R\n\ndecision d {\n  reason: x | y\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x)\nconflict d(reason: y)\n"},
		{name: "conflict twice", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict d(reason: y)\nconflict d(reason: a)",
			errs: []string{"7:1: conflict is declared twice"}, help: "a kind declares `conflict` once; remove one"},
		{name: "conflict on a collecting kind", src: "kind K version 1\ndecision read { reason: everyone | denied }\ncollect all\nconflict read(reason: denied)",
			errs: []string{"4:1: kind K collects all decisions and can't declare a conflict outcome"},
			help: "a collecting kind returns an empty outcome on a conflict, because granting anything on a defect in the policy would fail open; remove `conflict`"},
		{name: "conflict names an unknown decision", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict deny(reason: x)",
			errs: []string{"6:1: conflict names undeclared decision \"deny\""}, help: "the conflict outcome constructs one of the kind's decisions"},
		{name: "conflict without reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict d()",
			errs: []string{"6:10: decision d needs a reason"}, help: "write `d(reason: <reason>)`; d declares: a, x, y"},
		{name: "conflict with a positional reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict d(y)",
			errs: []string{"6:12: the reason is a named argument"}, help: "write `d(reason: y)`"},
		{name: "conflict with an undeclared reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict d(reason: conflicting_rules)",
			errs: []string{"6:20: decision d has no reason `conflicting_rules`"}, help: "d declares: a, x, y"},
		{name: "conflict with a string reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict d(reason: \"y\")",
			errs: []string{"6:20: decision reason must be a bare name"}, help: "reasons are declared names, not strings; write `reason: y`"},
		{name: "conflict with a computed reason", src: "kind K version 1\ndecision d { reason: a | x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\nconflict d(reason: 1 + 1)",
			errs: []string{"6:20: decision reason must be a bare name"}},
		{name: "conflict with an unknown field", src: "kind K version 1\ndecision approve { reason: ok | x  bake: duration = 1h }\ncollect one\nprecedence approve\ndefault approve(reason: x)\nconflict approve(reason: ok, bak: 15m)",
			errs: []string{"6:30: decision approve has no payload field \"bak\""}, help: "did you mean \"bake\"? approve takes reason: ok | x, and bake: duration = 1h"},
		{name: "conflict with a wrong value", src: "kind K version 1\ndecision approve { reason: ok | x  bake: duration = 1h }\ncollect one\nprecedence approve\ndefault approve(reason: x)\nconflict approve(reason: ok, bake: \"15m\")",
			errs: []string{"6:36: expected duration, found string"}},
		{name: "conflict gives a field twice", src: "kind K version 1\ndecision approve { reason: ok | x  bake: duration = 1h }\ncollect one\nprecedence approve\ndefault approve(reason: x)\nconflict approve(reason: ok, bake: 1h, bake: 2h)",
			errs: []string{"6:40: field \"bake\" is given twice"}},
		{name: "conflict misses a required field", src: "kind K version 1\ndecision review { reason: x | y  approvers: list<string> }\ncollect one\nprecedence review\ndefault review(reason: x, approvers: [])\nconflict review(reason: y)",
			errs: []string{"6:1: conflict: field \"approvers\" is required and has no value"}, help: "review takes reason: x | y, and approvers: list<string>"},
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
		first, _, _ := strings.Cut(string(src), "\n---\n")
		if _, errs := check.LoadKind(name+".sigil", []byte(first)); errs != nil {
			t.Errorf("%s: %v", name, errs)
		}
	}
}
