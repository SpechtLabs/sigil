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
		{name: "the guardrails policy", src: `policy deploy.guardrails: Test@1
param min_soak: duration = 24h
let eligible = "deployer" in actor.roles and environment == "production"
when not eligible {
  deny(reason: not_eligible)
}
when release.soak < min_soak and not release.hotfix {
  deny(reason: soak_too_short)
}`},
		{name: "the production policy", src: `policy deploy.production: Test@1
param approvers: list<string>
param tiers: list<Tier> = [standard, internal]
let owns_service = actor.teams any in service.owners
let cleared = split(service.labels["regions"], ",") all in actor.regions
when cleared {
  when service.tier == critical and "release_manager" in actor.roles {
    approve(reason: release_manager)
  }
  when service.tier in tiers and owns_service {
    review(reason: service_owner, approvers: approvers)
  }
}`},
		{name: "asserts read outcome and decisions", src: `policy access.guardrails: Test@1
assert("one_of", [deny, approve] exclusive in outcome)
when true {
  assert("named", deny not in outcome or actor.name != "")
  assert("soak", release.soak >= 0s)
}`},
		{name: "lets in any order", src: `policy p: Test@1
let a = b and c
let c = release.hotfix
let b = not c
when a { deny(reason: x) }`, lets: "c b a"},
		{name: "let reads a param", src: `policy p: Test@1
param min_soak: duration
let short = release.soak < min_soak
when short { deny(reason: x) }`, lets: "short"},
		{name: "scoped let in any order", src: `policy p: Test@1
when release.hotfix {
  when sre and environment == "production" { approve(reason: sre_hotfix) }
  let sre = any r in actor.roles: r like "sre-*"
}`, lets: "sre"},
		{name: "scoped lets read outer lets", src: `policy p: Test@1
let prod = environment == "production"
when true {
  let a = prod and release.hotfix
  when a {
    let b = a and not prod
    when b { deny(reason: x) }
  }
}`, lets: "prod a b"},
		{name: "pub let", src: "policy p: Test@1\npub let a = release.hotfix\nwhen a { deny(reason: x) }", lets: "a"},
		{name: "pub let in a module", src: "module m: Test@1\npub let a = release.hotfix\nlet b = a", lets: "a b"},
		{name: "payload with defaults left out", src: "policy p: Test@1\nwhen true { approve(reason: ok) }"},
		{name: "payload keyword field name", src: "policy p: Test@1\nwhen true { review(reason: r, approvers: [\"a\"]) }"},
		{name: "empty list argument typed by the field", src: "policy p: Test@1\nwhen true { review(reason: r, approvers: []) }"},
		{name: "several constructors in one body", src: "policy p: Test@1\nwhen true { deny(reason: a) deny(reason: b) review(reason: c, approvers: []) }"},
		{name: "module of lets", src: "module m: Test@1\nlet a = release.hotfix\nlet b = a and environment == \"x\"", lets: "a b"},

		// Kind and imports.
		{name: "wrong kind", src: "policy p: Other@1\nlet a = 1", errs: []string{"1:11: document is for kind Other, not Test"}, help: "this document can only be checked against kind Other"},
		{name: "unknown document", src: "policy p: Test@1\nuse deploy.common\nlet a = 1", errs: []string{"2:5: unknown document `deploy.common`"}, help: "`use` names a policy or module by the name in its header, wherever its file is"},

		// Params.
		{name: "param collides with input", src: "policy p: Test@1\nparam release: int", errs: []string{"2:7: `release` is already the name of an input"}, help: "every name in a document means one thing; rename one of them"},
		{name: "param collides with function", src: "policy p: Test@1\nparam split: int", errs: []string{"2:7: `split` is already the name of a host function"}},
		{name: "param collides with decision", src: "policy p: Test@1\nparam deny: int", errs: []string{"2:7: `deny` is already the name of a decision"}},
		{name: "param declared twice", src: "policy p: Test@1\nparam a: int\nparam a: string", errs: []string{"3:7: `a` is already the name of a param"}},
		{name: "param of unknown type", src: "policy p: Test@1\nparam a: Servce", errs: []string{"2:10: unknown type `Servce`"}, help: "did you mean `Service`?"},
		{name: "param of list of unknown type", src: "policy p: Test@1\nparam a: list<Ticket>", errs: []string{"2:15: unknown type `Ticket`"}, help: "types are the built-ins and the struct types and enums the kind declares"},
		{name: "param with a list key", src: "policy p: Test@1\nparam a: map<list<string>, int>", errs: []string{"2:14: list<string> can't be a map key"}},
		{name: "optional param", src: "policy p: Test@1\nparam a: ?string", errs: []string{"2:10: a param can't be optional"}, help: "an optional param is a param with a default; write `param a: string = <default>`"},
		{name: "param default of the wrong type", src: "policy p: Test@1\nparam a: duration = 24", errs: []string{"2:21: expected duration, found int"}},
		{name: "param default not constant", src: "policy p: Test@1\nparam a: string = environment", errs: []string{"2:19: `environment` isn't a constant"}},
		{name: "param bounds", src: "policy p: Test@1\nparam m: duration = 24h, min: 1h, max: 48h\nparam n: int, max: 5\nparam r: float = 0.5, min: 0.0, max: 1.0"},
		{name: "param bounds on a string", src: "policy p: Test@1\nparam s: string = \"a\", min: \"a\"", errs: []string{"2:29: a param of type string can't have bounds"}, help: "bounds apply to int, float and duration params"},
		{name: "param bound of the wrong type", src: "policy p: Test@1\nparam n: int, min: 1h", errs: []string{"2:20: expected int, found duration"}},
		{name: "param bound not constant", src: "policy p: Test@1\nparam n: int, max: count", errs: []string{"2:20: `count` isn't a constant"}},
		{name: "param max below min", src: "policy p: Test@1\nparam n: int, min: 5, max: 1", errs: []string{"2:28: max 1 is below min 5"}},
		{name: "param default below min", src: "policy p: Test@1\nparam m: duration = 30m, min: 1h", errs: []string{"2:21: default 30m is below the minimum 1h"}, help: "a default has to be a value the param accepts"},
		{name: "param default above max", src: "policy p: Test@1\nparam m: duration = 72h, min: 1h, max: 48h", errs: []string{"2:21: default 72h is above the maximum 48h"}},
		{name: "param default typed list", src: "policy p: Test@1\nparam a: list<string> = [1]", errs: []string{"2:26: expected string, found int"}},

		// Lets.
		{name: "let collides with input", src: "policy p: Test@1\nlet actor = 1", errs: []string{"2:5: `actor` is already the name of an input"}},
		{name: "let collides with param", src: "policy p: Test@1\nparam a: int\nlet a = 1", errs: []string{"3:5: `a` is already the name of a param"}},
		{name: "let declared twice", src: "policy p: Test@1\nlet a = 1\nlet a = 2", errs: []string{"3:5: `a` is already the name of a let"}},
		{name: "let depends on itself", src: "policy p: Test@1\nlet a = a", errs: []string{"2:9: let `a` depends on itself"}, help: "lets form a directed acyclic graph; a let can't depend on itself, even through other lets"},
		{name: "lets in a cycle", src: "policy p: Test@1\nlet a = b\nlet b = c\nlet c = a", errs: []string{"4:9: let `a` depends on itself"}},
		{name: "let with an empty list", src: "policy p: Test@1\nlet nothing = []", errs: []string{"2:15: cannot infer the type of `[]`"}},
		{name: "let reading outcome", src: "policy p: Test@1\nlet won = deny in outcome", errs: []string{"2:11: `deny` is a decision, not a value here", "2:19: `outcome` can only be read in an assert condition"}},
		{name: "let type error", src: "policy p: Test@1\nlet a = service.teir == \"x\"", errs: []string{"2:17: unknown field \"teir\" on type Service"}, help: "did you mean \"tier\"? Service declares: name, tier, plan, owners, labels, owner, scores, counts, by_id, allowed, by_tier"},
		{name: "let error reported once through dependents", src: "policy p: Test@1\nlet a = servce.tier\nlet b = a == \"x\"\nlet c = b and true", errs: []string{"2:9: unknown name `servce`"}},

		// Scoped lets.
		{name: "scoped let not visible in its condition", src: "policy p: Test@1\nwhen s { let s = true deny(reason: x) }", errs: []string{"2:6: unknown name `s`"}},
		{name: "scoped let not visible after its body", src: "policy p: Test@1\nwhen true { let s = true }\nwhen s { deny(reason: x) }", errs: []string{"3:6: unknown name `s`"}},
		{name: "scoped let named like one in another body", src: "policy p: Test@1\nwhen true { let s = true when s { deny(reason: a) } }\nwhen false { let s = false when s { deny(reason: b) } }",
			errs: []string{"3:18: `s` is already the name of a let in another `when` body"}, help: "let names are unique in a document, so a trace can name each one; rename one of them"},
		{name: "scoped let shadows a top-level let", src: "policy p: Test@1\nlet s = true\nwhen s { let s = false }", errs: []string{"3:14: `s` is already the name of a let"}},
		{name: "scoped let collides with a later top-level let", src: "policy p: Test@1\nwhen true { let s = false }\nlet s = true", errs: []string{"2:17: `s` is already the name of a let"}},
		{name: "scoped let shadows an outer scoped let", src: "policy p: Test@1\nwhen true { let s = true when s { let s = false } }", errs: []string{"2:39: `s` is already the name of a let"}},
		{name: "scoped let collides with an input", src: "policy p: Test@1\nwhen true { let actor = 1 }", errs: []string{"2:17: `actor` is already the name of an input"}},
		{name: "scoped lets in a cycle", src: "policy p: Test@1\nwhen true { let a = b let b = a }", errs: []string{"2:31: let `a` depends on itself"}},

		// Pub lets.
		{name: "pub let reads a param", src: "policy p: Test@1\nparam min_soak: duration\npub let short = release.soak < min_soak",
			errs: []string{"3:9: let `short` can't be `pub`: it reads param `min_soak`"}, help: "a param has no value outside an invocation; move the let to a module, or drop `pub`"},
		{name: "pub let reads a param through a let", src: "policy p: Test@1\nparam m: duration\npub let also = short and true\nlet short = release.soak < m",
			errs: []string{"3:9: let `also` can't be `pub`: it reads param `m`"}},

		// Rules and asserts.
		{name: "when condition not bool", src: "policy p: Test@1\nwhen actor.roles { deny(reason: x) }", errs: []string{"2:6: expected bool, found list<string>"}},
		{name: "nested when condition", src: "policy p: Test@1\nwhen true { when 1 { deny(reason: x) } }", errs: []string{"2:18: expected bool, found int"}},
		{name: "assert condition not bool", src: "policy p: Test@1\nassert(\"r\", environment)", errs: []string{"2:13: expected bool, found string"}},
		{name: "assert empty reason", src: "policy p: Test@1\nassert(\"\", true)", errs: []string{"2:8: an assert's reason can't be empty"}},
		{name: "outcome in a when", src: "policy p: Test@1\nwhen deny in outcome { deny(reason: x) }", errs: []string{"2:6: `deny` is a decision, not a value here", "2:14: `outcome` can only be read in an assert condition"}},
		{name: "quantifier variable collides with let", src: "policy p: Test@1\nlet r = 1\nwhen any r in actor.roles: true { deny(reason: x) }", errs: []string{"3:10: `r` is already the name of a let"}},
		{name: "filter variable collides with let", src: "policy p: Test@1\nlet r = 1\nlet xs = filter r in actor.roles: true", errs: []string{"3:17: `r` is already the name of a let"}},

		// Constructors.
		{name: "constructor at top level", src: "policy p: Test@1\ndeny(reason: x)", errs: []string{"2:1: decision constructors go inside a `when` block"}, help: "a constructor here would fire for every input; wrap it in `when true { deny(...) }` if that's what you mean"},
		{name: "top-level constructor is still checked", src: "policy p: Test@1\nreview(reason: x)", errs: []string{"2:1: decision constructors go inside a `when` block", "2:1: decision review needs field \"approvers\""}},
		{name: "unknown decision", src: "policy p: Test@1\nwhen true { denny(reason: x) }", errs: []string{"2:13: unknown decision `denny`"}, help: "did you mean `deny`?"},
		{name: "unknown decision without suggestion", src: "policy p: Test@1\nwhen true { page(reason: x) }", errs: []string{"2:13: unknown decision `page`"}, help: "constructors name one of the kind's decisions; invocations name an imported policy"},
		{name: "calling an input", src: "policy p: Test@1\nwhen true { actor(reason: x) }", errs: []string{"2:13: `actor` is an input, not a decision"}, help: "only the kind's decisions can be constructed, and only imported policies invoked"},
		{name: "calling a let", src: "policy p: Test@1\nlet a = 1\nwhen true { a(reason: x) }", errs: []string{"3:13: `a` is a let, not a decision"}},
		{name: "calling an enum value", src: "policy p: Test@1\nwhen true { critical(reason: x) }", errs: []string{"2:13: `critical` is an enum value, not a decision"}},
		{name: "invoking at top level", src: "policy p: Test@1\nguardrails(min_soak: 4h)", errs: []string{"2:1: unknown decision `guardrails`"}},
		{name: "reason after the payload", src: "policy p: Test@1\nwhen true { approve(bake: 2h, reason: release_manager) }"},
		{name: "reason between payload fields", src: "policy p: Test@1\nwhen true { escalate(tier: internal, reason: paged, plans: [standard]) }"},
		{name: "reason named like an enum value", src: "policy p: Test@1\nwhen true { escalate(reason: critical, tier: critical) }"},
		{name: "reason missing", src: "policy p: Test@1\nwhen true { deny() }", errs: []string{"2:13: decision deny needs a reason"}, help: "write `deny(reason: <reason>)`; deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "reason missing among payload fields", src: "policy p: Test@1\nwhen true { approve(bake: 2h) }", errs: []string{"2:13: decision approve needs a reason"}, help: "write `approve(reason: <reason>)`; approve declares: ok, x, b, release_manager, sre_hotfix"},
		{name: "reason computed", src: "policy p: Test@1\nwhen true { deny(reason: service.name) }", errs: []string{"2:26: decision reason must be a bare name"}, help: "a reason is one of the names the kind declares; put dynamic text in a `detail` field. deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "reason computed from an unknown name", src: "policy p: Test@1\nwhen true { deny(reason: servce.name) }", errs: []string{"2:26: decision reason must be a bare name", "2:26: unknown name `servce`"}},
		{name: "reason as a string", src: "policy p: Test@1\nwhen true { deny(reason: \"x\") }", errs: []string{"2:26: decision reason must be a bare name"}, help: "reasons are declared names, not strings; write `reason: x`"},
		{name: "reason as an unknown string", src: "policy p: Test@1\nwhen true { deny(reason: \"nope\") }", errs: []string{"2:26: decision reason must be a bare name"}, help: "reasons are declared names, not strings; deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "reason undeclared", src: "policy p: Test@1\nwhen true { deny(reason: soak_to_short) }", errs: []string{"2:26: decision deny has no reason `soak_to_short`"}, help: "did you mean `soak_too_short`? deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "reason of another decision", src: "policy p: Test@1\nwhen true { deny(reason: paged) }", errs: []string{"2:26: decision deny has no reason `paged`"}, help: "deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "reason given twice", src: "policy p: Test@1\nwhen true { deny(reason: x, reason: a) }", errs: []string{"2:29: field \"reason\" is given twice"}, help: "a constructor names one reason"},
		{name: "reason isn't resolved as a name", src: "policy p: Test@1\nlet x = 1\nwhen true { deny(reason: x) }"},
		{name: "reason label misspelled", src: "policy p: Test@1\nwhen true { deny(reasn: x) }", errs: []string{"2:13: decision deny needs a reason", "2:18: decision deny has no payload field \"reasn\"", "2:25: unknown name `x`"}, help: "write `deny(reason: <reason>)`; deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "positional reason", src: "policy p: Test@1\nwhen true { deny(soak_too_short) }", errs: []string{"2:18: the reason is a named argument"}, help: "write `deny(reason: soak_too_short)`"},
		{name: "positional reason with payload", src: "policy p: Test@1\nwhen true { approve(release_manager, bake: 2h) }", errs: []string{"2:21: the reason is a named argument"}, help: "write `approve(reason: release_manager, bake: 2h)`"},
		{name: "positional reason misspelled", src: "policy p: Test@1\nwhen true { deny(soak_to_short) }", errs: []string{"2:18: the reason is a named argument"}, help: "write `deny(reason: soak_too_short)`"},
		{name: "positional reason unknown", src: "policy p: Test@1\nwhen true { deny(nope) }", errs: []string{"2:18: the reason is a named argument"}, help: "write `deny(reason: <reason>)`; deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "positional reason as a string", src: "policy p: Test@1\nwhen true { review(\"x\", approvers: [\"a\", \"b\"]) }", errs: []string{"2:20: the reason is a named argument"}, help: "write `review(reason: x, approvers: [\"a\", \"b\"])`"},
		{name: "positional reason computed", src: "policy p: Test@1\nwhen true { deny(servce.name) }", errs: []string{"2:18: the reason is a named argument", "2:18: unknown name `servce`"}, help: "write `deny(reason: <reason>)`; deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "positional reason next to a labeled one", src: "policy p: Test@1\nwhen true { deny(x, reason: a) }", errs: []string{"2:18: the reason is a named argument"}, help: "remove `x`; the call already names `reason: a`"},
		{name: "qualified decision value in an assert", src: "policy p: Test@1\nassert(\"r\", approve.release_manager in outcome and [deny.x, approve] exclusive in outcome)"},
		{name: "qualified decision value with an unknown reason", src: "policy p: Test@1\nassert(\"r\", approve.lgtm in outcome)", errs: []string{"2:21: decision approve has no reason `lgtm`"}, help: "approve declares: ok, x, b, release_manager, sre_hotfix"},
		{name: "qualified decision value outside an assert", src: "policy p: Test@1\nwhen approve.x == approve { deny(reason: x) }", errs: []string{"2:6: `approve` is a decision, not a value here", "2:19: `approve` is a decision, not a value here"}},
		{name: "unknown payload field", src: "policy p: Test@1\nwhen true { approve(reason: x, bak: 15m) }", errs: []string{"2:32: decision approve has no payload field \"bak\""}, help: "did you mean \"bake\"? approve takes reason: ok | x | b | release_manager | sre_hotfix, and bake: duration = 1h"},
		{name: "unknown payload field value still checked", src: "policy p: Test@1\nwhen true { approve(reason: x, bak: servce) }", errs: []string{"2:32: decision approve has no payload field \"bak\"", "2:37: unknown name `servce`"}},
		{name: "payload field twice", src: "policy p: Test@1\nwhen true { approve(reason: x, bake: 1h, bake: 2h) }", errs: []string{"2:42: field \"bake\" is given twice"}},
		{name: "payload field of the wrong type", src: "policy p: Test@1\nwhen true { approve(reason: x, bake: 15) }", errs: []string{"2:38: expected duration, found int"}, help: "a bare number is never a duration; write a literal like `30m`"},
		{name: "payload list element of the wrong type", src: "policy p: Test@1\nwhen true { review(reason: x, approvers: [\"a\", 1]) }", errs: []string{"2:48: expected string, found int"}},
		{name: "required payload field missing", src: "policy p: Test@1\nwhen true { review(reason: x) }", errs: []string{"2:13: decision review needs field \"approvers\""}, help: "review takes reason: r | c | x | service_owner, and approvers: list<string>"},
		{name: "payload reads the input", src: "policy p: Test@1\nwhen true { review(reason: x, approvers: service.owners) }"},
		{name: "payload enum value", src: "policy p: Test@1\nwhen true { escalate(reason: paged, tier: standard, plans: [standard, enterprise]) }"},
		{name: "payload enum from the input", src: "policy p: Test@1\nwhen true { escalate(reason: paged, tier: service.tier) }"},
		{name: "payload enum value of another enum", src: "policy p: Test@1\nwhen true { escalate(reason: paged, tier: free) }", errs: []string{"2:43: Tier has no value `free`"}, help: "Tier declares: critical, standard, internal"},
		{name: "payload enum as a string", src: "policy p: Test@1\nwhen true { escalate(reason: paged, tier: \"internal\") }", errs: []string{"2:43: expected Tier, found string"}, help: "an enum value is a bare name; write `internal`"},
		{name: "payload enum of the wrong enum", src: "policy p: Test@1\nwhen true { escalate(reason: paged, tier: service.plan) }", errs: []string{"2:43: expected Tier, found Plan"}},

		{name: "qualified reason", src: "policy p: Test@1\nwhen true { approve(reason: approve.release_manager) }", errs: []string{"2:29: a reason is a bare name"}, help: "write `reason: release_manager`"},
		{name: "reason qualified like an enum value", src: "policy p: Test@1\nwhen true { escalate(reason: Tier.critical) }", errs: []string{"2:30: a reason is a bare name"}, help: "write `reason: critical`"},
		{name: "qualified reason misspelled", src: "policy p: Test@1\nwhen true { deny(reason: deny.soak_to_short) }", errs: []string{"2:26: a reason is a bare name"}, help: "write `reason: soak_too_short`"},
		{name: "qualified reason undeclared", src: "policy p: Test@1\nwhen true { deny(reason: Tier.standard) }", errs: []string{"2:26: a reason is a bare name"}, help: "write `reason: <reason>`; deny declares: no_rule_matched, x, a, b, not_eligible, soak_too_short"},
		{name: "positional qualified reason", src: "policy p: Test@1\nwhen true { deny(deny.x) }", errs: []string{"2:18: the reason is a named argument"}, help: "write `deny(reason: x)`"},
		{name: "payload qualified enum value", src: "policy p: Test@1\nwhen true { escalate(reason: paged, tier: Tier.standard, plans: [Plan.standard, free]) }"},
		{name: "payload qualified value of another enum", src: "policy p: Test@1\nwhen true { escalate(reason: paged, tier: Plan.standard) }", errs: []string{"2:43: expected Tier, found Plan"}, help: "Tier declares standard too; write `Tier.standard`, or `standard`"},

		// Enums.
		{name: "enum param with a default", src: "policy p: Test@1\nparam tier: Tier = standard\nparam plans: list<Plan> = [standard, free]\nparam counts: map<Tier, int> = {critical: 1, standard: 2}\nwhen service.tier == tier and service.plan in plans and service.by_tier[tier] > counts[tier] { deny(reason: x) }"},
		{name: "enum param without a default", src: "policy p: Test@1\nparam tier: Tier\nwhen service.tier == tier { deny(reason: x) }"},
		{name: "optional enum param", src: "policy p: Test@1\nparam tier: ?Tier", errs: []string{"2:13: a param can't be optional"}, help: "an optional param is a param with a default; write `param tier: Tier = <default>`"},
		{name: "enum param default of another enum", src: "policy p: Test@1\nparam tier: Tier = free", errs: []string{"2:20: Tier has no value `free`"}, help: "Tier declares: critical, standard, internal"},
		{name: "enum param default misspelled", src: "policy p: Test@1\nparam tier: Tier = critcal", errs: []string{"2:20: Tier has no value `critcal`"}, help: "did you mean `critical`? Tier declares: critical, standard, internal"},
		{name: "enum param default as a string", src: "policy p: Test@1\nparam tier: Tier = \"critical\"", errs: []string{"2:20: expected Tier, found string"}, help: "an enum value is a bare name; write `critical`"},
		{name: "enum param with bounds", src: "policy p: Test@1\nparam tier: Tier = standard, min: critical", errs: []string{"2:35: a param of type Tier can't have bounds"}, help: "bounds apply to int, float and duration params"},
		{name: "enum param as a map value", src: "policy p: Test@1\nparam by_team: map<string, Tier>", errs: []string{"2:28: an enum can't be a map value"}, help: "a missing key would read as the zero value, and an enum has none; key the map by the enum instead, or use a list"},
		{name: "enum param of unknown type", src: "policy p: Test@1\nparam tier: Teir", errs: []string{"2:13: unknown type `Teir`"}, help: "did you mean `Tier`?"},
		{name: "let of a unique enum value", src: "policy p: Test@1\nlet t = critical\nlet ok = service.tier == t\nwhen ok { deny(reason: x) }"},
		{name: "let of an ambiguous enum value", src: "policy p: Test@1\nlet t = standard", errs: []string{"2:9: `standard` is a value of Plan and Tier"}, help: "write `Tier.standard` or `Plan.standard`"},
		{name: "let of a qualified enum value", src: "policy p: Test@1\nlet t = Plan.standard\nlet ok = service.plan == t\nwhen ok { deny(reason: x) }"},
		{name: "enum param with a qualified default", src: "policy p: Test@1\nparam tier: Tier = Tier.standard\nparam tiers: list<Tier> = [Tier.critical, internal]"},
		{name: "enum param with a qualified default of another enum", src: "policy p: Test@1\nparam tier: Tier = Plan.standard", errs: []string{"2:20: `Plan.standard` isn't a value of Tier"}},
		{name: "let collides with an enum type", src: "policy p: Test@1\nlet Tier = 1", errs: []string{"2:5: `Tier` is already the name of an enum type"}, help: "every name in a document means one thing; rename one of them"},
		{name: "quantifier variable collides with an enum type", src: "policy p: Test@1\nlet ok = any Plan in actor.roles: true", errs: []string{"2:14: `Plan` is already the name of an enum type"}},
		{name: "constructing an enum type", src: "policy p: Test@1\nwhen true { Tier(reason: x) }", errs: []string{"2:13: `Tier` is an enum type, not a decision"}},
		{name: "let collides with an enum value", src: "policy p: Test@1\nlet critical = 1", errs: []string{"2:5: `critical` is already the name of an enum value"}, help: "every name in a document means one thing; rename one of them"},
		{name: "param collides with an enum value", src: "policy p: Test@1\nparam standard: int", errs: []string{"2:7: `standard` is already the name of an enum value"}},
		{name: "quantifier variable collides with an enum value", src: "policy p: Test@1\nwhen any eu in actor.regions: true { deny(reason: x) }", errs: []string{"2:10: `eu` is already the name of an enum value"}},

		// Modules.
		{name: "module wrong kind", src: "module m: Other@1\nlet a = 1", errs: []string{"1:11: document is for kind Other, not Test"}},
		{name: "module let cycle", src: "module m: Test@1\nlet a = b\nlet b = a", errs: []string{"3:9: let `a` depends on itself"}},
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

// TestPins checks the kind version a document pins against a kind at
// version 3 that still accepts version 2.
func TestPins(t *testing.T) {
	k, errs := check.LoadKind("k.sigil", []byte("kind Test version 3, accepts: 2\ninput approvers: list<string>\nfn split(string, string) -> list<string>\nenum Level: low | high\ndecision d { reason: a | x | y }\ndecision audit { reason: x }\ncollect all"))
	if errs != nil {
		t.Fatal(errs)
	}
	tests := []struct {
		src     string
		errs    string // "line:col: msg"; empty means clean
		help    string
		shadows string // names kept despite a kind name added since the pin
	}{
		{src: "policy p: Test@3"},
		{src: "policy p: Test@2"},
		{src: "module m: Test@2"},
		{src: "policy p: Test", errs: "1:11: `Test` needs a version", help: "pin the kind version the document was written against, like `Test@3`"},
		{src: "module m: Test", errs: "1:11: `Test` needs a version"},
		{src: "policy p: Test@4", errs: "1:16: Test@4 is newer than the kind, which is at version 3",
			help: "the host's kind is older than the document; upgrade the host, or check the document against this version and lower the pin"},
		{src: "policy p: Test@1", errs: "1:16: Test@1 is no longer accepted; the kind accepts version 2 and later",
			help: "review the document against the kind's changes since version 1, then raise the pin"},
		{src: "policy p: Test@1\nlet a = actor", errs: "1:16: Test@1 is no longer accepted; the kind accepts version 2 and later\n2:9: unknown name `actor`"},

		// A document pinned below the kind's version keeps names the kind added since.
		{src: "policy p: Test@2\nparam approvers: list<string>\nwhen \"x\" in approvers { d(reason: a) }", shadows: "approvers"},
		{src: "policy p: Test@2\nlet split = 1\nlet audit = split > 0", shadows: "split audit"},
		{src: "policy p: Test@2\nwhen true { let approvers = [\"a\"] }", shadows: "approvers"},
		{src: "policy p: Test@2\nlet ok = any split in [\"a\"]: split == \"a\"", shadows: "split"},
		{src: "module m: Test@2\nlet approvers = [\"a\"]", shadows: "approvers"},
		{src: "policy p: Test@2\nlet low = 1\nlet ok = low > 0", shadows: "low"},
		{src: "policy p: Test@2\nlet ok = any high in [1]: high > 0", shadows: "high"},
		{src: "policy p: Test@2\nlet Level = 1\nlet ok = Level > 0", shadows: "Level"},
		{src: "policy p: Test@2\nlet Level = 1\nlet ok = Level.low", errs: "3:16: `Level` is int, which has no fields", shadows: "Level"},
		// Pinned to the current version, the author knew the name.
		{src: "policy p: Test@3\nparam approvers: list<string>", errs: "2:7: `approvers` is already the name of an input",
			help: "every name in a document means one thing; rename one of them"},
		{src: "policy p: Test@3\nlet ok = any split in [\"a\"]: true", errs: "2:14: `split` is already the name of a host function"},
		{src: "policy p: Test@3\nparam low: int", errs: "2:7: `low` is already the name of an enum value"},
		{src: "policy p: Test@3\nlet Level = 1", errs: "2:5: `Level` is already the name of an enum type"},
		// Names the document declares itself still can't collide.
		{src: "policy p: Test@2\nlet a = 1\nlet a = 2", errs: "3:5: `a` is already the name of a let"},
	}
	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			f, perrs := parser.ParseFile("p.sigil", []byte(tt.src))
			if perrs != nil {
				t.Fatalf("parse: %v", perrs)
			}
			c := check.New("p.sigil")
			switch d := f.Docs[0].(type) {
			case *ast.PolicyDoc:
				c.Policy(d, k)
			case *ast.ModuleDoc:
				c.Module(d, k)
			}
			got := make([]string, len(c.Errors()))
			for i, e := range c.Errors() {
				got[i] = e.Pos.String() + ": " + e.Msg
			}
			if g := strings.Join(got, "\n"); g != tt.errs {
				t.Errorf("errors:\n%s\nwant:\n%s", g, tt.errs)
			}
			if tt.help != "" && (len(c.Errors()) == 0 || c.Errors()[0].Help != tt.help) {
				t.Errorf("help = %v\nwant   %q", c.Errors(), tt.help)
			}
			names := make([]string, len(c.Info().Shadows))
			for i, n := range c.Info().Shadows {
				names[i] = n.Name
			}
			if got := strings.Join(names, " "); got != tt.shadows {
				t.Errorf("Info.Shadows = %q, want %q", got, tt.shadows)
			}
		})
	}
}

// TestInfoConstructors checks that every constructor is tied to its
// decision and its reason, which is what the evaluator builds candidates
// from.
func TestInfoConstructors(t *testing.T) {
	c, f := checkDoc(t, "policy p: Test@1\nwhen true {\n  deny(reason: a)\n  when release.hotfix { approve(bake: 15m, reason: b) }\n}")
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
	for call, want := range map[*ast.CallStmt]string{deny: "a", approve: "b"} {
		if r := c.Info().Reasons[call]; r == nil || r.Name != want {
			t.Errorf("Reasons[%s] = %v, want %s", call.Name.Name, r, want)
		}
	}
}
