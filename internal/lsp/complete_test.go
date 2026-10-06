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
		{name: "statement after a let", src: head + "let a = service.name\n<|>", want: []string{"guardrails", "when"}},
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
		{name: "names in a condition", src: head + "when <|>", want: []string{"actor", "changes", "cleared", "environment", "owns_service", "release", "service", "split", "ticket"}, not: []string{"deny", "guardrails", "outcome", "approve"}},
		{name: "keywords in a condition", src: head + "when <|>", want: []string{"all", "any", "false", "filter", "not", "present", "true"}},
		{name: "a name being typed", src: head + "when serv<|>", want: []string{"service"}, exact: true},
		{name: "enum values in a condition", src: head + "when <|>", want: []string{"Risk", "Risk.standard", "Tier", "Tier.standard", "critical", "high", "internal", "low"}, not: []string{"standard"}},
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
		{name: "filter variable", src: head + "let mine = filter o in service.owners: o == <|>", want: []string{"o"}},
		{name: "filter variable in a parsed rule", src: head + "when cleared {\n  let reviewers = filter o in service.owners: o != <|>actor.name\n}\n", want: []string{"o", "reviewers"}},
		{name: "lets of a body", src: head + "when cleared {\n  let reviewers = service.owners\n  when <|>\n}\n", want: []string{"reviewers"}},
		{name: "operators after an operand", src: head + "when service.name <|>", want: []string{"all in", "and", "any in", "exclusive in", "has", "in", "like", "matches", "not in", "one in", "or", "xor"}, exact: true},
		{name: "operator being typed", src: head + "when cleared an<|>", want: []string{"and", "any in"}, exact: true},
		{name: "reason", src: head + "when cleared {\n  deny(reason: <|>)\n}", want: []string{"no_rule_matched", "not_eligible", "soak_too_short"}, exact: true},
		{name: "reason being typed", src: head + "when cleared {\n  approve(reason: pay<|>", want: []string{"payments_sre"}, exact: true},
		{name: "payload keys", src: head + "when cleared {\n  review(<|>", want: []string{"approvers", "reason", "tier"}, exact: true},
		{name: "payload keys after the reason", src: head + "when cleared {\n  review(reason: service_owner, <|>)\n}", want: []string{"approvers", "tier"}, exact: true},
		{name: "payload value of an enum", src: head + "when cleared {\n  review(reason: service_owner, approvers: [], tier: <|>", want: []string{"Tier.standard", "critical", "internal"}, not: []string{"standard"}},
		{name: "payload value", src: head + "when cleared {\n  approve(reason: release_manager, bake: <|>", want: []string{"release", "service"}},
		{name: "invocation params", src: head + "guardrails(<|>)", want: []string{"min_soak"}, exact: true},
		{name: "invocation params given", src: head + "guardrails(min_soak: 1h, <|>)", exact: true},
		{name: "invocation value", src: head + "guardrails(min_soak: <|>", want: []string{"critical"}},
		{name: "host function arguments", src: head + "when split(<|>", want: []string{"environment", "service"}},
		{name: "outcome in an assert", src: head + "assert(\"x\", <|>", want: []string{"approve", "deny", "review", "outcome"}},
		{name: "no outcome outside an assert", src: head + "let x = <|>", not: []string{"outcome", "deny"}},
		{name: "decisions after outcome", src: head + "assert(\"x\", outcome.<|>", want: []string{"approve", "deny", "review"}, exact: true},
		{name: "reasons of candidates", src: head + "assert(\"x\", outcome.approve.<|>", want: []string{"payments_sre", "release_manager"}, exact: true},
		{name: "candidate fields", src: head + "assert(\"x\", all r in outcome.review: r.<|>", want: []string{"approvers", "reason", "tier"}, exact: true},
		{name: "reasons of a decision value", src: head + "assert(\"x\", approve.<|>", want: []string{"payments_sre", "release_manager"}, exact: true},
		{name: "assert reason", src: head + "assert(<|>", exact: true},
		{name: "param type", src: head + "param p: <|>", want: []string{"Actor", "Release", "Risk", "Service", "Ticket", "Tier", "bool", "duration", "float", "int", "list", "map", "string", "timestamp"}, exact: true},
		{name: "param type in a list", src: head + "param p: list<<|>", want: []string{"Tier"}},
		{name: "param name", src: head + "param <|>", exact: true},
		{name: "param bounds", src: head + "param p: int = 1, <|>", want: []string{"max", "min"}, exact: true},
		{name: "param default", src: head + "param p: Tier = <|>", want: []string{"critical"}},
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
