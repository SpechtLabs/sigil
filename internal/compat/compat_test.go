package compat_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/compat"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// deploy is the old kind most cases change: a `collect one` kind with
// two enums, struct types, a host function, ranked reasons, an exclusive
// set, a default and a conflict outcome.
const deploy = `kind DeployApproval version 3, accepts: 2

enum Tier: critical | standard | internal
enum Plan: free | premium

type Release {
  soak: duration
  hotfix: bool
}

type Service {
  name: string
  tier: Tier
  plan: Plan
  team: string
}

input release: Release
input service: Service
input environment: string

fn split(string, string) -> list<string>
fn owner(string) -> string

decision deny {
  reason: not_eligible | no_release | no_rule_matched | conflicting_rules
}

decision review {
  reason: service_owner
  approvers: list<string>
}

decision approve {
  reason: release_manager | payments_sre
  bake: duration = 1h
}

collect one
precedence deny > review > approve
precedence deny: not_eligible > no_release > no_rule_matched > conflicting_rules
exclusive review, approve.payments_sre
exclusive deny.not_eligible, approve

default deny(reason: no_rule_matched)
conflict deny(reason: conflicting_rules)
`

// access is the old kind of the `collect all` cases: no ranking, and a
// default whose decision has a payload field with a default.
const access = `kind AccessGrant version 1

input team: string

decision read {
  reason: member
}

decision admin {
  reason: oncall
  ttl: duration = 8h
}

collect all

default admin(reason: oncall)
`

// TestCompare covers every row of the compatibility table, and each
// change the table doesn't name, with the changes Compare reports as
// `op class path`.
func TestCompare(t *testing.T) {
	tests := []struct {
		name string
		old  string // deploy when empty
		next string
		want []string
	}{
		// Compatible additions.
		{name: "input added", next: edit(deploy, "input environment: string\n", "input environment: string\ninput region: string\n"),
			want: []string{"added compatible input region"}},
		{name: "type field added", next: edit(deploy, "  hotfix: bool\n", "  hotfix: bool\n  commit: string\n"),
			want: []string{"added compatible type Release field commit"}},
		{name: "type added", next: edit(deploy, "\ninput release", "\ntype Actor {\n  name: string\n}\n\ninput release"),
			want: []string{"added compatible type Actor"}},
		{name: "function added", next: edit(deploy, "fn owner(string) -> string\n", "fn owner(string) -> string\nfn now() -> timestamp\n"),
			want: []string{"added compatible fn now"}},
		{name: "decision added, ranked anywhere", next: edit(edit(deploy, "\ncollect one", "\ndecision escalate {\n  reason: incident\n}\n\ncollect one"), "precedence deny > review > approve", "precedence escalate > deny > review > approve"),
			want: []string{"added compatible decision escalate"}},
		{name: "reason added, ranked anywhere", next: edit(edit(deploy, "conflicting_rules\n}", "conflicting_rules | frozen\n}"), "precedence deny: not_eligible >", "precedence deny: frozen > not_eligible >"),
			want: []string{"added compatible decision deny reason frozen"}},
		{name: "enum added", next: edit(deploy, "enum Plan: free | premium\n", "enum Plan: free | premium\nenum Region: eu | us\n"),
			want: []string{"added compatible enum Region"}},
		{name: "enum value added", next: edit(deploy, "critical | standard | internal", "critical | standard | internal | batch"),
			want: []string{"added compatible enum Tier value batch"}},
		{name: "payload field added with a default", next: edit(deploy, "  bake: duration = 1h\n", "  bake: duration = 1h\n  notify: bool = false\n"),
			want: []string{"added compatible decision approve field notify"}},
		{name: "payload field gained a default", next: edit(deploy, "approvers: list<string>", "approvers: list<string> = []"),
			want: []string{"changed compatible decision review field approvers"}},
		{name: "two new enums share a value no old enum declares", next: edit(deploy, "enum Plan: free | premium\n", "enum Plan: free | premium\nenum Size: small | huge\nenum Stage: build | huge\n"),
			want: []string{"added compatible enum Size", "added compatible enum Stage"}},

		// Ambiguity.
		{name: "value added that another enum declares", next: edit(deploy, "free | premium", "free | standard | premium"),
			want: []string{"ambiguous breaking enum Plan value standard"}},
		{name: "new enum overlaps an existing one", next: edit(deploy, "enum Plan: free | premium\n", "enum Plan: free | premium\nenum Support: basic | standard\n"),
			want: []string{"added compatible enum Support", "ambiguous breaking enum Support value standard"}},
		{name: "value already ambiguous", old: edit(deploy, "free | premium", "free | standard"), next: edit(deploy, "free | premium", "free | standard\nenum Support: standard"),
			want: []string{"added compatible enum Support"}},

		// Reorders.
		{name: "enum values reordered", next: edit(deploy, "critical | standard | internal", "internal | critical | standard"),
			want: []string{"reordered compatible enum Tier values"}},
		{name: "enums reordered", next: edit(deploy, "enum Tier: critical | standard | internal\nenum Plan: free | premium\n", "enum Plan: free | premium\nenum Tier: critical | standard | internal\n"),
			want: []string{"reordered compatible enums"}},
		{name: "type fields reordered", next: edit(deploy, "  soak: duration\n  hotfix: bool\n", "  hotfix: bool\n  soak: duration\n"),
			want: []string{"reordered compatible type Release fields"}},
		{name: "types reordered", next: edit(edit(deploy, "type Release {\n  soak: duration\n  hotfix: bool\n}\n\n", ""), "\ninput release", "\ntype Release {\n  soak: duration\n  hotfix: bool\n}\n\ninput release"),
			want: []string{"reordered compatible types"}},
		{name: "inputs reordered", next: edit(deploy, "input release: Release\ninput service: Service\n", "input service: Service\ninput release: Release\n"),
			want: []string{"reordered compatible inputs"}},
		{name: "functions reordered", next: edit(deploy, "fn split(string, string) -> list<string>\nfn owner(string) -> string\n", "fn owner(string) -> string\nfn split(string, string) -> list<string>\n"),
			want: []string{"reordered compatible functions"}},
		{name: "decisions reordered", next: edit(edit(deploy, "decision review {\n  reason: service_owner\n  approvers: list<string>\n}\n\n", ""), "\ncollect one", "\ndecision review {\n  reason: service_owner\n  approvers: list<string>\n}\n\ncollect one"),
			want: []string{"reordered compatible decisions"}},
		{name: "reasons reordered", next: edit(deploy, "reason: release_manager | payments_sre", "reason: payments_sre | release_manager"),
			want: []string{"reordered compatible decision approve reasons"}},
		{name: "payload fields reordered", next: edit(deploy, "  bake: duration = 1h\n", "  bake: duration = 1h\n  notify: bool = false\n"), old: edit(deploy, "  bake: duration = 1h\n", "  notify: bool = false\n  bake: duration = 1h\n"),
			want: []string{"reordered compatible decision approve fields"}},
		{name: "exclusive set reordered", next: edit(deploy, "exclusive review, approve.payments_sre", "exclusive approve.payments_sre, review"),
			want: []string{"reordered compatible exclusive approve.payments_sre, review"}},
		{name: "exclusive sets reordered", next: edit(deploy, "exclusive review, approve.payments_sre\nexclusive deny.not_eligible, approve\n", "exclusive deny.not_eligible, approve\nexclusive review, approve.payments_sre\n"),
			want: []string{"reordered compatible exclusive"}},

		// Removals and renames.
		{name: "input removed", next: edit(deploy, "input environment: string\n", ""),
			want: []string{"removed breaking input environment"}},
		{name: "input renamed", next: edit(deploy, "input environment: string", "input env: string"),
			want: []string{"removed breaking input environment", "added compatible input env"}},
		{name: "type field removed", next: edit(deploy, "  hotfix: bool\n", ""),
			want: []string{"removed breaking type Release field hotfix"}},
		{name: "type removed", next: edit(edit(deploy, "type Release {\n  soak: duration\n  hotfix: bool\n}\n\n", ""), "input release: Release\n", ""),
			want: []string{"removed breaking type Release", "removed breaking input release"}},
		{name: "function removed", next: edit(deploy, "fn owner(string) -> string\n", ""),
			want: []string{"removed breaking fn owner"}},
		{name: "enum removed", next: edit(edit(deploy, "enum Plan: free | premium\n", ""), "  plan: Plan\n", ""),
			want: []string{"removed breaking enum Plan", "removed breaking type Service field plan"}},
		{name: "enum value removed", next: edit(deploy, "critical | standard | internal", "critical | standard"),
			want: []string{"removed breaking enum Tier value internal"}},
		{name: "enum value renamed", next: edit(deploy, "critical | standard | internal", "critical | standard | internal_only"),
			want: []string{"removed breaking enum Tier value internal", "added compatible enum Tier value internal_only"}},
		{name: "enum renamed", next: edit(edit(deploy, "enum Plan:", "enum Pricing:"), "plan: Plan", "plan: Pricing"),
			want: []string{"removed breaking enum Plan", "added compatible enum Pricing", "changed breaking type Service field plan"}},
		{name: "reason removed", next: edit(edit(deploy, "not_eligible | no_release | no_rule_matched", "not_eligible | no_rule_matched"), "not_eligible > no_release >", "not_eligible >"),
			want: []string{"removed breaking decision deny reason no_release"}},
		{name: "payload field removed", next: edit(deploy, "  bake: duration = 1h\n", ""),
			want: []string{"removed breaking decision approve field bake"}},
		{name: "decision removed, with its exclusive set", next: edit(edit(edit(deploy, "decision review {\n  reason: service_owner\n  approvers: list<string>\n}\n\n", ""), "deny > review > approve", "deny > approve"), "exclusive review, approve.payments_sre\n", ""),
			want: []string{"removed breaking decision review", "removed behavior exclusive approve.payments_sre, review"}},
		{name: "kind renamed", next: edit(deploy, "kind DeployApproval", "kind Deploy"),
			want: []string{"changed breaking kind"}},

		// Type changes.
		{name: "input type changed", next: edit(deploy, "input environment: string", "input environment: ?string"),
			want: []string{"changed breaking input environment"}},
		{name: "string field changed to an enum", next: edit(deploy, "  team: string\n", "  team: Plan\n"),
			want: []string{"changed breaking type Service field team"}},
		{name: "function signature changed", next: edit(deploy, "fn owner(string) -> string", "fn owner(string) -> list<string>"),
			want: []string{"changed breaking fn owner"}},
		{name: "payload field type changed", next: edit(deploy, "bake: duration = 1h", "bake: int = 60"),
			want: []string{"changed breaking decision approve field bake"}},
		{name: "payload field added without a default", next: edit(deploy, "  bake: duration = 1h\n", "  bake: duration = 1h\n  notify: bool\n"),
			want: []string{"added breaking decision approve field notify"}},
		{name: "payload field lost its default", next: edit(deploy, "bake: duration = 1h", "bake: duration"),
			want: []string{"changed breaking decision approve field bake"}},

		// Changes in behavior.
		{name: "precedence reordered", next: edit(deploy, "deny > review > approve", "deny > approve > review"),
			want: []string{"changed behavior precedence"}},
		{name: "scoped precedence added", next: edit(deploy, "\nexclusive review", "\nprecedence approve: release_manager > payments_sre\nexclusive review"),
			want: []string{"added behavior precedence approve"}},
		{name: "scoped precedence reordered", next: edit(deploy, "not_eligible > no_release", "no_release > not_eligible"),
			want: []string{"changed behavior precedence deny"}},
		{name: "scoped precedence removed", next: edit(deploy, "precedence deny: not_eligible > no_release > no_rule_matched > conflicting_rules\n", ""),
			want: []string{"removed behavior precedence deny"}},
		{name: "exclusive set added", next: edit(deploy, "exclusive deny.not_eligible, approve\n", "exclusive deny.not_eligible, approve\nexclusive deny.no_release, review\n"),
			want: []string{"added behavior exclusive deny.no_release, review"}},
		{name: "exclusive set removed", next: edit(deploy, "exclusive deny.not_eligible, approve\n", ""),
			want: []string{"removed behavior exclusive approve, deny.not_eligible"}},
		{name: "exclusive set changed", next: edit(deploy, "exclusive deny.not_eligible, approve", "exclusive deny, approve"),
			want: []string{"removed behavior exclusive approve, deny.not_eligible", "added behavior exclusive approve, deny"}},
		{name: "default changed", next: edit(deploy, "default deny(reason: no_rule_matched)", "default deny(reason: not_eligible)"),
			want: []string{"changed behavior default"}},
		{name: "conflict changed", next: edit(deploy, "conflict deny(reason: conflicting_rules)", "conflict deny(reason: no_rule_matched)"),
			want: []string{"changed behavior conflict"}},
		{name: "conflict removed", next: edit(deploy, "conflict deny(reason: conflicting_rules)\n", ""),
			want: []string{"removed behavior conflict"}},
		{name: "conflict added", old: edit(deploy, "conflict deny(reason: conflicting_rules)\n", ""), next: deploy,
			want: []string{"added behavior conflict"}},
		{name: "payload field default changed", next: edit(deploy, "bake: duration = 1h", "bake: duration = 2h"),
			want: []string{"changed behavior decision approve field bake"}},

		// Collecting kinds.
		{name: "collect one to collect all", next: edit(deploy, "collect one", "collect all", "conflict deny(reason: conflicting_rules)\n", ""),
			want: []string{"changed breaking collect", "removed behavior conflict"}},
		{name: "collect all to collect one", old: access, next: edit(access, "collect all\n", "collect one\nprecedence admin > read\n"),
			want: []string{"changed breaking collect", "added behavior precedence"}},
		{name: "precedence added to a collecting kind", old: access, next: edit(access, "collect all\n", "collect all\nprecedence admin > read\n"),
			want: []string{"added behavior precedence"}},
		{name: "precedence removed from a collecting kind", old: edit(access, "collect all\n", "collect all\nprecedence admin > read\n"), next: access,
			want: []string{"removed behavior precedence"}},
		{name: "default added", old: edit(access, "\ndefault admin(reason: oncall)\n", ""), next: access,
			want: []string{"added behavior default"}},
		{name: "default removed", old: access, next: edit(access, "\ndefault admin(reason: oncall)\n", ""),
			want: []string{"removed behavior default"}},
		{name: "default spells out a field's default", old: access, next: edit(access, "admin(reason: oncall)", "admin(reason: oncall, ttl: 8h)")},
		{name: "default passes another value", old: access, next: edit(access, "admin(reason: oncall)", "admin(reason: oncall, ttl: 1h)"),
			want: []string{"changed behavior default"}},
		{name: "default changes with its field's default", old: access, next: edit(access, "ttl: duration = 8h", "ttl: duration = 1h"),
			want: []string{"changed behavior decision admin field ttl", "changed behavior default"}},
		{name: "default keeps a field it passes", old: edit(access, "admin(reason: oncall)", "admin(reason: oncall, ttl: 8h)"), next: edit(access, "admin(reason: oncall)\n", "admin(reason: oncall, ttl: 8h)\n", "ttl: duration = 8h", "ttl: duration = 1h"),
			want: []string{"changed behavior decision admin field ttl"}},
		{name: "default gains a payload field", old: access, next: edit(access, "ttl: duration = 8h\n", "ttl: duration = 8h\n  scope: string = \"all\"\n"),
			want: []string{"added compatible decision admin field scope"}},

		// No change.
		{name: "unchanged", next: deploy},
		{name: "comments and layout", next: "// the deploy gate\n" + edit(deploy, "enum Plan: free | premium", "enum Plan: free\n  | premium // pricing")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			old := tt.old
			if old == "" {
				old = deploy
			}
			r := compat.Compare(load(t, old), load(t, tt.next))
			got := make([]string, len(r.Changes))
			for i, c := range r.Changes {
				got[i] = fmt.Sprintf("%s %s %s", c.Op, c.Class, c.Path)
				if c.Message == "" {
					t.Errorf("%s has no message", got[i])
				}
				if c.Class != compat.Compatible && c.Why == "" {
					t.Errorf("%s says nothing about why it isn't compatible", got[i])
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("changes:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(tt.want, "\n  "))
			}
		})
	}
}

// TestCompareMessages pins what a change says, for the cases whose
// wording depends on more than the declaration's name.
func TestCompareMessages(t *testing.T) {
	tests := []struct {
		name     string
		next     string
		message  string
		old, new string
		help     string // with accepts to raise to 4
	}{
		{name: "ambiguous value", next: edit(deploy, "free | premium", "free | standard | premium"),
			message: "enum Plan declares `standard`, which Tier declares too", new: "standard",
			help: "a bare `standard` without context becomes ambiguous; raise `accepts` to 4, and qualify it as `Tier.standard`"},
		{name: "ambiguous value whose old enum lost it", next: edit(edit(deploy, "critical | standard | internal", "critical | internal"), "free | premium", "free | standard | premium\nenum Support: standard"),
			message: "enum Plan declares `standard`, which Support declares too", new: "standard",
			help: "a bare `standard` without context becomes ambiguous; raise `accepts` to 4"},
		{name: "removed reason", next: edit(edit(deploy, "not_eligible | no_release | no_rule_matched", "not_eligible | no_rule_matched"), "not_eligible > no_release >", "not_eligible >"),
			message: "decision deny lost reason `no_release`", old: "no_release",
			help: "policies that construct deny(reason: no_release) no longer compile; raise `accepts` to 4"},
		{name: "precedence", next: edit(deploy, "deny > review > approve", "deny > approve > review"),
			message: "precedence changed", old: "deny > review > approve", new: "deny > approve > review",
			help: "every policy still compiles, but the decisions rank differently; raise `accepts` to 4, so policies pinned to older versions are reviewed before they load"},
		{name: "string to enum", next: edit(deploy, "  team: string\n", "  team: Plan\n"),
			message: "type Service field `team` changed type from string to Plan", old: "team: string", new: "team: Plan",
			help: "policies that compare `.team` with a string no longer compile; write the bare value of Plan instead of the string; raise `accepts` to 4"},
		{name: "required payload field", next: edit(deploy, "  bake: duration = 1h\n", "  bake: duration = 1h\n  notify: bool\n"),
			message: "decision approve gained field `notify` without a default", new: "notify: bool",
			help: "every approve(...) that doesn't pass `notify:` no longer compiles; raise `accepts` to 4, or give `notify` a default"},
		{name: "payload field default", next: edit(deploy, "bake: duration = 1h", "bake: duration = 2h"),
			message: "decision approve field `bake` changed its default from 1h to 2h", old: "bake: duration = 1h", new: "bake: duration = 2h",
			help: "every policy still compiles, but every approve(...) that leaves out `bake:` gets 2h instead; raise `accepts` to 4, so policies pinned to older versions are reviewed before they load"},
		{name: "collect", next: edit(deploy, "collect one", "collect all", "conflict deny(reason: conflicting_rules)\n", ""),
			message: "`collect one` changed to `collect all`", old: "collect one", new: "collect all",
			help: "the host gets every candidate at the top rank instead of one winner, and a policy written for one doesn't decide the same under the other; raise `accepts` to 4"},
		{name: "kind renamed", next: edit(deploy, "kind DeployApproval", "kind Deploy"),
			message: "kind DeployApproval is now called Deploy", old: "DeployApproval", new: "Deploy",
			help: "every policy's header names its kind, as in `policy deploy.production: DeployApproval@3`, so none of them finds it; raise `accepts` to 4"},
		{name: "exclusive set removed", next: edit(deploy, "exclusive deny.not_eligible, approve\n", ""),
			message: "exclusive deny.not_eligible, approve was removed", old: "exclusive deny.not_eligible, approve",
			help: "every policy still compiles, but evaluations where deny.not_eligible and approve fire together no longer fail with a conflict; raise `accepts` to 4, so policies pinned to older versions are reviewed before they load"},
		{name: "default", next: edit(deploy, "default deny(reason: no_rule_matched)", "default deny(reason: not_eligible)"),
			message: "default changed", old: "deny(reason: no_rule_matched)", new: "deny(reason: not_eligible)",
			help: "every policy still compiles, but evaluations where no rule fires return deny(reason: not_eligible) instead; raise `accepts` to 4, so policies pinned to older versions are reviewed before they load"},
		{name: "enum reordered", next: edit(deploy, "critical | standard | internal", "internal | critical | standard"),
			message: "enum Tier reordered its values", old: "critical | standard | internal", new: "internal | critical | standard",
			help: "raise `accepts` to 4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := compat.Compare(load(t, deploy), load(t, tt.next))
			i := slices.IndexFunc(r.Changes, func(c compat.Change) bool { return c.Message == tt.message })
			if i < 0 {
				t.Fatalf("no change says %q: %+v", tt.message, r.Changes)
			}
			c := r.Changes[i]
			if c.Old != tt.old || c.New != tt.new {
				t.Errorf("old, new = %q, %q, want %q, %q", c.Old, c.New, tt.old, tt.new)
			}
			if got := c.Help(4); got != tt.help {
				t.Errorf("help:\n  %s\nwant:\n  %s", got, tt.help)
			}
		})
	}
}

// TestCompareHeader covers the header rules: what the numbers must be
// after a change, and what Compare reports when they aren't.
func TestCompareHeader(t *testing.T) {
	const (
		compatible = "input environment: string\ninput region: string"
		breaking   = "input env: string"
		behavior   = "deny > approve > review"
	)
	changes := map[string]func(string) string{
		"":         func(s string) string { return s },
		compatible: func(s string) string { return edit(s, "input environment: string", compatible) },
		breaking:   func(s string) string { return edit(s, "input environment: string", breaking) },
		behavior:   func(s string) string { return edit(s, "deny > review > approve", behavior) },
	}
	tests := []struct {
		name       string
		change     string // a key of changes
		header     string // the new kind's header
		want       []compat.Rule
		minVersion int
		minAccepts int
		ok         bool
	}{
		{name: "no change", header: "version 3, accepts: 2", minVersion: 3, minAccepts: 2, ok: true},
		{name: "version bumped without a change", header: "version 4, accepts: 2", want: []compat.Rule{compat.VersionOnly}, minVersion: 3, minAccepts: 2, ok: true},
		{name: "accepts raised without a change", header: "version 3, accepts: 3", minVersion: 3, minAccepts: 2, ok: true},
		{name: "compatible change, version bumped", change: compatible, header: "version 4, accepts: 2", minVersion: 4, minAccepts: 2, ok: true},
		{name: "compatible change, version not bumped", change: compatible, header: "version 3, accepts: 2", want: []compat.Rule{compat.VersionUnchanged}, minVersion: 4, minAccepts: 2},
		{name: "compatible change, accepts raised", change: compatible, header: "version 4, accepts: 4", minVersion: 4, minAccepts: 2, ok: true},
		{name: "breaking change, accepts raised", change: breaking, header: "version 4, accepts: 4", minVersion: 4, minAccepts: 4, ok: true},
		{name: "breaking change, accepts not raised", change: breaking, header: "version 4, accepts: 2", want: []compat.Rule{compat.AcceptsNotRaised}, minVersion: 4, minAccepts: 4},
		{name: "breaking change, accepts raised short of the version", change: breaking, header: "version 5, accepts: 4", want: []compat.Rule{compat.AcceptsNotRaised}, minVersion: 4, minAccepts: 5},
		{name: "breaking change, version jumped", change: breaking, header: "version 6, accepts: 6", minVersion: 4, minAccepts: 6, ok: true},
		{name: "breaking change, nothing bumped", change: breaking, header: "version 3, accepts: 2", want: []compat.Rule{compat.VersionUnchanged, compat.AcceptsNotRaised}, minVersion: 4, minAccepts: 4},
		{name: "breaking change, accepts raised to the old version", change: breaking, header: "version 3, accepts: 3", want: []compat.Rule{compat.VersionUnchanged, compat.AcceptsNotRaised}, minVersion: 4, minAccepts: 4},
		{name: "change in behavior counts as breaking", change: behavior, header: "version 4, accepts: 2", want: []compat.Rule{compat.AcceptsNotRaised}, minVersion: 4, minAccepts: 4},
		{name: "change in behavior, accepts raised", change: behavior, header: "version 4, accepts: 4", minVersion: 4, minAccepts: 4, ok: true},
		{name: "version went down", header: "version 2, accepts: 2", want: []compat.Rule{compat.VersionDecreased}, minVersion: 3, minAccepts: 2},
		{name: "version went down with a change", change: compatible, header: "version 2", want: []compat.Rule{compat.VersionDecreased, compat.AcceptsLowered}, minVersion: 4, minAccepts: 2},
		{name: "accepts went down", change: compatible, header: "version 4", want: []compat.Rule{compat.AcceptsLowered}, minVersion: 4, minAccepts: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			next := edit(changes[tt.change](deploy), "version 3, accepts: 2", tt.header)
			r := compat.Compare(load(t, deploy), load(t, next))
			var got []compat.Rule
			for _, p := range r.Problems {
				got = append(got, p.Rule)
				if p.Message == "" || (p.Severity == compat.Error && p.Help == "") {
					t.Errorf("%s: message %q, help %q", p.Rule, p.Message, p.Help)
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("problems = %v, want %v", got, tt.want)
			}
			if r.MinVersion != tt.minVersion || r.MinAccepts != tt.minAccepts {
				t.Errorf("min version, accepts = %d, %d, want %d, %d", r.MinVersion, r.MinAccepts, tt.minVersion, tt.minAccepts)
			}
			if r.OK() != tt.ok {
				t.Errorf("OK() = %v, want %v", r.OK(), tt.ok)
			}
		})
	}
}

// TestProblemText pins the header problems' wording, which names the
// numbers to set.
func TestProblemText(t *testing.T) {
	tests := []struct {
		name          string
		next          string
		message, help string
	}{
		{name: "version unchanged", next: edit(deploy, "input environment: string", "input environment: string\ninput region: string"),
			message: "the contract changed, but `version` is still 3", help: "bump `version` to 4"},
		{name: "accepts not raised", next: edit(deploy, "version 3, accepts: 2", "version 4, accepts: 2", "input environment: string", "input env: string"),
			message: "1 breaking change, but `accepts` is 2", help: "raise `accepts` to 4, so policies written against version 3 or earlier are reviewed before they load"},
		{name: "accepts not raised for several", next: edit(deploy, "version 3, accepts: 2", "version 4, accepts: 2", "input environment: string", "input env: string", "deny > review > approve", "deny > approve > review"),
			message: "2 breaking changes, but `accepts` is 2", help: "raise `accepts` to 4, so policies written against version 3 or earlier are reviewed before they load"},
		{name: "version went down", next: edit(deploy, "version 3, accepts: 2", "version 2, accepts: 2"),
			message: "`version` went down from 3 to 2", help: "a kind's version only goes up; set `version` to 3, or check that OLD_KIND_FILE comes first"},
		{name: "accepts went down", next: edit(deploy, "version 3, accepts: 2", "version 4", "input environment: string", "input environment: string\ninput region: string"),
			message: "`accepts` went down from 2 to 1", help: "policies pinned below 2 were rejected for changes made since; keep `accepts` at 2 or above"},
		{name: "version only", next: edit(deploy, "version 3, accepts: 2", "version 4, accepts: 2"),
			message: "`version` went from 3 to 4, but the contract didn't change"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := compat.Compare(load(t, deploy), load(t, tt.next))
			if len(r.Problems) != 1 {
				t.Fatalf("problems = %+v, want one", r.Problems)
			}
			if p := r.Problems[0]; p.Message != tt.message || p.Help != tt.help {
				t.Errorf("message, help:\n  %s\n  %s\nwant:\n  %s\n  %s", p.Message, p.Help, tt.message, tt.help)
			}
		})
	}
}

// TestStrings covers the names JSON and text output print.
func TestStrings(t *testing.T) {
	tests := []struct{ got, want string }{
		{compat.Compatible.String(), "compatible"},
		{compat.Breaking.String(), "breaking"},
		{compat.Behavior.String(), "behavior"},
		{compat.Error.String(), "error"},
		{compat.Note.String(), "note"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("got %q, want %q", tt.got, tt.want)
		}
	}
}

// load loads a kind file the test needs to be valid.
func load(t *testing.T, src string) *kind.Kind {
	t.Helper()
	k, errs := check.LoadKind("kind.sigil", []byte(src))
	if errs != nil {
		t.Fatalf("%v\n%s", errs, src)
	}
	return k
}

// edit applies replacements to s, given as pairs of the text to find and
// what to put in its place. Each text must occur exactly once at the time
// it's replaced, or edit panics, so a case can't silently test nothing.
func edit(s string, pairs ...string) string {
	for i := 0; i+1 < len(pairs); i += 2 {
		if n := strings.Count(s, pairs[i]); n != 1 {
			panic(fmt.Sprintf("%q occurs %d times, want once", pairs[i], n))
		}
		s = strings.Replace(s, pairs[i], pairs[i+1], 1)
	}
	return s
}

// TestCompareInvalid checks that kinds Validate would reject don't make
// Compare panic: a default of a decision neither kind declares, a field
// neither passed nor defaulted, and no collect mode.
func TestCompareInvalid(t *testing.T) {
	gone := &kind.Default{Decision: "gone", Reason: "r"}
	old := &kind.Kind{Name: "K", Version: 1, Accepts: 1, Default: gone,
		Decisions: []*kind.Decision{{Name: "d", Reasons: []string{"r"}, Fields: []*kind.Field{{Name: "n", Type: types.Int}}}},
		Conflict:  &kind.Default{Decision: "d", Reason: "r"}}
	next := &kind.Kind{Name: "K", Version: 2, Accepts: 1, Default: gone, Collect: kind.CollectOne,
		Decisions: []*kind.Decision{{Name: "d", Reasons: []string{"r"}, Fields: []*kind.Field{{Name: "n", Type: types.Int, HasDefault: true, Default: int64(1)}}}},
		Conflict:  &kind.Default{Decision: "d", Reason: "r"}}
	r := compat.Compare(old, next)
	got := make([]string, len(r.Changes))
	for i, c := range r.Changes {
		got[i] = fmt.Sprintf("%s %s %s", c.Op, c.Class, c.Path)
	}
	want := []string{"changed compatible decision d field n", "changed breaking collect", "changed behavior conflict"}
	if !slices.Equal(got, want) {
		t.Errorf("changes = %v, want %v", got, want)
	}
	if c := r.Changes[1]; c.Old != "no collect" {
		t.Errorf("collect old = %q, want %q", c.Old, "no collect")
	}
}
