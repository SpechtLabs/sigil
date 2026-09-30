package check_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

// testKind is the README kind with a few extra shapes: an optional field,
// timestamps, an optional struct, an int-keyed map, more functions, and
// enums, two of which share the value `standard`.
const testKind = `kind Test version 1
enum Tier: critical | standard | internal
enum Plan: free | standard | enterprise
enum Region: eu
  | us
type Release { soak: duration hotfix: bool ticket: ?string built_at: timestamp parent: ?Commit tier: ?Tier }
type Commit { id: int author: Actor merged_by: ?Actor note: ?string }
type Service { name: string tier: Tier plan: Plan owners: list<string> labels: map<string, string> owner: Actor scores: map<string, int> counts: list<int> by_id: map<int, string> allowed: list<Tier> by_tier: map<Tier, int> }
type Actor { name: string teams: list<string> roles: list<string> regions: list<string> }
input release: Release
input service: Service
input actor: Actor
input environment: string
input now: timestamp
input ratio: float
input count: int
fn split(string, string) -> list<string>
fn len(list<string>) -> int
fn now_fn() -> timestamp
fn tier_of(string) -> Tier
fn upgrade(Plan) -> Plan
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
}
decision escalate {
  reason: paged | critical
  tier: Tier = critical
  plans: list<Plan> = [free]
}
collect one
precedence deny > review > approve > escalate
default deny(reason: no_rule_matched)
`

var (
	tier = &types.Enum{Name: "Tier", Values: []string{"critical", "standard", "internal"}}
	plan = &types.Enum{Name: "Plan", Values: []string{"free", "standard", "enterprise"}}
)

func loadKind(t *testing.T) *kind.Kind {
	t.Helper()
	k, errs := check.LoadKind("test.sigil", []byte(testKind))
	if errs != nil {
		t.Fatalf("test kind: %v", errs)
	}
	return k
}

// env returns the scope of a policy of the test kind, with a param, two
// lets and their types declared.
func env(t *testing.T) *check.Env {
	t.Helper()
	e := check.NewEnv(loadKind(t))
	for name, b := range map[string]check.Binding{
		"min_soak": {Entity: check.Param, Type: types.Duration},
		"tiers":    {Entity: check.Param, Type: &types.List{Elem: types.String}},
		"cleared":  {Entity: check.Let, Type: types.Bool},
		"owners":   {Entity: check.Let, Type: &types.List{Elem: types.String}},
	} {
		if _, ok := e.Declare(name, b); !ok {
			t.Fatalf("declare %s", name)
		}
	}
	return e
}

func TestExpr(t *testing.T) {
	tests := []struct {
		src    string
		want   string // the type, when the expression checks
		msg    string // the error, when it doesn't
		help   string
		span   string
		assert bool       // check as an assert condition
		as     types.Type // check with ExprAs against this type
	}{
		// Names and literals.
		{src: "release", want: "Release"},
		{src: "environment", want: "string"},
		{src: "min_soak", want: "duration"},
		{src: "cleared", want: "bool"},
		{src: "deny", assert: true, want: "decision"},
		{src: "true", want: "bool"},
		{src: "42", want: "int"},
		{src: "0.5", want: "float"},
		{src: `"a"`, want: "string"},
		{src: "1h", want: "duration"},
		{src: "(count)", want: "int"},
		{src: `["a", "b"]`, want: "list<string>"},
		{src: "[1, 2]", want: "list<int>"},
		{src: `[["a"], []]`, want: "list<list<string>>"},
		{src: `[[], ["a"]]`, want: "list<list<string>>"},
		{src: `{"a": 1}`, want: "map<string, int>"},
		{src: "{environment: 1}", want: "map<string, int>"},
		{src: "{count: environment}", want: "map<int, string>"},
		{src: `service.labels has {environment: "payments"}`, want: "bool"},
		{src: `{1: "a", 2: "b"}`, want: "map<int, string>"},
		{src: `{"a": [], "b": [1]}`, want: "map<string, list<int>>"},
		{src: "[deny, approve]", assert: true, want: "list<decision>"},
		{src: `{"a": [approve]}`, assert: true, want: "map<string, list<decision>>"},
		{src: "deny", msg: "`deny` is a decision, not a value here", help: "to produce the decision, construct it inside a rule: `when <condition> { deny(reason: <reason>) }`; its bare name is only a value in an assert condition", span: "1:1-1:5"},
		{src: "environment == deny", msg: "`deny` is a decision, not a value here", span: "1:16-1:20"},
		{src: "[deny] one in actor.roles", msg: "`deny` is a decision, not a value here", span: "1:2-1:6"},
		{src: "outcome", assert: true, want: "list<decision>"},

		// Boolean operators.
		{src: "cleared and release.hotfix", want: "bool"},
		{src: "cleared or not release.hotfix", want: "bool"},
		{src: "cleared xor release.hotfix", want: "bool"},
		{src: "not cleared", want: "bool"},

		// Comparison.
		{src: "service.tier == critical", want: "bool"},
		{src: "release.soak != 1h", want: "bool"},
		{src: "release.soak < min_soak", want: "bool"},
		{src: "count >= 3", want: "bool"},
		{src: "ratio > 0.5", want: "bool"},
		{src: "now > release.built_at", want: "bool"},
		{src: "release.hotfix == true", want: "bool"},
		{src: "deny == approve", assert: true, want: "bool"},

		// Membership and list operators.
		{src: `"deployer" in actor.roles`, want: "bool"},
		{src: `"admin" not in actor.roles`, want: "bool"},
		{src: `service.labels has "regions"`, want: "bool"},
		{src: `"payments" in service.name`, want: "bool"},
		{src: "service.tier in [critical, standard]", want: "bool"},
		{src: "service.by_id has 3", want: "bool"},
		{src: "actor.teams any in service.owners", want: "bool"},
		{src: "split(service.labels[\"regions\"], \",\") all in actor.regions", want: "bool"},
		{src: "[deny, approve] one in outcome", assert: true, want: "bool"},
		{src: "[deny, approve] exclusive in outcome", assert: true, want: "bool"},
		{src: "[] all in actor.regions", want: "bool"},
		// The emptiness tests the `== []` hint suggests.
		{src: "any x in actor.regions: true", want: "bool"},
		{src: "not (any x in actor.regions: true)", want: "bool"},
		{src: "any x in (release.parent?.author.roles ?? []): true", want: "bool"},
		{src: "actor.regions all in []", want: "bool"},

		// Candidates: `outcome.<decision>` in asserts, typed by the payload.
		{src: "outcome.review", assert: true, want: "list<review candidate>"},
		{src: "outcome.review.service_owner", assert: true, want: "list<review candidate>"},
		{src: "all r in outcome.review: actor.name not in r.approvers", assert: true, want: "bool"},
		{src: "any a in outcome.approve: a.bake > 1h", assert: true, want: "bool"},
		{src: "all d in outcome.deny: d.reason in [deny.no_rule_matched, deny.x]", assert: true, want: "bool"},
		{src: "all r in outcome.review: r.reason == review.x", assert: true, want: "bool"},
		{src: `filter r in outcome.review: "a" in r.approvers`, assert: true, want: "list<review candidate>"},
		{src: "any r in (filter r2 in outcome.review.x: true): true", assert: true, want: "bool"},

		// Filters have the type of the list they filter.
		{src: `filter r in actor.roles: r like "sre-*"`, want: "list<string>"},
		{src: "filter n in [1, 2, 3]: n > 1", want: "list<int>"},
		{src: `"a" in (filter r in actor.roles: r != "b")`, want: "bool"},
		{src: "(filter r in actor.roles: true) all in actor.teams", want: "bool"},
		{src: "any r in (filter t in actor.teams: t in actor.roles): true", want: "bool"},
		{src: "filter r in (release.parent?.author.roles ?? []): true", want: "list<string>"},
		{src: `[] in [["a"]]`, want: "bool"},

		// Map containment and patterns.
		{src: `service.labels has {"team": "payments"}`, want: "bool"},
		{src: `service.labels has "env"`, want: "bool"},
		{src: "service.labels has {}", want: "bool"},
		{src: `service.name like "payments-*"`, want: "bool"},
		{src: "service.labels[\"team\"] matches `^team-[a-z]+$`", want: "bool"},
		{src: `service.name matches ("a" )`, want: "bool"},

		// Optionals.
		{src: `release.ticket ?? "none"`, want: "string"},
		{src: `release.ticket ?? "none" == "CHG-1"`, want: "bool"},

		// Arithmetic.
		{src: "count + 1", want: "int"},
		{src: "ratio - 0.5", want: "float"},
		{src: "release.soak + 2h", want: "duration"},
		{src: "now + 1h", want: "timestamp"},
		{src: "now - 1h", want: "timestamp"},
		{src: "now - release.built_at", want: "duration"},
		{src: "-count", want: "int"},
		{src: "-ratio", want: "float"},
		{src: "-min_soak", want: "duration"},
		{src: "release.soak + 2h >= min_soak", want: "bool"},

		// Postfix forms.
		{src: "service.owner.name", want: "string"},
		{src: "service.labels[\"team\"]", want: "string"},
		{src: "service.scores[\"a\"]", want: "int"},
		{src: "service.by_id[1]", want: "string"},
		{src: "actor.roles[0]", want: "string"},
		{src: "split(service.name, \"-\")", want: "list<string>"},
		{src: "split(service.name, \"-\")[0]", want: "string"},
		{src: "len(actor.roles)", want: "int"},
		{src: "len([])", want: "int"},
		{src: "now_fn()", want: "timestamp"},

		// Quantifiers.
		{src: `any r in actor.roles: r like "sre-*"`, want: "bool"},
		{src: `all r in actor.roles: r != "admin"`, want: "bool"},
		{src: "all a in actor.teams: any b in service.owners: a == b", want: "bool"},
		{src: "any n in service.counts: n > count", want: "bool"},

		// Typed by context.
		{src: "[]", as: &types.List{Elem: types.String}, want: "list<string>"},
		{src: "{}", as: &types.Map{Key: types.String, Value: types.Int}, want: "map<string, int>"},
		{src: "[[]]", as: &types.List{Elem: &types.List{Elem: types.Int}}, want: "list<list<int>>"},
		{src: `{"a": []}`, as: &types.Map{Key: types.String, Value: &types.List{Elem: types.Int}}, want: "map<string, list<int>>"},
		{src: "release.ticket", as: &types.Optional{Elem: types.String}, want: "?string"},
		{src: `"x"`, as: &types.Optional{Elem: types.String}, msg: "expected ?string, found string", span: "1:1-1:4"},

		// Unknown names.
		{src: "servce", msg: "unknown name `servce`", help: "did you mean `service`?", span: "1:1-1:7"},
		{src: "Release", msg: "unknown name `Release`", help: "did you mean `release`?", span: "1:1-1:8"},
		{src: "ministry", msg: "unknown name `ministry`", help: "names come from the kind's inputs, host functions, decisions and enum values, and the document's params, lets and imports", span: "1:1-1:9"},
		{src: "split", msg: "`split` is a host function, not a value", help: "call it with its arguments; it's declared as: fn split(string, string) -> list<string>", span: "1:1-1:6"},
		{src: "outcome", msg: "`outcome` can only be read in an assert condition", help: "a rule that read its own outcome could fire exactly when it doesn't; only `assert` conditions may read it", span: "1:1-1:8"},

		// Fields.
		{src: "service.teir", msg: `unknown field "teir" on type Service`, help: `did you mean "tier"? Service declares: name, tier, plan, owners, labels, owner, scores, counts, by_id, allowed, by_tier`, span: "1:9-1:13"},
		{src: "actor.email", msg: `unknown field "email" on type Actor`, help: "Actor declares: name, teams, roles, regions", span: "1:7-1:12"},
		{src: "environment.name", msg: "`environment` is string, which has no fields", span: "1:13-1:17"},
		{src: "actor.roles.first", msg: "`actor.roles` is list<string>, which has no fields", span: "1:13-1:18"},
		{src: "release.parent.id", msg: "`release.parent` is ?Commit, which may be absent", help: "read the field with `release.parent?.id`", span: "1:16-1:18"},

		// Presence.
		{src: "present release.ticket", want: "bool"},
		{src: "present release.parent", want: "bool"},
		{src: "present release.parent?.author", want: "bool"},
		{src: "not present release.parent and cleared", want: "bool"},
		{src: "present release.soak", msg: "`release.soak` is duration, which is always present", help: "only an optional value can be absent", span: "1:9-1:21"},
		{src: "present release.ticket ?? \"\"", msg: "`??` needs an optional on the left, found bool", span: "1:1-1:23"},

		// Element comparison.
		{src: `["eu-1"] in [["eu-1"], ["us-1"]]`, want: "bool"},
		{src: `service.owner in [service.owner]`, msg: "`in` can't compare elements of type Actor", help: "structs have no equality; compare a field that identifies them, such as a name", span: "1:1-1:33"},
		{src: `[service.owner] any in [service.owner]`, msg: "`any in` can't compare elements of type Actor", span: "1:1-1:39"},
		{src: `[[service.owner]] all in [[service.owner]]`, msg: "`all in` can't compare elements of type list<Actor>", span: "1:1-1:43"},
		{src: `service.labels has service.labels`, want: "bool"},

		// Optional chaining.
		{src: "release.parent?.id", want: "?int"},
		{src: "release.parent?.id ?? 0", want: "int"},
		{src: "release.parent?.id ?? 0 > 3", want: "bool"},
		{src: "release.parent?.note", want: "?string"},
		{src: "release.parent?.author.name", want: "?string"},
		{src: "release.parent?.author.roles[0]", want: "?string"},
		{src: "release.parent?.merged_by?.name", want: "?string"},
		{src: `release.parent?.author.roles ?? [] any in ["a"]`, want: "bool"},
		{src: `release.parent?.author.roles any in ["a"]`, msg: "`any in` needs lists on both sides, found ?list<string>", help: "unwrap it with `??`, like `release.parent?.author.roles ?? <default>`", span: "1:1-1:29"},
		{src: "release.parent?.merged_by.name", msg: "`release.parent?.merged_by` is ?Actor, which may be absent", help: "read the field with `release.parent?.merged_by?.name`", span: "1:27-1:31"},
		{src: "(release.parent?.author).name", msg: "`(release.parent?.author)` is ?Actor, which may be absent", span: "1:26-1:30"},
		{src: "release?.soak", msg: "`release` isn't optional", help: "read the field with `release.soak`; `?.` is for a struct that may be absent", span: "1:10-1:14"},
		{src: "release.ticket?.id", msg: "`release.ticket` holds string, which has no fields", span: "1:17-1:19"},
		{src: "release.parent?.nope", msg: `unknown field "nope" on type Commit`, span: "1:17-1:21"},
		{src: "servce.tier", msg: "unknown name `servce`", span: "1:1-1:7"},

		// Indexing and calls.
		{src: "service.labels[1]", msg: "expected string, found int", span: "1:16-1:17"},
		{src: `actor.roles["admin"]`, msg: "expected int, found string", span: "1:13-1:20"},
		{src: `environment["a"]`, msg: "`environment` is string, which can't be indexed", span: "1:1-1:12"},
		{src: `release.ticket["a"]`, msg: "`release.ticket` is ?string, which can't be indexed", help: "unwrap it with `??`, like `release.ticket ?? <default>`", span: "1:1-1:15"},
		{src: "split(service.name)", msg: "`split` takes 2 arguments, found 1", help: "it's declared as: fn split(string, string) -> list<string>", span: "1:1-1:20"},
		{src: "now_fn(1)", msg: "`now_fn` takes 0 arguments, found 1", span: "1:1-1:10"},
		{src: "len(actor.roles, 1)", msg: "`len` takes 1 argument, found 2", span: "1:1-1:20"},
		{src: "split(service.name, 1)", msg: "expected string, found int", span: "1:21-1:22"},
		{src: "len(service.labels)", msg: "expected list<string>, found map<string, string>", span: "1:5-1:19"},
		{src: "splt(service.name, \",\")", msg: "unknown function `splt`", help: "did you mean `split`?", span: "1:1-1:5"},
		{src: "actor(1)", msg: "`actor` is an input, not a function", help: "policies can only call the host functions the kind declares", span: "1:1-1:6"},
		{src: "cleared(1)", msg: "`cleared` is a let, not a function", span: "1:1-1:8"},
		{src: "service.name(1)", msg: "`service.name` isn't a function", span: "1:1-1:13"},

		// Boolean operators need bools.
		{src: "actor.roles and cleared", msg: "expected bool, found list<string>", span: "1:1-1:12"},
		{src: "not count", msg: "expected bool, found int", span: "1:5-1:10"},
		{src: "cleared or 1", msg: "expected bool, found int", span: "1:12-1:13"},
		{src: "release.ticket and cleared", msg: "expected bool, found ?string", help: "unwrap it with `??`, like `release.ticket ?? <default>`", span: "1:1-1:15"},

		// Comparison rules.
		{src: "release.soak == 30", msg: "`==` needs operands of the same type, found duration and int", help: "a bare number is never a duration; write a literal like `30m`", span: "1:1-1:19"},
		{src: "3 == 3.0", msg: "`==` needs operands of the same type, found int and float", help: "int and float don't convert implicitly", span: "1:1-1:9"},
		{src: `count == "3"`, msg: "`==` needs operands of the same type, found int and string", span: "1:1-1:13"},
		{src: `release.ticket == "CHG-1"`, msg: "`==` can't compare ?string; unwrap it first", help: "unwrap it with `??`, like `release.ticket ?? <default>`", span: "1:1-1:15"},
		{src: "actor.roles == actor.teams", msg: "`==` isn't defined for list<string>", help: "lists, maps and structs can't be compared yet", span: "1:1-1:27"},
		{src: "service == service", msg: "`==` isn't defined for Service", span: "1:1-1:19"},
		{src: `"a" < "b"`, msg: "`<` isn't defined for string", help: "only int, float, duration and timestamp are ordered", span: "1:1-1:10"},
		{src: "cleared < release.hotfix", msg: "`<` isn't defined for bool", span: "1:1-1:25"},
		{src: "deny < approve", assert: true, msg: "`<` isn't defined for decision", span: "1:1-1:15"},
		{src: "count < ratio", msg: "`<` needs operands of the same type, found int and float", span: "1:1-1:14"},
		// An empty literal takes the other side's type, so comparing a list or
		// map with one is reported as that comparison, with a way to test for
		// emptiness instead.
		{src: "actor.regions != []", msg: "`!=` isn't defined for list<string>", help: "lists have no `!=`; to test that `actor.regions` isn't empty, write `any x in actor.regions: true`, or call a host function such as `len` if the kind declares one", span: "1:1-1:20"},
		{src: "[] == actor.regions", msg: "`==` isn't defined for list<string>", help: "lists have no `==`; to test that `actor.regions` is empty, write `not (any x in actor.regions: true)`, or call a host function such as `len` if the kind declares one", span: "1:1-1:20"},
		{src: "actor.regions == ([])", msg: "`==` isn't defined for list<string>", help: "lists have no `==`; to test that `actor.regions` is empty, write `not (any x in actor.regions: true)`, or call a host function such as `len` if the kind declares one", span: "1:1-1:22"},
		{src: "any x in actor.roles: actor.regions != []", msg: "`!=` isn't defined for list<string>", help: "lists have no `!=`; to test that `actor.regions` isn't empty, write `any x2 in actor.regions: true`, or call a host function such as `len` if the kind declares one", span: "1:23-1:42"},
		{src: "release.parent?.author.roles != []", msg: "`!=` isn't defined for ?list<string>", help: "lists have no `!=`; to test that `release.parent?.author.roles` isn't empty, write `any x in (release.parent?.author.roles ?? []): true`, or call a host function such as `len` if the kind declares one", span: "1:1-1:35"},
		{src: "service.labels == {}", msg: "`==` isn't defined for map<string, string>", help: "maps have no `==`; test for a key with `has`, or for emptiness with a host function such as `len` if the kind declares one", span: "1:1-1:21"},
		{src: `actor.roles != ["a"]`, msg: "`!=` isn't defined for list<string>", help: "lists, maps and structs can't be compared yet", span: "1:1-1:21"},
		{src: "actor.regions < []", msg: "`<` isn't defined for list<string>", help: "only int, float, duration and timestamp are ordered", span: "1:1-1:19"},
		{src: "[] == []", msg: "`==` isn't defined for an empty list", help: "lists, maps and structs can't be compared yet", span: "1:1-1:9"},
		{src: "{} != 1", msg: "`!=` isn't defined for an empty map", help: "lists, maps and structs can't be compared yet", span: "1:1-1:8"},
		{src: "count == []", msg: "`==` isn't defined for an empty list", span: "1:1-1:12"},

		// Membership rules.
		{src: "1 in actor.roles", msg: "`in` needs an element of the list's type, found int in list<string>", span: "1:1-1:17"},
		{src: `"team" in service.labels`, msg: "`in` doesn't apply to a map; a key is tested with `has`", help: "write `service.labels has \"team\"`", span: "1:1-1:25"},
		{src: `"team" not in service.labels`, msg: "`not in` doesn't apply to a map; a key is tested with `has`", help: "write `not service.labels has \"team\"`", span: "1:1-1:29"},
		{src: `1 in service.name`, msg: "`in` needs a string on the left of a string, found int", help: "`in` on a string tests for a substring", span: "1:1-1:18"},
		{src: `"a" in count`, msg: "`in` needs a list, map or string on the right, found int", span: "1:8-1:13"},
		{src: `"a" in release.ticket`, msg: "`in` needs a list, map or string on the right, found ?string", help: "unwrap it with `??`, like `release.ticket ?? <default>`", span: "1:8-1:22"},
		{src: `"a" not in count`, msg: "`not in` needs a list, map or string on the right, found int", span: "1:12-1:17"},
		{src: "[] in []", msg: "cannot infer the type of `([] in [])`", span: "1:1-1:9"},
		{src: "actor.roles all in service.counts", msg: "`all in` needs lists of one element type, found list<string> and list<int>", span: "1:1-1:34"},
		{src: "service.name any in actor.roles", msg: "`any in` needs lists on both sides, found string", span: "1:1-1:13"},
		{src: "actor.roles one in service.labels", msg: "`one in` needs lists on both sides, found map<string, string>", span: "1:20-1:34"},
		{src: "[] exclusive in []", msg: "cannot infer the type of `([] exclusive in [])`", span: "1:1-1:19"},

		// has, like, matches, ??.
		{src: `actor.roles has "a"`, msg: "`has` needs a map on the left, found list<string>", span: "1:1-1:12"},
		{src: `service.labels has 1`, msg: "`has` needs map<string, string> or a key of type string, found int", span: "1:20-1:21"},
		{src: `service.labels has {"a": 1}`, msg: "expected string, found int", help: "", span: "1:26-1:27"},
		{src: `service.labels has service.scores`, msg: "`has` needs map<string, string> or a key of type string, found map<string, int>", span: "1:20-1:34"},
		{src: `service.name like service.tier`, msg: "the pattern after `like` must be a string literal", help: "patterns compile once, when the policy loads, so they can't be computed", span: "1:19-1:31"},
		{src: `count like "1*"`, msg: "expected string, found int", span: "1:1-1:6"},
		{src: "service.name matches `(`", msg: "invalid regular expression: error parsing regexp: missing closing ): `(`", help: "patterns are RE2 regular expressions, as Go's regexp package reads them", span: "1:22-1:25"},
		{src: `environment ?? "x"`, msg: "`??` needs an optional on the left, found string", help: "only an optional (`?T`) value needs a default; this one is always present", span: "1:1-1:12"},
		{src: "release.ticket ?? 1", msg: "expected string, found int", span: "1:19-1:20"},

		// Arithmetic rules.
		{src: `service.name + "x"`, msg: "`+` isn't defined for string and string", help: "there is no string concatenation", span: "1:1-1:19"},
		{src: "count + ratio", msg: "`+` isn't defined for int and float", help: "int and float don't convert implicitly", span: "1:1-1:14"},
		{src: "release.soak + 30", msg: "`+` isn't defined for duration and int", help: "a bare number is never a duration; write a literal like `30m`", span: "1:1-1:18"},
		{src: "now + now", msg: "`+` isn't defined for timestamp and timestamp", help: "`+` and `-` work on int, float and duration, and on a timestamp with a duration", span: "1:1-1:10"},
		{src: "1h - now", msg: "`-` isn't defined for duration and timestamp", span: "1:1-1:9"},
		{src: "cleared + cleared", msg: "`+` isn't defined for bool and bool", span: "1:1-1:18"},
		{src: "release.ticket + \"x\"", msg: "`+` isn't defined for ?string and string", help: "unwrap it with `??`, like `release.ticket ?? <default>`", span: "1:1-1:21"},
		{src: "-service.name", msg: "`-` needs int, float or duration, found string", span: "1:1-1:14"},
		{src: "-now", msg: "`-` needs int, float or duration, found timestamp", span: "1:1-1:5"},

		// Literals.
		{src: `["a", 1]`, msg: "expected string, found int", help: "every element of a list has the same type", span: "1:7-1:8"},
		{src: `[[], 1]`, msg: "expected int, found an empty list", span: "1:2-1:4"},
		{src: `[1, []]`, msg: "expected int, found an empty list", span: "1:5-1:7"},
		{src: `{"a": 1, "b": "x"}`, msg: "expected int, found string", help: "every value of a map has the same type", span: "1:15-1:18"},
		{src: `{"a": 1, 2: 3}`, msg: "expected string, found int", help: "every key of a map has the same type", span: "1:10-1:11"},
		{src: `{["a"]: 1}`, msg: "list<string> can't be a map key", help: "map keys are bool, int, float, string, duration, timestamp or an enum", span: "1:2-1:7"},

		// A map key is an expression: a bare word nothing declares gets the
		// quoted string as its fix, and a close name as well when one exists.
		{src: `{team: "payments"}`, msg: "unknown name `team`", help: "a map key is an expression, so `team` reads as a name; for the string key, write `\"team\"`", span: "1:2-1:6"},
		{src: `service.labels has {team: "payments"}`, msg: "unknown name `team`", help: "a map key is an expression, so `team` reads as a name; for the string key, write `\"team\"`", span: "1:21-1:25"},
		{src: `{"env": "prod", team: "payments"}`, msg: "unknown name `team`", help: "a map key is an expression, so `team` reads as a name; for the string key, write `\"team\"`", span: "1:17-1:21"},
		{src: `{tier: "critical"}`, msg: "unknown name `tier`", help: "a map key is an expression, so `tier` reads as a name; for the string key, write `\"tier\"`, or did you mean `Tier`?", span: "1:2-1:6"},
		{src: "{team: 1}", as: &types.Map{Key: types.String, Value: types.Int}, msg: "unknown name `team`", help: "a map key is an expression, so `team` reads as a name; for the string key, write `\"team\"`", span: "1:2-1:6"},
		// Where the key type isn't string, quoting wouldn't compile either,
		// so the plain name error stands; the same for a parenthesized key.
		{src: `service.by_id has {team: "a"}`, msg: "unknown name `team`", help: "names come from the kind's inputs, host functions, decisions and enum values, and the document's params, lets and imports", span: "1:20-1:24"},
		{src: `{1: "a", team: "b"}`, msg: "unknown name `team`", help: "names come from the kind's inputs, host functions, decisions and enum values, and the document's params, lets and imports", span: "1:10-1:14"},
		{src: `{(team): "a"}`, msg: "unknown name `team`", help: "names come from the kind's inputs, host functions, decisions and enum values, and the document's params, lets and imports", span: "1:3-1:7"},
		// A bare key that resolves is an ordinary expression.
		{src: `{tiers: 1}`, msg: "list<string> can't be a map key", span: "1:2-1:7"},
		{src: `{"a": [], "b": 1}`, msg: "expected int, found an empty list", span: "1:7-1:9"},
		{src: `{"a": approve}`, assert: true, msg: "a decision can't be a map value", help: "a missing key would read as the zero value, and a decision has none; test decisions with `in outcome`, or collect them in a list like `[deny, approve]`", span: "1:7-1:14"},
		{src: `{"a": [], "b": deny}`, assert: true, msg: "a decision can't be a map value", span: "1:16-1:20"},
		{src: `{"a": 1} has {"b": deny}`, assert: true, msg: "expected int, found decision", span: "1:20-1:24"},
		{src: "[]", msg: "cannot infer the type of `[]`", help: "add an element, or use the literal where a typed list or map is expected", span: "1:1-1:3"},
		{src: "{}", msg: "cannot infer the type of `{}`", span: "1:1-1:3"},
		{src: "[[]]", msg: "cannot infer the type of `[[]]`", span: "1:1-1:5"},
		{src: "[]", as: types.Int, msg: "expected int, found an empty list", span: "1:1-1:3"},
		{src: "{}", as: &types.List{Elem: types.Int}, msg: "expected list<int>, found an empty map", span: "1:1-1:3"},

		// Quantifier rules.
		{src: "any r in service.labels: true", msg: "`any` needs a list to range over, found map<string, string>", help: "quantifiers range over lists; to test a map's keys, index it or use `has` on map<string, string>", span: "1:10-1:24"},
		{src: "all r in environment: true", msg: "`all` needs a list to range over, found string", help: "quantifiers range over lists", span: "1:10-1:21"},
		{src: "any r in release.ticket: true", msg: "`any` needs a list to range over, found ?string", help: "unwrap it with `??`, like `release.ticket ?? <default>`", span: "1:10-1:24"},
		{src: "any actor in actor.roles: true", msg: "`actor` is already the name of an input", help: "nothing shadows anything; pick a name that isn't in use", span: "1:5-1:10"},
		{src: "any tiers in actor.roles: true", msg: "`tiers` is already the name of a param", span: "1:5-1:10"},
		{src: "any split in actor.roles: true", msg: "`split` is already the name of a host function", span: "1:5-1:10"},
		{src: "any r in actor.roles: any r in actor.teams: true", msg: "`r` is already the name of a quantifier variable", span: "1:27-1:28"},
		{src: "any r in actor.roles: r", msg: "expected bool, found string", span: "1:23-1:24"},
		{src: "any r in actor.roles: r == 1", msg: "`==` needs operands of the same type, found string and int", span: "1:23-1:29"},
		{src: "(any r in actor.roles: r == \"a\") and r == \"b\"", msg: "unknown name `r`", span: "1:38-1:39"},

		// Candidates are ranged over and read field by field, nothing else.
		{src: "outcome.reviw", assert: true, msg: "the kind declares no decision `reviw`", help: "did you mean `review`? `outcome.<decision>` reads the candidates of one decision; the kind declares: deny, review, approve, escalate", span: "1:9-1:14"},
		{src: "outcome.review.approvers", assert: true, msg: "decision review has no reason `approvers`", help: "`approvers` is a field of each candidate, not of the list; read it per candidate, like `all x in outcome.review: x.approvers ...`", span: "1:16-1:25"},
		{src: "outcome.review.servce_owner", assert: true, msg: "decision review has no reason `servce_owner`", help: "did you mean `service_owner`? review declares: r, c, x, service_owner", span: "1:16-1:28"},
		{src: "all r in outcome.review: r.aprovers", assert: true, msg: "unknown field \"aprovers\" on a review candidate", help: "did you mean \"approvers\"? a review candidate has: approvers, reason", span: "1:28-1:36"},
		{src: "all a in outcome.approve: a.bak > 1h", assert: true, msg: "unknown field \"bak\" on an approve candidate", help: "did you mean \"bake\"? an approve candidate has: bake, reason", span: "1:29-1:32"},
		{src: "outcome.review[0].approvers", assert: true, msg: "`outcome.review` is a list of candidates, which can't be indexed", help: "the order of candidates isn't part of the outcome; test every one with `all x in outcome.review: ...`, or `any`", span: "1:1-1:18"},
		{src: "(filter r in outcome.review: true)[0]", assert: true, msg: "`((filter r in outcome.review: true))` is a list of candidates, which can't be indexed", span: "1:1-1:38"},
		{src: "[outcome.review]", assert: true, msg: "list<review candidate> can't be a list element", help: "candidates can only be ranged over with `any`, `all` or `filter`, and read field by field, like `all r in outcome.<decision>: r.<field> ...`", span: "1:2-1:16"},
		{src: "all r in outcome.review: [r]", assert: true, msg: "review candidate can't be a list element", span: "1:27-1:28"},
		{src: `all r in outcome.review: {"a": r}`, assert: true, msg: "review candidate can't be a map value", span: "1:32-1:33"},
		{src: "all r in outcome.review: all s in outcome.review: r == s", assert: true, msg: "`==` isn't defined for review candidate", help: "candidates have no equality; compare a field of each, such as `reason`", span: "1:51-1:57"},
		{src: "outcome.review any in outcome.review", assert: true, msg: "`any in` can't compare elements of type review candidate", help: "candidates have no equality; compare a field of each, such as `reason`", span: "1:1-1:37"},
		{src: "all r in outcome.review: r in outcome.review", assert: true, msg: "`in` can't compare elements of type review candidate", span: "1:26-1:45"},
		{src: "outcome?.review", assert: true, msg: "`outcome` isn't optional", span: "1:10-1:16"},
		{src: "any d in outcome: d.x", assert: true, msg: "`d` is a decision value, not a decision's name, so `.x` names no reason", help: "compare the whole value instead, like `d == <decision>.x`, or test it with `in`", span: "1:21-1:22"},
		{src: "all r in outcome.review: r.reason.x", assert: true, msg: "`r.reason.x` names no reason", help: "a reason follows a decision's name directly, like `approve.release_manager`", span: "1:35-1:36"},
		{src: "outcome.review", msg: "`outcome` can only be read in an assert condition", help: "a rule that read its own outcome could fire exactly when it doesn't; only `assert` conditions may read it", span: "1:1-1:8"},

		// Filter rules, the same as a quantifier's.
		{src: "filter r in service.labels: true", msg: "`filter` needs a list to range over, found map<string, string>", help: "a filter ranges over a list; to test a map's keys, index it or use `has` on map<string, string>", span: "1:13-1:27"},
		{src: "filter r in environment: true", msg: "`filter` needs a list to range over, found string", help: "a filter ranges over a list", span: "1:13-1:24"},
		{src: "filter r in nope: true", msg: "unknown name `nope`", span: "1:13-1:17"},
		{src: "filter r in release.ticket: true", msg: "`filter` needs a list to range over, found ?string", help: "unwrap it with `??`, like `release.ticket ?? <default>`", span: "1:13-1:27"},
		{src: "filter actor in actor.roles: true", msg: "`actor` is already the name of an input", help: "nothing shadows anything; pick a name that isn't in use", span: "1:8-1:13"},
		{src: "filter r in actor.roles: any r in actor.teams: true", msg: "`r` is already the name of a filter variable", span: "1:30-1:31"},
		{src: "any r in actor.roles: r in (filter r in actor.teams: true)", msg: "`r` is already the name of a quantifier variable", span: "1:36-1:37"},
		{src: "filter r in actor.roles: r", msg: "expected bool, found string", span: "1:26-1:27"},
		{src: "(filter r in actor.roles: true) all in [r]", msg: "unknown name `r`", span: "1:41-1:42"},

		// Enum values: a bare name, typed by the context first, then by the
		// one enum that declares it.
		{src: "critical", want: "Tier"},
		{src: "free", want: "Plan"},
		{src: "(eu)", want: "Region"},
		{src: "standard", msg: "`standard` is a value of Plan and Tier", help: "write `Tier.standard` or `Plan.standard`", span: "1:1-1:9"},
		{src: "standard", as: plan, want: "Plan"},
		{src: "standard", as: &types.Optional{Elem: tier}, msg: "expected ?Tier, found Tier", span: "1:1-1:9"},
		{src: "critical", as: plan, msg: "Plan has no value `critical`", help: "Plan declares: free, standard, enterprise", span: "1:1-1:9"},
		{src: "entreprise", as: plan, msg: "Plan has no value `entreprise`", help: "did you mean `enterprise`? Plan declares: free, standard, enterprise", span: "1:1-1:11"},
		{src: "service.tier == critical", want: "bool"},
		{src: "critical == service.tier", want: "bool"},
		{src: "service.tier == standard", want: "bool"},
		{src: "standard != service.tier", want: "bool"},
		{src: "service.plan == standard", want: "bool"},
		{src: "(standard) == service.plan", want: "bool"},
		{src: "critical == standard", want: "bool"},
		{src: "standard == critical", want: "bool"},
		{src: "service.tier == service.tier", want: "bool"},
		{src: "tier_of(service.name) == internal", want: "bool"},
		{src: "service.tier == critcal", msg: "Tier has no value `critcal`", help: "did you mean `critical`? Tier declares: critical, standard, internal", span: "1:17-1:24"},
		{src: "critcal == service.tier", msg: "Tier has no value `critcal`", span: "1:1-1:8"},
		{src: "service.tier == free", msg: "Tier has no value `free`", help: "Tier declares: critical, standard, internal", span: "1:17-1:21"},
		{src: "environment == critcal", msg: "unknown name `critcal`", help: "did you mean `critical`?", span: "1:16-1:23"},
		{src: "environment == critical", msg: "`==` needs operands of the same type, found string and Tier", span: "1:1-1:24"},
		{src: "service.tier == service.plan", msg: "`==` needs operands of the same type, found Tier and Plan", span: "1:1-1:29"},
		{src: `service.tier == "critical"`, msg: "`==` needs operands of the same type, found Tier and string", help: "an enum value is a bare name; write `critical`", span: "1:1-1:27"},
		{src: `"critical" != service.tier`, msg: "`!=` needs operands of the same type, found string and Tier", help: "an enum value is a bare name; write `critical`", span: "1:1-1:27"},
		{src: `service.tier == "crit"`, msg: "`==` needs operands of the same type, found Tier and string", help: "an enum value is a bare name; Tier declares: critical, standard, internal", span: "1:1-1:23"},
		{src: `service.tier == "critcal"`, msg: "`==` needs operands of the same type, found Tier and string", help: "an enum value is a bare name; did you mean `critical`? Tier declares: critical, standard, internal", span: "1:1-1:26"},
		{src: "service.tier < critical", msg: "`<` isn't defined for Tier", help: "an enum's values have no order; test them with `==` or `in`", span: "1:1-1:24"},
		{src: "release.tier >= standard", msg: "`>=` isn't defined for ?Tier", help: "an enum's values have no order; test them with `==` or `in`", span: "1:1-1:25"},
		{src: "release.tier == critical", msg: "`==` can't compare ?Tier; unwrap it first", help: "unwrap it with `??`, like `release.tier ?? <default>`", span: "1:1-1:13"},
		{src: "release.tier ?? standard", want: "Tier"},
		{src: "(release.tier ?? standard) == internal", want: "bool"},
		{src: "release.tier ?? free", msg: "Tier has no value `free`", span: "1:17-1:21"},
		{src: "present release.tier", want: "bool"},
		{src: `service.tier like "c*"`, msg: "expected string, found Tier", span: "1:1-1:13"},
		{src: "service.tier + critical", msg: "`+` isn't defined for Tier and Tier", span: "1:1-1:24"},
		{src: "-critical", msg: "`-` needs int, float or duration, found Tier", span: "1:1-1:10"},

		// Enum values in lists, sets and maps.
		{src: "[critical, internal]", want: "list<Tier>"},
		{src: "[standard, critical]", want: "list<Tier>"},
		{src: "[standard, service.tier]", want: "list<Tier>"},
		{src: "[service.plan, standard]", want: "list<Plan>"},
		{src: "[[standard], service.allowed]", want: "list<list<Tier>>"},
		{src: "[[], [critical]]", want: "list<list<Tier>>"},
		{src: "[{critical: 1}, service.by_tier]", want: "list<map<Tier, int>>"},
		{src: "critical in service.name", msg: "`in` needs a string on the left of a string, found Tier", span: "1:1-1:25"},
		{src: "[standard]", msg: "`standard` is a value of Plan and Tier", span: "1:2-1:10"},
		{src: "[critical, free]", msg: "Tier has no value `free`", span: "1:12-1:16"},
		{src: `[critical, "internal"]`, msg: "expected string, found Tier", span: "1:2-1:10"},
		{src: "service.tier in [standard, internal]", want: "bool"},
		{src: "service.tier not in [standard]", want: "bool"},
		{src: "standard in service.allowed", want: "bool"},
		{src: "[standard] in [service.allowed]", want: "bool"},
		{src: "service.tier in [standard, internl]", msg: "Tier has no value `internl`", help: "did you mean `internal`? Tier declares: critical, standard, internal", span: "1:28-1:35"},
		{src: "critcal in service.allowed", msg: "Tier has no value `critcal`", span: "1:1-1:8"},
		{src: `service.tier in ["critical", "standard"]`, msg: "`in` needs an element of the list's type, found Tier in list<string>", help: "an enum value is a bare name; write `[critical, standard]`", span: "1:1-1:41"},
		{src: `service.tier in ["critical", "gold"]`, msg: "`in` needs an element of the list's type, found Tier in list<string>", help: "an enum value is a bare name; Tier declares: critical, standard, internal", span: "1:1-1:37"},
		{src: `service.tier in ["critical", environment]`, msg: "`in` needs an element of the list's type, found Tier in list<string>", help: "", span: "1:1-1:42"},
		{src: `service.tier in (["critical"])`, msg: "`in` needs an element of the list's type, found Tier in list<string>", help: "an enum value is a bare name; write `[critical]`", span: "1:1-1:31"},
		{src: `"critical" in service.allowed`, msg: "`in` needs an element of the list's type, found string in list<Tier>", help: "an enum value is a bare name; write `critical`", span: "1:1-1:30"},
		{src: "service.tier in actor.roles", msg: "`in` needs an element of the list's type, found Tier in list<string>", help: "", span: "1:1-1:28"},
		{src: "service.tier in service.name", msg: "`in` needs a string on the left of a string, found Tier", span: "1:1-1:29"},
		{src: "service.allowed any in [standard]", want: "bool"},
		{src: "[critical] all in service.allowed", want: "bool"},
		{src: "service.allowed one in [internal, standard]", want: "bool"},
		{src: "[standard] exclusive in service.allowed", want: "bool"},
		{src: "[free] any in service.allowed", msg: "Tier has no value `free`", span: "1:2-1:6"},
		{src: "service.by_tier has critical", want: "bool"},
		{src: "service.by_tier has standard", want: "bool"},
		{src: "service.by_tier has {critical: 1, standard: 2}", want: "bool"},
		{src: "service.by_tier has critcal", msg: "Tier has no value `critcal`", span: "1:21-1:28"},
		{src: "service.by_tier has free", msg: "Tier has no value `free`", span: "1:21-1:25"},
		{src: `service.by_tier has "critical"`, msg: "`has` needs map<Tier, int> or a key of type Tier, found string", span: "1:21-1:31"},
		{src: "any t in service.allowed: {standard: 1} has t", want: "bool"},
		{src: "{standard: 1, critical: 2} has service.tier", want: "bool"},
		{src: "{standard: 1} has service.by_tier", want: "bool"},
		{src: "{critcal: 1} has service.tier", msg: "Tier has no value `critcal`", help: "did you mean `critical`? Tier declares: critical, standard, internal", span: "1:2-1:9"},
		{src: "{standard: 1} has service.name", msg: "`standard` is a value of Plan and Tier", span: "1:2-1:10"},
		{src: "{critical: 1} has nope", msg: "Tier has no value `nope`", span: "1:19-1:23"},
		{src: "{standard: 1} has release.tier", msg: "`has` needs map<Tier, int> or a key of type Tier, found ?Tier", span: "1:19-1:31"},
		{src: "service.by_tier[internal]", want: "int"},
		{src: "service.by_tier[standard] > 1", want: "bool"},
		{src: "service.by_tier[free]", msg: "Tier has no value `free`", span: "1:17-1:21"},
		{src: `service.by_tier["internal"]`, msg: "expected Tier, found string", help: "an enum value is a bare name; write `internal`", span: "1:17-1:27"},
		{src: "{critical: 1, standard: 2}", want: "map<Tier, int>"},
		{src: "{standard: 1, critical: 2}", msg: "`standard` is a value of Plan and Tier", span: "1:2-1:10"},
		{src: "{critical: 1}", as: &types.Map{Key: tier, Value: types.Int}, want: "map<Tier, int>"},
		{src: "{standard: 1}", as: &types.Map{Key: tier, Value: types.Int}, want: "map<Tier, int>"},
		{src: `{"a": [standard], "b": [critical]}`, msg: "`standard` is a value of Plan and Tier", span: "1:8-1:16"},
		{src: `{"a": [critical], "b": [standard]}`, want: "map<string, list<Tier>>"},
		{src: `{"a": critical}`, msg: "an enum can't be a map value", help: "a missing key would read as the zero value, and an enum has none; key the map by the enum instead, or use a list", span: "1:7-1:15"},
		{src: "any t in service.allowed: t == standard", want: "bool"},
		{src: "filter t in service.allowed: t != internal", want: "list<Tier>"},
		{src: "any critical in service.allowed: true", msg: "`critical` is already the name of an enum value", help: "nothing shadows anything; pick a name that isn't in use", span: "1:5-1:13"},

		// Qualified enum values name their enum, so they need no context.
		{src: "Tier.standard", want: "Tier"},
		{src: "Plan.standard", want: "Plan"},
		{src: "Tier.critical", want: "Tier"},
		{src: "(Plan.free)", want: "Plan"},
		{src: "service.tier == Tier.standard", want: "bool"},
		{src: "Plan.standard == service.plan", want: "bool"},
		{src: "[Tier.standard, standard]", want: "list<Tier>"},
		{src: "[standard, Plan.standard]", want: "list<Plan>"},
		{src: "service.tier in [Tier.standard, internal]", want: "bool"},
		{src: "{Tier.standard: 1, critical: 2}", want: "map<Tier, int>"},
		{src: "service.by_tier has Tier.standard", want: "bool"},
		{src: "service.by_tier[Tier.internal]", want: "int"},
		{src: "release.tier ?? Tier.standard", want: "Tier"},
		{src: "upgrade(Plan.standard)", want: "Plan"},
		{src: "Tier.standard", as: tier, want: "Tier"},
		{src: "Tier.critcal", msg: "Tier has no value `critcal`", help: "did you mean `critical`? Tier declares: critical, standard, internal", span: "1:6-1:13"},
		{src: "Tier.free", msg: "Tier has no value `free`", help: "Tier declares: critical, standard, internal", span: "1:6-1:10"},
		{src: "Tier?.critical", msg: "`Tier` isn't optional", help: "write `Tier.critical`; `?.` reads a field of an optional struct", span: "1:7-1:15"},
		{src: "service.plan == Tier.standard", msg: "`==` needs operands of the same type, found Plan and Tier", help: "Plan declares standard too; write `Plan.standard`, or `standard`", span: "1:1-1:30"},
		{src: "upgrade(Tier.standard)", msg: "expected Plan, found Tier", help: "Plan declares standard too; write `Plan.standard`, or `standard`", span: "1:9-1:22"},
		{src: "Tier.standard", as: plan, msg: "expected Plan, found Tier", span: "1:1-1:14"},
		{src: "Tier.critical", as: plan, msg: "expected Plan, found Tier", help: "", span: "1:1-1:14"},
		{src: "Tier(1)", msg: "`Tier` is an enum type, not a function", span: "1:1-1:5"},
		{src: "Tier.critical.name", msg: "`Tier.critical` is Tier, which has no fields", span: "1:15-1:19"},
		{src: "Service.name", msg: "unknown name `Service`", span: "1:1-1:8"},

		// An enum type isn't a value.
		{src: "Tier", msg: "Tier is an enum type", help: "name one of its values, such as `Tier.critical`", span: "1:1-1:5"},
		{src: "service.tier == Tier", msg: "Tier is an enum type", help: "name one of its values, such as `Tier.critical`", span: "1:17-1:21"},
		{src: "[Plan]", msg: "Plan is an enum type", help: "name one of its values, such as `Plan.free`", span: "1:2-1:6"},
		{src: "{Tier: 1}", msg: "Tier is an enum type", span: "1:2-1:6"},

		// Host functions type their arguments.
		{src: "upgrade(standard)", want: "Plan"},
		{src: "upgrade(standard) == enterprise", want: "bool"},
		{src: "upgrade(critical)", msg: "Plan has no value `critical`", help: "Plan declares: free, standard, enterprise", span: "1:9-1:17"},
		{src: `upgrade("free")`, msg: "expected Plan, found string", help: "an enum value is a bare name; write `free`", span: "1:9-1:15"},
		{src: "split(critical, \",\")", msg: "expected string, found Tier", span: "1:7-1:15"},

		// Errors don't cascade: one operand's error is the only report.
		{src: "servce.tier == \"critical\" and cleared", msg: "unknown name `servce`", span: "1:1-1:7"},
		{src: "split(servce.name, \",\") all in actor.regions", msg: "unknown name `servce`", span: "1:7-1:13"},
	}

	for _, tt := range tests {
		t.Run(tt.src, func(t *testing.T) {
			x, perrs := parser.ParseExpr("p.sigil", []byte(tt.src))
			if perrs != nil {
				t.Fatalf("parse: %v", perrs)
			}
			e := env(t)
			e.InAssert = tt.assert
			c := check.New("p.sigil")
			var got types.Type
			if tt.as != nil {
				got = c.ExprAs(x, e, tt.as)
			} else {
				got = c.Expr(x, e)
			}
			errs := c.Errors()

			if tt.msg == "" {
				if errs != nil {
					t.Fatalf("errors:\n%v", errs)
				}
				if got.String() != tt.want {
					t.Errorf("type = %s, want %s", got, tt.want)
				}
				if rec := c.Info().TypeOf(x); rec == nil || rec.String() != tt.want {
					t.Errorf("recorded type = %v, want %s", rec, tt.want)
				}
				return
			}
			if got != types.Invalid {
				t.Errorf("type = %s, want invalid", got)
			}
			if len(errs) != 1 {
				t.Fatalf("got %d errors, want 1:\n%v", len(errs), errs)
			}
			e0 := errs[0]
			if e0.Msg != tt.msg {
				t.Errorf("Msg  = %q\nwant   %q", e0.Msg, tt.msg)
			}
			if tt.help != "" && e0.Help != tt.help {
				t.Errorf("Help = %q\nwant   %q", e0.Help, tt.help)
			}
			if got := e0.Pos.String() + "-" + e0.End.String(); got != tt.span {
				t.Errorf("span = %s, want %s", got, tt.span)
			}
			if e0.File != "p.sigil" {
				t.Errorf("File = %q", e0.File)
			}
		})
	}
}

// TestInfoRecordsEveryNode checks that subexpressions get types too,
// which is what the evaluator relies on.
func TestInfoRecordsEveryNode(t *testing.T) {
	src := `release.soak + 2h >= min_soak and any r in actor.roles: r like "sre-*"`
	x, perrs := parser.ParseExpr("p.sigil", []byte(src))
	if perrs != nil {
		t.Fatal(perrs)
	}
	c := check.New("p.sigil")
	if got := c.Expr(x, env(t)); got != types.Bool {
		t.Fatalf("type = %v, errors %v", got, c.Errors())
	}
	want := map[string]string{
		"release":                           "Release",
		"release.soak":                      "duration",
		"2h":                                "duration",
		"(release.soak + 2h)":               "duration",
		"min_soak":                          "duration",
		"((release.soak + 2h) >= min_soak)": "bool",
		"actor.roles":                       "list<string>",
		"r":                                 "string",
		`(r like "sre-*")`:                  "bool",
		`(any r in actor.roles: (r like "sre-*"))`:                                         "bool",
		`(((release.soak + 2h) >= min_soak) and (any r in actor.roles: (r like "sre-*")))`: "bool",
	}
	var walk func(n ast.Expr)
	walk = func(n ast.Expr) {
		if n == nil {
			return
		}
		if w, ok := want[ast.Sprint(n)]; ok {
			if got := c.Info().TypeOf(n); got == nil || got.String() != w {
				t.Errorf("%s: type %v, want %s", ast.Sprint(n), got, w)
			}
			delete(want, ast.Sprint(n))
		}
		switch n := n.(type) {
		case *ast.BinaryExpr:
			walk(n.X)
			walk(n.Y)
		case *ast.SelectorExpr:
			walk(n.X)
		case *ast.QuantExpr:
			walk(n.Range)
			walk(n.Body)
			walk(n.Var)
		}
	}
	walk(x)
	for k := range want {
		t.Errorf("node %s was not visited", k)
	}
}

func TestEnv(t *testing.T) {
	e := check.NewEnv(loadKind(t))

	t.Run("kind names", func(t *testing.T) {
		for name, ent := range map[string]check.Entity{"release": check.Input, "split": check.Function, "deny": check.DecisionName} {
			b, ok := e.Lookup(name)
			if !ok || b.Entity != ent {
				t.Errorf("Lookup(%q) = %+v, %v; want entity %v", name, b, ok, ent)
			}
		}
		if _, ok := e.Lookup("nope"); ok {
			t.Error("Lookup(nope) found something")
		}
	})

	t.Run("declare refuses taken names", func(t *testing.T) {
		if prev, ok := e.Declare("release", check.Binding{Entity: check.Param, Type: types.Int}); ok || prev.Entity != check.Input {
			t.Errorf("Declare(release) = %+v, %v; want refused by the input", prev, ok)
		}
		if _, ok := e.Declare("min_soak", check.Binding{Entity: check.Param, Type: types.Duration}); !ok {
			t.Error("Declare(min_soak) refused")
		}
		child := e.Child()
		if _, ok := child.Declare("min_soak", check.Binding{Entity: check.QuantVar, Type: types.Int}); ok {
			t.Error("child Declare(min_soak) accepted a shadowing name")
		}
		if _, ok := child.Declare("r", check.Binding{Entity: check.QuantVar, Type: types.Int}); !ok {
			t.Error("child Declare(r) refused")
		}
		if b, ok := child.Lookup("min_soak"); !ok || b.Entity != check.Param {
			t.Errorf("child Lookup(min_soak) = %+v, %v", b, ok)
		}
		if _, ok := e.Lookup("r"); ok {
			t.Error("parent sees the child's variable")
		}
	})

	t.Run("names are sorted", func(t *testing.T) {
		names := e.Names()
		if !sortedStrings(names) || len(names) < 10 {
			t.Errorf("Names() = %v", names)
		}
		if strings.Join(names[:3], ",") != "Plan,Region,Tier" {
			t.Errorf("Names()[:3] = %v", names[:3])
		}
	})

	t.Run("child inherits assert", func(t *testing.T) {
		e.InAssert = true
		if !e.Child().InAssert {
			t.Error("child lost InAssert")
		}
	})

	t.Run("nil kind", func(t *testing.T) {
		if got := check.NewEnv(nil).Names(); len(got) != 0 {
			t.Errorf("Names() = %v", got)
		}
	})
}

func sortedStrings(xs []string) bool {
	for i := 1; i < len(xs); i++ {
		if xs[i-1] > xs[i] {
			return false
		}
	}
	return true
}

// TestAmbiguousEnumValue checks the error for a value several enums
// declare, used where nothing fixes its type: the enums sorted by name,
// and the qualified values to write instead, in declaration order.
func TestAmbiguousEnumValue(t *testing.T) {
	tests := []struct {
		name string
		kind string
		msg  string
		help string
	}{
		{name: "two enums", kind: "enum B: x | z\nenum A: x | y",
			msg: "`x` is a value of A and B", help: "write `B.x` or `A.x`"},
		{name: "three enums", kind: "enum C: x\nenum A: x\nenum B: x",
			msg: "`x` is a value of A, B and C", help: "write `C.x`, `A.x` or `B.x`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, errs := check.LoadKind("k.sigil", []byte("kind K version 1\n"+tt.kind+"\ndecision d { reason: r }\ncollect all"))
			if errs != nil {
				t.Fatal(errs)
			}
			x, perrs := parser.ParseExpr("p.sigil", []byte("x"))
			if perrs != nil {
				t.Fatal(perrs)
			}
			c := check.New("p.sigil")
			if got := c.Expr(x, check.NewEnv(k)); got != types.Invalid {
				t.Errorf("type = %s, want invalid", got)
			}
			errs = c.Errors()
			if len(errs) != 1 || errs[0].Msg != tt.msg || errs[0].Help != tt.help {
				t.Errorf("errors = %v\nwant   %s (help %q)", errs, tt.msg, tt.help)
			}
		})
	}
}
