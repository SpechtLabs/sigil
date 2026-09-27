package check_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/parser"
)

// checkDoc parses src as one document and checks it against the test
// kind, returning the checker.
func checkDoc(t *testing.T, src string) (*check.Checker, *ast.File) {
	t.Helper()
	f, perrs := parser.ParseFile("p.sigil", []byte(src))
	if perrs != nil {
		t.Fatalf("parse: %v", perrs)
	}
	if len(f.Docs) != 1 {
		t.Fatalf("%d documents, want 1", len(f.Docs))
	}
	c := check.New("p.sigil")
	k := loadKind(t)
	switch d := f.Docs[0].(type) {
	case *ast.PolicyDoc:
		c.Policy(d, k)
	case *ast.ModuleDoc:
		c.Module(d, k)
	default:
		t.Fatalf("document is a %T", d)
	}
	return c, f
}

func TestPolicy(t *testing.T) {
	tests := []struct {
		name string
		src  string
		errs []string // "line:col: msg"; empty means clean
		help string   // of the first error
		lets string   // Info.Lets names in order, when relevant
	}{
		{name: "the guardrails policy", src: `policy deploy.guardrails: Test
param min_soak: duration = 24h
let eligible = "deployer" in actor.roles and environment == "production"
when not eligible {
  deny("not_eligible")
}
when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}`},
		{name: "the production policy", src: `policy deploy.production: Test
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
let owns_service = actor.teams any in service.owners
let cleared = split(service.labels["regions"], ",") all in actor.regions
when cleared {
  when service.tier == "critical" and "release_manager" in actor.roles {
    approve("release_manager")
  }
  when service.tier in tiers and owns_service {
    review("service_owner", approvers: approvers)
  }
}`},
		{name: "asserts read outcome and decisions", src: `policy access.guardrails: Test
assert [deny, approve] exclusive in outcome, "one_of"
when true {
  assert deny not in outcome or actor.name != "", "named"
  assert release.soak >= 0s, "soak"
}`},
		{name: "lets in any order", src: `policy p: Test
let a = b and c
let c = release.hotfix
let b = not c
when a { deny("x") }`, lets: "c b a"},
		{name: "let reads a param", src: `policy p: Test
param min_soak: duration
let short = release.soak < min_soak
when short { deny("x") }`, lets: "short"},
		{name: "payload with defaults left out", src: "policy p: Test\nwhen true { approve(\"ok\") }"},
		{name: "payload keyword field name", src: "policy p: Test\nwhen true { review(\"r\", approvers: [\"a\"]) }"},
		{name: "empty list argument typed by the field", src: "policy p: Test\nwhen true { review(\"r\", approvers: []) }"},
		{name: "several constructors in one body", src: "policy p: Test\nwhen true { deny(\"a\") deny(\"b\") review(\"c\", approvers: []) }"},
		{name: "module of lets", src: "module m: Test\nlet a = release.hotfix\nlet b = a and environment == \"x\"", lets: "a b"},

		// Kind and imports.
		{name: "wrong kind", src: "policy p: Other\nlet a = 1", errs: []string{"1:11: document is for kind Other, not Test"}, help: "this document can only be checked against kind Other"},
		{name: "use is not supported yet", src: "policy p: Test\nuse deploy.common\nlet a = 1", errs: []string{"2:1: `use` isn't supported yet"}},

		// Params.
		{name: "param collides with input", src: "policy p: Test\nparam release: int", errs: []string{"2:7: `release` is already the name of an input"}, help: "every name in a document means one thing; rename one of them"},
		{name: "param collides with function", src: "policy p: Test\nparam split: int", errs: []string{"2:7: `split` is already the name of a host function"}},
		{name: "param collides with decision", src: "policy p: Test\nparam deny: int", errs: []string{"2:7: `deny` is already the name of a decision"}},
		{name: "param declared twice", src: "policy p: Test\nparam a: int\nparam a: string", errs: []string{"3:7: `a` is already the name of a param"}},
		{name: "param of unknown type", src: "policy p: Test\nparam a: Servce", errs: []string{"2:10: unknown type `Servce`"}, help: "did you mean `Service`?"},
		{name: "param of list of unknown type", src: "policy p: Test\nparam a: list<Ticket>", errs: []string{"2:15: unknown type `Ticket`"}, help: "types are the built-ins and the struct types the kind declares"},
		{name: "param with a list key", src: "policy p: Test\nparam a: map<list<string>, int>", errs: []string{"2:14: list<string> can't be a map key"}},
		{name: "optional param", src: "policy p: Test\nparam a: ?string", errs: []string{"2:10: a param can't be optional"}, help: "an optional param is a param with a default; write `param a: string = <default>`"},
		{name: "param default of the wrong type", src: "policy p: Test\nparam a: duration = 24", errs: []string{"2:21: expected duration, found int"}},
		{name: "param default not constant", src: "policy p: Test\nparam a: string = environment", errs: []string{"2:19: `environment` isn't a constant"}},
		{name: "param default typed list", src: "policy p: Test\nparam a: list<string> = [1]", errs: []string{"2:26: expected string, found int"}},

		// Lets.
		{name: "let collides with input", src: "policy p: Test\nlet actor = 1", errs: []string{"2:5: `actor` is already the name of an input"}},
		{name: "let collides with param", src: "policy p: Test\nparam a: int\nlet a = 1", errs: []string{"3:5: `a` is already the name of a param"}},
		{name: "let declared twice", src: "policy p: Test\nlet a = 1\nlet a = 2", errs: []string{"3:5: `a` is already the name of a let"}},
		{name: "let depends on itself", src: "policy p: Test\nlet a = a", errs: []string{"2:9: let `a` depends on itself"}, help: "lets form a directed acyclic graph; a let can't depend on itself, even through other lets"},
		{name: "lets in a cycle", src: "policy p: Test\nlet a = b\nlet b = c\nlet c = a", errs: []string{"4:9: let `a` depends on itself"}},
		{name: "let with an empty list", src: "policy p: Test\nlet nothing = []", errs: []string{"2:15: cannot infer the type of `[]`"}},
		{name: "let reading outcome", src: "policy p: Test\nlet won = deny in outcome", errs: []string{"2:11: `deny` is a decision, not a value here", "2:19: `outcome` can only be read in an assert condition"}},
		{name: "let type error", src: "policy p: Test\nlet a = service.teir == \"x\"", errs: []string{"2:17: unknown field \"teir\" on type Service"}, help: "did you mean \"tier\"? Service declares: name, tier, owners, labels, owner, scores, counts, by_id"},
		{name: "let error reported once through dependents", src: "policy p: Test\nlet a = servce.tier\nlet b = a == \"x\"\nlet c = b and true", errs: []string{"2:9: unknown name `servce`"}},

		// Rules and asserts.
		{name: "when condition not bool", src: "policy p: Test\nwhen actor.roles { deny(\"x\") }", errs: []string{"2:6: expected bool, found list<string>"}},
		{name: "nested when condition", src: "policy p: Test\nwhen true { when 1 { deny(\"x\") } }", errs: []string{"2:18: expected bool, found int"}},
		{name: "assert condition not bool", src: "policy p: Test\nassert environment, \"r\"", errs: []string{"2:8: expected bool, found string"}},
		{name: "assert empty reason", src: "policy p: Test\nassert true, \"\"", errs: []string{"2:14: an assert's reason can't be empty"}},
		{name: "outcome in a when", src: "policy p: Test\nwhen deny in outcome { deny(\"x\") }", errs: []string{"2:6: `deny` is a decision, not a value here", "2:14: `outcome` can only be read in an assert condition"}},
		{name: "quantifier variable collides with let", src: "policy p: Test\nlet r = 1\nwhen any r in actor.roles: true { deny(\"x\") }", errs: []string{"3:10: `r` is already the name of a let"}},

		// Constructors.
		{name: "constructor at top level", src: "policy p: Test\ndeny(\"x\")", errs: []string{"2:1: decision constructors go inside a `when` block"}, help: "a constructor here would fire for every input; wrap it in `when true { deny(...) }` if that's what you mean"},
		{name: "top-level constructor is still checked", src: "policy p: Test\nreview(\"x\")", errs: []string{"2:1: decision constructors go inside a `when` block", "2:1: decision review needs field \"approvers\""}},
		{name: "unknown decision", src: "policy p: Test\nwhen true { denny(\"x\") }", errs: []string{"2:13: unknown decision `denny`"}, help: "did you mean `deny`?"},
		{name: "unknown decision without suggestion", src: "policy p: Test\nwhen true { escalate(\"x\") }", errs: []string{"2:13: unknown decision `escalate`"}, help: "constructors name one of the kind's decisions; invocations name an imported policy"},
		{name: "calling an input", src: "policy p: Test\nwhen true { actor(\"x\") }", errs: []string{"2:13: `actor` is an input, not a decision"}, help: "only the kind's decisions can be constructed, and only imported policies invoked"},
		{name: "calling a let", src: "policy p: Test\nlet a = 1\nwhen true { a(\"x\") }", errs: []string{"3:13: `a` is a let, not a decision"}},
		{name: "invoking at top level", src: "policy p: Test\nguardrails(min_soak: 4h)", errs: []string{"2:1: unknown decision `guardrails`"}},
		{name: "reason missing", src: "policy p: Test\nwhen true { deny() }", errs: []string{"2:13: decision deny needs a reason"}, help: "write `deny(\"<reason>\")`; deny is declared as: decision deny(reason: string)"},
		{name: "reason computed", src: "policy p: Test\nwhen true { deny(service.tier) }", errs: []string{"2:18: decision reason must be a string literal"}, help: "put dynamic text in a `detail` field; declare `detail: string = \"\"` on decision deny in the kind"},
		{name: "reason raw", src: "policy p: Test\nwhen true { deny(`x`) }", errs: []string{"2:18: decision reason must be a double-quoted string"}},
		{name: "reason empty", src: "policy p: Test\nwhen true { deny(\"\") }", errs: []string{"2:18: decision reason can't be empty"}},
		{name: "reason as named argument", src: "policy p: Test\nwhen true { deny(reason: \"x\") }", errs: []string{"2:13: decision deny needs a reason", "2:18: decision deny has no payload field \"reason\""}},
		{name: "unknown payload field", src: "policy p: Test\nwhen true { approve(\"x\", bak: 15m) }", errs: []string{"2:26: decision approve has no payload field \"bak\""}, help: "did you mean \"bake\"? approve is declared as: decision approve(reason: string, bake: duration = 1h)"},
		{name: "unknown payload field value still checked", src: "policy p: Test\nwhen true { approve(\"x\", bak: servce) }", errs: []string{"2:26: decision approve has no payload field \"bak\"", "2:31: unknown name `servce`"}},
		{name: "payload field twice", src: "policy p: Test\nwhen true { approve(\"x\", bake: 1h, bake: 2h) }", errs: []string{"2:36: field \"bake\" is given twice"}},
		{name: "payload field of the wrong type", src: "policy p: Test\nwhen true { approve(\"x\", bake: 15) }", errs: []string{"2:32: expected duration, found int"}, help: "a bare number is never a duration; write a literal like `30m`"},
		{name: "payload list element of the wrong type", src: "policy p: Test\nwhen true { review(\"x\", approvers: [\"a\", 1]) }", errs: []string{"2:42: expected string, found int"}},
		{name: "required payload field missing", src: "policy p: Test\nwhen true { review(\"x\") }", errs: []string{"2:13: decision review needs field \"approvers\""}, help: "review is declared as: decision review(reason: string, approvers: list<string>)"},
		{name: "payload reads the input", src: "policy p: Test\nwhen true { review(\"x\", approvers: service.owners) }"},

		// Modules.
		{name: "module wrong kind", src: "module m: Other\nlet a = 1", errs: []string{"1:11: document is for kind Other, not Test"}},
		{name: "module let cycle", src: "module m: Test\nlet a = b\nlet b = a", errs: []string{"3:9: let `a` depends on itself"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := checkDoc(t, tt.src)
			errs := c.Errors()
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
			if tt.lets != "" {
				names := make([]string, len(c.Info().Lets))
				for i, l := range c.Info().Lets {
					names[i] = l.Name.Name
				}
				if got := strings.Join(names, " "); got != tt.lets {
					t.Errorf("Info.Lets = %q, want %q", got, tt.lets)
				}
			}
		})
	}
}

// TestInfoConstructors checks that every constructor is tied to its
// decision, which is what the evaluator builds candidates from.
func TestInfoConstructors(t *testing.T) {
	c, f := checkDoc(t, "policy p: Test\nwhen true {\n  deny(\"a\")\n  when release.hotfix { approve(\"b\", bake: 15m) }\n}")
	if errs := c.Errors(); errs != nil {
		t.Fatal(errs)
	}
	doc := f.Docs[0].(*ast.PolicyDoc)
	outer := doc.Stmts[0].(*ast.WhenStmt)
	deny := outer.Body[0].(*ast.CallStmt)
	approve := outer.Body[1].(*ast.WhenStmt).Body[0].(*ast.CallStmt)
	if d := c.Info().Constructors[deny]; d == nil || d.Name != "deny" {
		t.Errorf("Constructors[deny] = %v", d)
	}
	if d := c.Info().Constructors[approve]; d == nil || d.Name != "approve" {
		t.Errorf("Constructors[approve] = %v", d)
	}
	if got := c.Info().TypeOf(approve.Args[0].Value); got == nil || got.String() != "duration" {
		t.Errorf("payload argument type = %v", got)
	}
}
