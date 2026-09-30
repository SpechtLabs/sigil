package policy_test

import (
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// TestReason checks the reason handles: a declared reason gives a handle
// that names its decision and reason, and anything else panics with the
// hint the checker gives for the same typo in a policy.
func TestReason(t *testing.T) {
	tests := []struct {
		name   string
		reason string
		panic  string // the panic message; empty when Reason returns
	}{
		{name: "declared", reason: "no_rule_matched"},
		{name: "a typo gets a did-you-mean", reason: "no_rule_mached",
			panic: `policy: decision deny has no reason "no_rule_mached" (did you mean "no_rule_matched"? deny declares: a, no_rule_matched, never, b, not_eligible, soak_too_short, x)`},
		{name: "a case slip gets a did-you-mean", reason: "No_Rule_Matched",
			panic: `policy: decision deny has no reason "No_Rule_Matched" (did you mean "no_rule_matched"? deny declares: a, no_rule_matched, never, b, not_eligible, soak_too_short, x)`},
		{name: "nothing near lists the reasons", reason: "banned",
			panic: `policy: decision deny has no reason "banned" (deny declares: a, no_rule_matched, never, b, not_eligible, soak_too_short, x)`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				r := recover()
				if msg, _ := r.(string); msg != tt.panic || (r == nil) != (tt.panic == "") {
					t.Errorf("Reason(%q) panicked with %v\nwant %q", tt.reason, r, tt.panic)
				}
			}()
			o := Deny.Reason(tt.reason)
			if o.Decision() != "deny" || o.Name() != tt.reason {
				t.Errorf("Reason(%q) = %s, %s", tt.reason, o.Decision(), o.Name())
			}
		})
	}
}

// TestOutcomeIs checks Is against results of each collect mode. It
// follows Match: exactly one entry with the handle's decision and
// reason, false for a nil result, and a panic where Match panics.
func TestOutcomeIs(t *testing.T) {
	one := compileSrc(t, Deploy, "policy p: DeployApproval@1\n\nwhen release.hotfix {\n  deny(reason: soak_too_short)\n}\n\nwhen release.soak < 1h {\n  approve(reason: a)\n}\n")
	top := compileSrc(t, Reviews, "policy p: Reviews@1\n\n"+
		"when service.tier == \"critical\" {\n  review(reason: security, approvers: [\"security-leads\"])\n}\n\n"+
		"when \"payments\" in service.owners {\n  review(reason: owner, approvers: [\"payments-leads\"])\n}\n")
	all := compileSrc(t, Access, "policy p: AccessGrant@1\n\nwhen \"engineering\" in actor.teams {\n  read(reason: engineering_member)\n}\n")

	var (
		soakTooShort  = Deny.Reason("soak_too_short")
		noRuleMatched = Deny.Reason("no_rule_matched")
		approveA      = Approve.Reason("a")
		denyA         = Deny.Reason("a") // same reason name, other decision
		security      = ReviewAny.Reason("security")
		owner         = ReviewAny.Reason("owner")
	)
	tests := []struct {
		name    string
		res     *policy.Result
		outcome policy.Outcome
		want    bool
		panics  bool
	}{
		// collect one
		{name: "collect one: the winner", res: eval(t, one, Input{Release: Release{Hotfix: true}}), outcome: soakTooShort, want: true},
		{name: "collect one: another reason of the winner", res: eval(t, one, Input{Release: Release{Hotfix: true}}), outcome: noRuleMatched},
		{name: "collect one: the default", res: eval(t, one, Input{Release: Release{Soak: 2 * time.Hour}}), outcome: noRuleMatched, want: true},
		{name: "collect one: the reason under another decision", res: eval(t, one, Input{}), outcome: denyA},
		{name: "collect one: the same reason name", res: eval(t, one, Input{}), outcome: approveA, want: true},

		// collect all with precedence
		{name: "ranked: one entry at the top", res: eval(t, top, Input{Service: Service{Tier: "critical"}}), outcome: security, want: true},
		{name: "ranked: two entries at the top", res: eval(t, top, Input{Service: Service{Tier: "critical", Owners: []string{"payments"}}}), outcome: security},
		{name: "ranked: two entries, the other one", res: eval(t, top, Input{Service: Service{Tier: "critical", Owners: []string{"payments"}}}), outcome: owner},
		{name: "ranked: nothing fired", res: eval(t, top, Input{}), outcome: security},

		// collect all without precedence
		{name: "unranked: an entry", res: eval(t, all, AccessInput{Actor: Actor{Teams: []string{"engineering"}}}), outcome: Read.Reason("engineering_member"), panics: true},
		{name: "unranked: nothing fired", res: eval(t, all, AccessInput{}), outcome: Read.Reason("engineering_member"), panics: true},

		{name: "nil result", outcome: noRuleMatched},
		{name: "the zero outcome", res: eval(t, one, Input{}), outcome: policy.Outcome{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); (r != nil) != tt.panics {
					t.Errorf("recover() = %v, want a panic: %v", r, tt.panics)
				}
			}()
			if got := tt.outcome.Is(tt.res); got != tt.want {
				t.Errorf("%s.%s.Is = %v, want %v", tt.outcome.Decision(), tt.outcome.Name(), got, tt.want)
			}
		})
	}
}

// compileSrc compiles the policy p from a single-document source.
func compileSrc[In any](t *testing.T, k *policy.Kind[In], src string) *policy.Policy[In] {
	t.Helper()
	p, err := k.Compile(src, "p")
	if err != nil {
		t.Fatalf("Compile:\n%v", err)
	}
	return p
}
