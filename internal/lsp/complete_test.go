package lsp

import (
	"slices"
	"strings"
	"testing"
)

// head starts the policy the completion tests write: its header and
// imports, so its body sees the kind, a module's lets and a policy to
// invoke.
const head = "policy payments.production: DeployApproval@2\n\nuse deploy.common.{cleared, owns_service}\nuse deploy.guardrails\n\n"

// TestComplete completes at the marker of each source, which replaces
// production.sigil in the test workspace. want lists labels that must be
// offered, in the order offered; exact makes it the whole list. not lists
// labels that must not be.
func TestComplete(t *testing.T) {
	tests := []struct {
		name  string
		file  string // the file the source replaces; production.sigil when empty
		src   string
		want  []string
		not   []string
		exact bool
	}{
		{name: "document start", src: "<|>", want: []string{"module", "policy"}, exact: true},
		{name: "document start after a separator", src: head + "---\n<|>", want: []string{"module", "policy"}, exact: true},
		{name: "kind in the header", src: "policy p: <|>", want: []string{"DeployApproval"}, exact: true},
		{name: "kind being typed", src: "policy p: Dep<|>", want: []string{"DeployApproval"}, exact: true},
		{name: "kind's version", src: "policy p: DeployApproval@<|>", want: []string{"2"}, exact: true},
		{name: "policy name", src: "policy pay<|>", exact: true},
		{name: "statement at the top", src: head + "<|>", want: []string{"guardrails", "assert", "let", "param", "pub let", "use", "when"}, not: []string{"deny", "review", "service"}, exact: true},
		{name: "statement after a let", src: head + "let a = service.name\n<|>", want: []string{"guardrails", "when"}, not: []string{"use"}},
		{name: "statement after a rule", src: head + "when cleared {\n  deny(reason: not_eligible)\n}\n<|>", want: []string{"guardrails", "when"}},
		{name: "statement in a body", src: head + "when cleared {\n  <|>\n}", want: []string{"approve", "deny", "guardrails", "review", "assert", "let", "when"}, not: []string{"param", "use"}, exact: true},
		{name: "statement after a constructor", src: head + "when cleared {\n  deny(reason: not_eligible)\n  <|>\n}", want: []string{"approve", "deny"}},
		{name: "statement in a module", file: "common.sigil", src: "module deploy.common: DeployApproval@2\n\n<|>", want: []string{"let", "pub let", "use"}, exact: true},
		{name: "use path", src: "policy payments.production: DeployApproval@2\n\nuse <|>", want: []string{"deploy.common", "deploy.guardrails"}, exact: true},
		{name: "use path after a dot", src: "policy payments.production: DeployApproval@2\n\nuse deploy.<|>", want: []string{"deploy.common", "deploy.guardrails"}, exact: true},
		{name: "use path being typed", src: "policy payments.production: DeployApproval@2\n\nuse deploy.co<|>", want: []string{"deploy.common"}, exact: true},
		{name: "use alias", src: "policy payments.production: DeployApproval@2\n\nuse deploy.common as <|>", exact: true},
		{name: "use items", src: "policy payments.production: DeployApproval@2\n\nuse deploy.common.{<|>", want: []string{"cleared", "owns_service"}, exact: true},
		{name: "use items after one", src: "policy payments.production: DeployApproval@2\n\nuse deploy.common.{cleared, <|>}", want: []string{"owns_service"}, exact: true},
		{name: "use item alias", src: "policy payments.production: DeployApproval@2\n\nuse deploy.common.{cleared as <|>", exact: true},
		{name: "names in a condition", src: head + "when <|>", want: []string{"cleared", "owns_service", "actor", "changes", "environment", "release", "service", "ticket", "split"}, not: []string{"deny", "guardrails", "outcome", "approve"}},
		{name: "keywords in a condition", src: head + "when <|>", want: []string{"all", "any", "false", "not", "present", "true", "filter"}},
		{name: "a name being typed", src: head + "when serv<|>", want: []string{"service"}, exact: true},
		{name: "enum values in a condition", src: head + "when <|>", want: []string{"critical", "high", "internal", "low", "Risk", "Risk.standard", "Tier", "Tier.standard"}, not: []string{"standard"}},
		{name: "lets", src: head + "let mine = 1\nwhen <|>", want: []string{"mine"}},
		{name: "fields", src: head + "when service.<|>", want: []string{"labels", "name", "owners", "tier"}, exact: true},
		{name: "fields mid-edit before a rule", src: head + "when service.<|>\nwhen true {\n  deny(reason: not_eligible)\n}\n", want: []string{"labels", "name", "owners", "tier"}, exact: true},
		{name: "field being typed", src: head + "when service.ow<|> {\n}\n", want: []string{"owners"}, exact: true},
		{name: "fields of a field", src: head + "when release.risk == Risk.<|>", want: []string{"high", "low", "standard"}, exact: true},
		{name: "fields through an optional", src: head + "let t = ticket?.<|>", want: []string{"approved", "id"}, exact: true},
		{name: "fields of an index", src: head + "when changes[0].<|>", want: []string{"hotfix", "risk", "soak"}, exact: true},
		{name: "fields in parentheses", src: head + "when (release).<|>", want: []string{"hotfix", "risk", "soak"}, exact: true},
		{name: "no fields on a string", src: head + "when environment.<|>", exact: true},
		{name: "enum values", src: head + "when service.tier == Tier.<|>", want: []string{"critical", "internal", "standard"}, exact: true},
		{name: "module lets", src: "policy payments.production: DeployApproval@2\n\nuse deploy.common\n\nwhen common.<|>", want: []string{"cleared", "owns_service"}, exact: true},
		{name: "quantifier variable", src: head + "when any c in changes: c.<|>", want: []string{"hotfix", "risk", "soak"}, exact: true},
		{name: "quantifier variable in scope", src: head + "when all c in changes: <|>", want: []string{"c", "changes"}},
		{name: "quantifier variable out of scope", src: head + "when (any c in changes: c.hotfix) and <|>", not: []string{"c"}},
		{name: "nested quantifiers", src: head + "when any c in changes: any o in service.owners: o == c.<|>", want: []string{"hotfix", "risk", "soak"}, exact: true},
		{name: "quantifier variable in a host call", src: head + "when any c in changes: split(c.<|>", want: []string{"hotfix", "risk", "soak"}, exact: true},
		{name: "quantifier closed by a comma", src: head + "when cleared {\n  review(reason: service_owner, approvers: filter o in service.owners: o != \"\", tier: <|>", not: []string{"o"}},
		{name: "a stray closing brace", src: head + "}\nwhen service.<|>", want: []string{"labels", "name", "owners", "tier"}, exact: true},
		{name: "a dot after an unopened bracket", src: head + "when x).<|>", exact: true},
		{name: "a dot after a string", src: head + "when \"a\".<|>", exact: true},
		{name: "a dot after a list", src: head + "when [service].<|>", exact: true},
		{name: "fields after a call", src: head + "let first = split(environment, \",\")[0]\nwhen release.<|>", want: []string{"hotfix", "risk", "soak"}, exact: true},
		{name: "filter variable", src: head + "let mine = filter o in service.owners: <|>", want: []string{"o"}},
		{name: "filter variable in a parsed rule", src: head + "when cleared {\n  let reviewers = filter o in service.owners: actor.name != <|>o\n}\n", want: []string{"o", "reviewers"}},
		{name: "lets of a body", src: head + "when cleared {\n  let reviewers = service.owners\n  when <|>\n}\n", want: []string{"reviewers"}},
		{name: "operators after an operand", src: head + "when service.name <|>", want: []string{"like", "matches", "in", "not in", "==", "!="}, exact: true},
		{name: "operator being typed", src: head + "when cleared an<|>", want: []string{"and"}, exact: true},
		{name: "operators after an operand of no type", src: head + "when nope <|>", want: []string{"and", "or", "xor", "in", "not in", "has", "like", "matches", "all in", "any in", "one in", "exclusive in"}, exact: true},
		{name: "reason", src: head + "when cleared {\n  deny(reason: <|>)\n}", want: []string{"no_rule_matched", "not_eligible", "soak_too_short"}, exact: true},
		{name: "reason being typed", src: head + "when cleared {\n  approve(reason: pay<|>", want: []string{"payments_sre"}, exact: true},
		{name: "payload keys", src: head + "when cleared {\n  review(<|>", want: []string{"approvers", "reason", "tier"}, exact: true},
		{name: "payload keys after the reason", src: head + "when cleared {\n  review(reason: service_owner, <|>)\n}", want: []string{"approvers", "tier"}, exact: true},
		{name: "payload value of an enum", src: head + "when cleared {\n  review(reason: service_owner, approvers: [], tier: <|>", want: []string{"critical", "internal", "Tier.standard"}, not: []string{"standard"}},
		{name: "payload value", src: head + "when cleared {\n  approve(reason: release_manager, bake: <|>", want: []string{"release", "service"}},
		{name: "invocation params", src: head + "guardrails(<|>)", want: []string{"min_soak"}, exact: true},
		{name: "invocation params given", src: head + "guardrails(min_soak: 1h, <|>)", exact: true},
		{name: "invocation value", src: head + "guardrails(min_soak: <|>", want: []string{"1h"}, exact: true},
		{name: "host function arguments", src: head + "when split(<|>", want: []string{"environment", "service"}},
		{name: "outcome in an assert", src: head + "assert(\"x\", <|>", want: []string{"approve", "deny", "review", "outcome"}},
		{name: "no outcome outside an assert", src: head + "let x = <|>", not: []string{"outcome", "deny"}},
		{name: "decisions after outcome", src: head + "assert(\"x\", outcome.<|>", want: []string{"approve", "deny", "review"}, exact: true},
		{name: "reasons of candidates", src: head + "assert(\"x\", outcome.approve.<|>", want: []string{"payments_sre", "release_manager"}, exact: true},
		{name: "candidate fields", src: head + "assert(\"x\", all r in outcome.review: r.<|>", want: []string{"approvers", "reason", "tier"}, exact: true},
		{name: "reasons of a decision value", src: head + "assert(\"x\", approve.<|>", want: []string{"payments_sre", "release_manager"}, exact: true},
		{name: "assert reason", src: head + "assert(<|>", exact: true},
		{name: "param type", src: head + "param p: <|>", want: []string{"Actor", "bool", "duration", "float", "int", "list", "map", "Release", "Risk", "Service", "string", "Ticket", "Tier", "timestamp"}, exact: true},
		{name: "param type in a list", src: head + "param p: list<<|>", want: []string{"Tier"}},
		{name: "param name", src: head + "param <|>", exact: true},
		{name: "param bounds", src: head + "param p: int = 1, <|>", want: []string{"max", "min"}, exact: true},
		{name: "param default", src: head + "param p: Tier = <|>", want: []string{"critical"}},
		{name: "a bool param's default", src: head + "param b: bool = <|>", want: []string{"false", "true"}, exact: true},
		{name: "let name", src: head + "let <|>", exact: true},
		{name: "comment", src: head + "// when <|>", exact: true},
		{name: "string", src: head + "let s = \"serv<|>\"", exact: true},
		{name: "kind document", file: "deploy_approval.sigil", src: "kind DeployApproval version 2\n\ninput x: <|>", exact: true},
		{name: "a use of the kind", src: "policy p: DeployApproval@2\n\nuse DeployApproval\nuse deploy.nope\n\nwhen serv<|>", want: []string{"service"}, exact: true},
		{name: "unknown kind", src: "policy p: Nope@1\n\nwhen <|>", want: []string{"any", "not"}, exact: false, not: []string{"service"}},
		{name: "a header that doesn't parse", src: "policy p: DeployApproval@2 extra\n\nwhen service.<|>", want: []string{"labels", "name", "owners", "tier"}, exact: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := tt.file
			if file == "" {
				file = "production.sigil"
			}
			v, offset := viewOf(t, file, tt.src)
			items, _, _ := v.complete(offset)
			var got []string
			for _, it := range items {
				got = append(got, it.label)
			}
			for _, label := range tt.not {
				if slices.Contains(got, label) {
					t.Errorf("offered %q: %v", label, got)
				}
			}
			if tt.exact {
				if !slices.Equal(got, tt.want) {
					t.Errorf("completions = %v, want %v", got, tt.want)
				}
				return
			}
			at := -1
			for _, label := range tt.want {
				i := slices.Index(got, label)
				if i < 0 {
					t.Errorf("didn't offer %q: %v", label, got)
					continue
				}
				if i < at {
					t.Errorf("offered %q before %q: %v", label, tt.want[slices.Index(tt.want, label)-1], got)
				}
				at = i
			}
		})
	}
}

// TestCompleteReplaces checks the span a completion replaces: the whole
// name the cursor is in, the part after the cursor too, or the whole
// dotted name after `use`, and what each item inserts.
func TestCompleteReplaces(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		label  string
		from   string // the source before the span's start
		to     string // the source before the span's end; the source before the cursor when empty
		insert string
	}{
		{name: "a name", src: head + "when serv<|>", label: "service", from: head + "when ", insert: "service"},
		{name: "the dotted name after use", src: "policy p: DeployApproval@2\n\nuse deploy.co<|>", label: "deploy.common", from: "policy p: DeployApproval@2\n\nuse ", insert: "deploy.common"},
		{name: "an argument name", src: head + "when cleared {\n  review(<|>", label: "approvers", from: head + "when cleared {\n  review(", insert: "approvers: "},
		{name: "a field", src: head + "when service.na<|>", label: "name", from: head + "when service.", insert: "name"},
		{name: "inside a name", src: head + "when actor.te<|>ams {\n}\n", label: "teams", from: head + "when actor.", to: head + "when actor.teams", insert: "teams"},
		{name: "right after a dot, before a name", src: head + "when actor.<|>teams {\n}\n", label: "name", from: head + "when actor.", to: head + "when actor.teams", insert: "name"},
		{name: "before a name after a space", src: head + "when <|>cleared {\n}\n", label: "service", from: head + "when ", to: head + "when cleared", insert: "service"},
		{name: "inside a dotted name after use", src: "policy p: DeployApproval@2\n\nuse deploy.co<|>mmon\n", label: "deploy.common", from: "policy p: DeployApproval@2\n\nuse ", to: "policy p: DeployApproval@2\n\nuse deploy.common", insert: "deploy.common"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, offset := viewOf(t, "production.sigil", tt.src)
			items, from, to := v.complete(offset)
			if from != len(tt.from) {
				t.Errorf("replaces from %d, want %d", from, len(tt.from))
			}
			want := len(tt.to)
			if tt.to == "" {
				want = offset
			}
			if to != want {
				t.Errorf("replaces to %d, want %d", to, want)
			}
			i := slices.IndexFunc(items, func(it item) bool { return it.label == tt.label })
			if i < 0 {
				t.Fatalf("didn't offer %q", tt.label)
			}
			insert := items[i].insert
			if insert == "" {
				insert = items[i].label
			}
			if insert != tt.insert {
				t.Errorf("inserts %q, want %q", insert, tt.insert)
			}
		})
	}
}

// TestCompleteDetails checks what completions say about themselves.
func TestCompleteDetails(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		label  string
		detail string
		doc    string // a substring of the documentation
	}{
		{name: "input", src: head + "when <|>", label: "service", detail: "Service"},
		{name: "function", src: head + "when <|>", label: "split", detail: "fn split(string, string) -> list<string>"},
		{name: "imported let", src: head + "when <|>", label: "cleared", detail: "bool", doc: "from deploy.common"},
		{name: "decision", src: head + "when cleared {\n  <|>", label: "review", detail: "review takes reason: service_owner, approvers: list<string>, and tier: Tier = standard", doc: "decision review {"},
		{name: "invocable", src: head + "<|>", label: "guardrails", detail: "policy deploy.guardrails", doc: "param min_soak: duration = 24h, min: 1h, max: 48h"},
		{name: "payload field", src: head + "when cleared {\n  review(<|>", label: "tier", detail: "tier: Tier = standard"},
		{name: "reason key", src: head + "when cleared {\n  review(<|>", label: "reason", detail: "reason: service_owner"},
		{name: "param", src: head + "guardrails(<|>", label: "min_soak", detail: "min_soak: duration = 24h, min: 1h, max: 48h"},
		{name: "module path", src: "policy p: DeployApproval@2\n\nuse <|>", label: "deploy.common", detail: "module deploy.common: DeployApproval@2"},
		{name: "kind", src: "policy p: <|>", label: "DeployApproval", detail: "kind DeployApproval version 2"},
		{name: "struct type", src: head + "param p: <|>", label: "Ticket", doc: "type Ticket {\n  id: string\n  approved: bool\n}"},
		{name: "use item", src: "policy p: DeployApproval@2\n\nuse deploy.common.{<|>", label: "cleared", detail: "bool"},
		{name: "field", src: head + "when service.<|>", label: "owners", detail: "list<string>"},
		{name: "candidates", src: head + "assert(\"x\", outcome.<|>", label: "review", detail: "list<review candidate>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, offset := viewOf(t, "production.sigil", tt.src)
			items, _, _ := v.complete(offset)
			i := slices.IndexFunc(items, func(it item) bool { return it.label == tt.label })
			if i < 0 {
				t.Fatalf("didn't offer %q", tt.label)
			}
			if tt.detail != "" && items[i].detail != tt.detail {
				t.Errorf("detail = %q, want %q", items[i].detail, tt.detail)
			}
			if !strings.Contains(items[i].doc, tt.doc) {
				t.Errorf("documentation = %q, want it to hold %q", items[i].doc, tt.doc)
			}
		})
	}
}

// TestCompleteExpected completes an operand where the context expects a
// type, and checks the labels offered first, in order, and the one
// preselected: what has the type comes first, then the rest.
func TestCompleteExpected(t *testing.T) {
	tests := []struct {
		name  string
		src   string
		first []string // the labels offered first, in order
		best  string   // the label preselected; "" for none
		not   []string // labels that must not be offered
	}{
		{name: "a condition", src: "when <|>", first: []string{"cleared", "owns_service", "release.hotfix", "all", "any", "false", "not", "present", "true", "actor"}, best: "cleared"},
		{name: "after not", src: "when not <|>", first: []string{"cleared", "owns_service", "release.hotfix"}, best: "cleared"},
		{name: "after and", src: "when release.soak < 4h and <|>", first: []string{"cleared", "owns_service", "release.hotfix"}, best: "cleared"},
		{name: "an enum on the left", src: "when service.tier == <|>", first: []string{"critical", "internal", "Tier.standard", "cleared"}, best: "critical", not: []string{"service.tier"}},
		{name: "a duration on the left", src: "when release.soak > <|>", first: []string{"1h", "cleared"}, best: "1h", not: []string{"release.soak"}},
		{name: "a string on the left", src: "when environment == <|>", first: []string{`""`, "actor.name", "service.name", "cleared"}, best: `""`, not: []string{"environment"}},
		{name: "a bool on the left", src: "when release.hotfix != <|>", first: []string{"cleared", "owns_service", "all", "any", "false"}, best: "cleared"},
		{name: "an index on the left", src: "when changes[0].risk == <|>", first: []string{"high", "low", "Risk.standard", "release.risk"}, best: "high"},
		{name: "a string in a list", src: "when environment in <|>", first: []string{"[]", "split", "actor.roles", "actor.teams", "service.owners", "cleared"}, best: "[]"},
		{name: "a literal in a list", src: `when "x" in <|>`, first: []string{"[]", "split", "actor.roles"}, best: "[]"},
		{name: "an enum in a list", src: "when service.tier in <|>", first: []string{"[]", "cleared"}, best: "[]"},
		{name: "a list on the left", src: "when actor.teams any in <|>", first: []string{"[]", "split", "actor.roles", "service.owners", "cleared"}, best: "[]", not: []string{"actor.teams"}},
		{name: "a map key", src: "when service.labels has <|>", first: []string{`""`, "environment", "actor.name", "service.name"}, best: `""`},
		{name: "an index of a map", src: "when service.labels[<|>", first: []string{`""`, "environment"}, best: `""`},
		{name: "an index of a list", src: "when changes[<|>", first: []string{"cleared"}},
		{name: "a lookup on the left", src: `when service.labels["a"] == <|>`, first: []string{`""`, "environment"}, best: `""`},
		{name: "an optional on the left", src: "when ticket ?? <|>", first: []string{"cleared"}, not: []string{"ticket"}},
		{name: "a pattern", src: "when environment like <|>", first: []string{`""`, "actor.name", "service.name"}, best: `""`, not: []string{"environment"}},
		{name: "a host function argument", src: "when split(<|>", first: []string{`""`, "environment", "actor.name", "service.name", "cleared"}, best: `""`},
		{name: "a second host function argument", src: "when split(environment, <|>", first: []string{`""`, "environment"}, best: `""`},
		{name: "a duration field", src: "when cleared {\n  approve(reason: release_manager, bake: <|>", first: []string{"1h", "release.soak", "cleared"}, best: "1h"},
		{name: "a list field", src: "when cleared {\n  review(reason: service_owner, approvers: <|>", first: []string{"[]", "split", "actor.roles", "actor.teams", "service.owners"}, best: "[]"},
		{name: "an enum field", src: "when cleared {\n  review(reason: service_owner, tier: <|>", first: []string{"critical", "internal", "Tier.standard", "service.tier"}, best: "critical"},
		{name: "an invocation argument", src: "guardrails(min_soak: <|>", first: []string{"1h"}, best: "1h", not: []string{"release", "release.soak", "cleared", "critical"}},
		{name: "an assert condition", src: `assert("x", <|>`, first: []string{"cleared", "owns_service", "release.hotfix", "all", "any", "false", "not", "present", "true"}, best: "cleared"},
		{name: "a quantifier body", src: "when all c in changes: <|>", first: []string{"c.hotfix", "cleared", "owns_service", "release.hotfix"}, best: "c.hotfix"},
		{name: "a quantifier variable", src: "when any c in changes: c.risk == <|>", first: []string{"high", "low", "Risk.standard", "release.risk"}, best: "high"},
		{name: "a param's default", src: "param p: duration = <|>", first: []string{"1h"}, best: "1h", not: []string{"release", "cleared", "release.soak", "Risk", "critical", "true"}},
		{name: "an enum param's default", src: "param p: Tier = <|>", first: []string{"critical", "internal", "Tier.standard"}, best: "critical", not: []string{"service.tier"}},
		{name: "a list param's default", src: "param p: list<Tier> = <|>", first: []string{"[]"}, best: "[]", not: []string{"critical", "service.tier"}},
		{name: "a map param's default", src: "param p: map<string, int> = <|>", first: []string{"{}"}, best: "{}"},
		{name: "a param's bound", src: "param p: duration = 1h, min: <|>", first: []string{"1h"}, best: "1h"},
		{name: "a map on the left", src: "when service.labels == <|>", first: []string{"{}"}, best: "{}"},
		{name: "an unknown name on the left", src: "when nope == <|>", first: []string{"cleared", "owns_service", "actor"}},
		{name: "a let", src: "let x = <|>", first: []string{"cleared", "owns_service", "actor"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, offset := viewOf(t, "production.sigil", head+tt.src)
			items, _, _ := v.complete(offset)
			var got []string
			best := ""
			for _, it := range items {
				got = append(got, it.label)
				if it.best {
					best = it.label
				}
			}
			if len(got) < len(tt.first) || !slices.Equal(got[:len(tt.first)], tt.first) {
				t.Errorf("offered first %v, want %v", got[:min(len(got), len(tt.first))], tt.first)
			}
			if best != tt.best {
				t.Errorf("preselected %q, want %q", best, tt.best)
			}
			for _, label := range tt.not {
				if slices.Contains(got, label) {
					t.Errorf("offered %q: %v", label, got)
				}
			}
		})
	}
}

// TestCompleteOperators completes the operator after an operand, which
// its type picks, and the unit of a duration being typed.
func TestCompleteOperators(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{name: "a list", src: "when service.owners <|>", want: []string{"any in", "all in", "one in", "exclusive in"}},
		{name: "a map", src: "when service.labels <|>", want: []string{"has"}},
		{name: "a string", src: "when environment <|>", want: []string{"like", "matches", "in", "not in", "==", "!="}},
		{name: "a string literal", src: `when "a" <|>`, want: []string{"like", "matches", "in", "not in", "==", "!="}},
		{name: "a string variable", src: "when any r in actor.roles: r <|>", want: []string{"like", "matches", "in", "not in", "==", "!="}},
		{name: "an optional struct", src: "when ticket <|>", want: []string{"??", "?."}},
		{name: "an optional field", src: "when ticket?.approved <|>", want: []string{"??"}},
		{name: "a duration", src: "when release.soak <|>", want: []string{"<", "<=", ">", ">=", "==", "!=", "in", "not in", "+", "-"}},
		{name: "a duration in parentheses", src: "when (release.soak) <|>", want: []string{"<", "<=", ">", ">=", "==", "!=", "in", "not in", "+", "-"}},
		{name: "an int", src: "when 3 <|>", want: []string{"<", "<=", ">", ">=", "==", "!=", "in", "not in", "+", "-"}},
		{name: "a bool", src: "when cleared <|>", want: []string{"and", "or", "xor", "==", "!="}},
		{name: "a bool literal", src: "when true <|>", want: []string{"and", "or", "xor", "==", "!="}},
		{name: "an enum", src: "when service.tier <|>", want: []string{"==", "!=", "in", "not in"}},
		{name: "a call", src: `when split(environment, ",") <|>`, want: []string{"any in", "all in", "one in", "exclusive in"}},
		{name: "a list literal", src: `when ["a"] <|>`, want: []string{"any in", "all in", "one in", "exclusive in"}},
		{name: "a struct", src: "when service <|>"},
		{name: "a unit after a duration's number", src: "when release.soak < 4<|>", want: []string{"4d", "4h", "4m", "4s", "4ms"}},
		{name: "a unit in a duration field", src: "when cleared {\n  approve(reason: release_manager, bake: 2<|>", want: []string{"2d", "2h", "2m", "2s", "2ms"}},
		{name: "a unit in an invocation", src: "guardrails(min_soak: 12<|>", want: []string{"12d", "12h", "12m", "12s", "12ms"}},
		{name: "a unit in a param's default", src: "param p: duration = 3<|>", want: []string{"3d", "3h", "3m", "3s", "3ms"}},
		{name: "no unit after an int", src: "when changes[0<|>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, offset := viewOf(t, "production.sigil", head+tt.src)
			items, _, _ := v.complete(offset)
			var got []string
			for _, it := range items {
				got = append(got, it.label)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("completions = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCompleteSnippets checks the snippet each completion inserts for a
// client that takes snippets.
func TestCompleteSnippets(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		label   string
		snippet string
	}{
		{name: "when", src: "<|>", label: "when", snippet: "when ${1:true} {\n\t$0\n}"},
		{name: "assert", src: "<|>", label: "assert", snippet: "assert(\"${1:reason}\", ${2:true})"},
		{name: "use", src: "<|>", label: "use", snippet: "use ${1:deploy.common}"},
		{name: "an invocation with no required param", src: "<|>", label: "guardrails", snippet: "guardrails($0)"},
		{name: "a constructor with one reason", src: "when cleared {\n  <|>", label: "review", snippet: "review(reason: service_owner, approvers: ${1:[]})"},
		{name: "a constructor with several reasons", src: "when cleared {\n  <|>", label: "deny", snippet: "deny(reason: ${1:not_eligible})"},
		{name: "a constructor whose fields have defaults", src: "when cleared {\n  <|>", label: "approve", snippet: "approve(reason: ${1:release_manager})"},
		{name: "any", src: "when <|>", label: "any", snippet: "any ${1:x} in ${2:changes}: ${3:true}"},
		{name: "all", src: "when <|>", label: "all", snippet: "all ${1:x} in ${2:changes}: ${3:true}"},
		{name: "filter", src: "let x = <|>", label: "filter", snippet: "filter ${1:x} in ${2:changes}: ${3:true}"},
		{name: "a host function", src: "when <|>", label: "split", snippet: "split(${1:\"\"}, ${2:\"\"})"},
		{name: "a duration", src: "when release.soak > <|>", label: "1h", snippet: "${1:1}${2:h}"},
		{name: "a string", src: "when environment == <|>", label: `""`, snippet: `"$0"`},
		{name: "a list", src: "when environment in <|>", label: "[]", snippet: "[$0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, offset := viewOf(t, "production.sigil", head+tt.src)
			items, _, _ := v.complete(offset)
			i := slices.IndexFunc(items, func(it item) bool { return it.label == tt.label })
			if i < 0 {
				t.Fatalf("didn't offer %q", tt.label)
			}
			if items[i].snippet != tt.snippet {
				t.Errorf("snippet = %q, want %q", items[i].snippet, tt.snippet)
			}
		})
	}
}

// TestCompleteInvocationSnippet checks that an invocation's snippet fills
// the params without a default, and the escapes a snippet needs.
func TestCompleteInvocationSnippet(t *testing.T) {
	files := testWorkspace(t)
	files[root+"/guardrails.sigil"] = []byte("policy deploy.guardrails: DeployApproval@2\n\nparam min_soak: duration\nparam teams: list<string>\nparam tier: Tier = Tier.critical\n\nwhen release.soak < min_soak {\n  deny(reason: soak_too_short)\n}\n")
	l := &memLoader{files: files}
	src := head
	snap := l.Load(root, map[string][]byte{root + "/production.sigil": []byte(src)})
	items, _, _ := newView(snap.Project, root+"/production.sigil", []byte(src)).complete(len(src))
	i := slices.IndexFunc(items, func(it item) bool { return it.label == "guardrails" })
	if i < 0 {
		t.Fatal("didn't offer guardrails")
	}
	if want := "guardrails(min_soak: ${1:0s}, teams: ${2:[]})"; items[i].snippet != want {
		t.Errorf("snippet = %q, want %q", items[i].snippet, want)
	}
	if got := escapePlaceholder(`$x}`); got != `\$x\}` {
		t.Errorf("escapePlaceholder = %q", got)
	}
}

// TestCompleteDocs checks the doc comments completions show, read from
// the comment above each declaration in the kind file and the module, and
// what they say they are.
func TestCompleteDocs(t *testing.T) {
	files := commented(t)
	tests := []struct {
		name  string
		src   string
		label string
		doc   string // a substring of the documentation
		desc  string
	}{
		{name: "an input", src: "when <|>", label: "service", doc: "The service being deployed.\n\nIts owners review.", desc: "input"},
		{name: "a host function", src: "when <|>", label: "split", doc: "split cuts a string at a separator.", desc: "host function"},
		{name: "a decision", src: "when cleared {\n  <|>", label: "review", doc: "review asks a person.", desc: "decision"},
		{name: "a payload field", src: "when cleared {\n  review(<|>", label: "approvers", doc: "Who reviews."},
		{name: "a struct field", src: "when service.<|>", label: "owners", doc: "The teams that own it."},
		{name: "an imported let", src: "when <|>", label: "owns_service", doc: "Whether the actor owns the service.", desc: "from deploy.common"},
		{name: "a let without a comment", src: "when <|>", label: "cleared", doc: "from deploy.common", desc: "from deploy.common"},
		{name: "a use item", src: "<|>", label: "owns_service"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := strings.Replace(head+tt.src, marker, "", 1)
			if tt.name == "a use item" {
				src = "policy p: DeployApproval@2\n\nuse deploy.common.{"
				tt.doc = "Whether the actor owns the service."
			}
			l := &memLoader{files: files}
			snap := l.Load(root, map[string][]byte{root + "/production.sigil": []byte(src)})
			items, _, _ := newView(snap.Project, root+"/production.sigil", []byte(src)).complete(len(src))
			i := slices.IndexFunc(items, func(it item) bool { return it.label == tt.label })
			if i < 0 {
				t.Fatalf("didn't offer %q", tt.label)
			}
			if !strings.Contains(items[i].doc, tt.doc) {
				t.Errorf("documentation = %q, want it to hold %q", items[i].doc, tt.doc)
			}
			if items[i].desc != tt.desc {
				t.Errorf("description = %q, want %q", items[i].desc, tt.desc)
			}
		})
	}
}
