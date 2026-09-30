package report

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/internal/workspace"
)

func theme() pretty.Theme {
	return pretty.New(&bytes.Buffer{}, pretty.WithEnviron(nil)).Theme()
}

func TestFailureText(t *testing.T) {
	tests := []struct {
		name         string
		f            *Failure
		wantHeadline string
		wantText     []string
	}{
		{
			name:         "several asserts, one with a cause",
			f:            &Failure{Kind: FailAssertion, Help: "h", Asserts: []Assert{{Reason: "a", Position: "p:1:1"}, {Reason: "b", Position: "p:2:1", Cause: "p:2:5: boom"}}},
			wantHeadline: "2 asserts failed",
			wantText:     []string{"assert a failed at p:1:1\n", "assert b failed at p:2:1\n  p:2:5: boom\n", "  = help: h\n"},
		},
		{
			name: "an outcome assert, and a cause with its own help",
			f: &Failure{Kind: FailAssertion, Help: "h", Asserts: []Assert{
				{Reason: "o", Position: "p:1:1", Outcome: []Entry{{Decision: "write", Reason: "owner", Position: "p:8:3"}, {Decision: "write", Reason: "oncall", Position: "p:9:3"}}},
				{Reason: "c", Position: "p:2:1", Cause: "p:2:5: unbound", Help: "use the host's binary"},
			}},
			wantHeadline: "2 asserts failed",
			wantText: []string{
				"assert o failed at p:1:1\n  the outcome it read:\n    write(reason: owner)   p:8:3\n    write(reason: oncall)  p:9:3\n",
				"assert c failed at p:2:1\n  p:2:5: unbound\n    = help: use the host's binary\n",
				"  = help: h\n",
			},
		},
		{
			name: "an outcome assert shows the payloads it read",
			f: &Failure{Kind: FailAssertion, Asserts: []Assert{
				{Reason: "no_self_review", Position: "p:1:1", Outcome: []Entry{{Decision: "review", Reason: "a", Position: "p:3:3", Fields: []workspace.Field{{Name: "approvers", Value: []any{"alice", "bob"}}}}}},
			}},
			wantHeadline: "an assert failed",
			wantText:     []string{"  the outcome it read:\n    review(reason: a)  p:3:3\n      approvers = [\"alice\", \"bob\"]\n"},
		},
		{
			name:         "one assert",
			f:            &Failure{Kind: FailAssertion, Asserts: []Assert{{Reason: "a", Position: "p:1:1"}}},
			wantHeadline: "an assert failed",
		},
		{
			name:         "conflict",
			f:            &Failure{Kind: FailConflict, Message: "2 at the top", Candidates: []Entry{{Decision: "deny", Reason: "a", Position: "p:1:1"}, {Decision: "deny", Reason: "longer", Position: "p:2:1"}}},
			wantHeadline: "the candidates conflict",
			wantText:     []string{"conflict: 2 at the top\n", "    deny(reason: a)       p:1:1\n", "    deny(reason: longer)  p:2:1\n"},
		},
		{
			name:         "canceled",
			f:            &Failure{Kind: FailCanceled, Message: "the evaluation was stopped: context deadline exceeded", Help: "give it more time"},
			wantHeadline: "the evaluation was stopped",
			wantText:     []string{"stopped: the evaluation was stopped: context deadline exceeded\n  = help: give it more time\n"},
		},
		{
			name:         "runtime",
			f:            &Failure{Kind: FailRuntime, Message: "p:1:1: boom", Help: "fix it"},
			wantHeadline: "a runtime error stopped the evaluation",
			wantText:     []string{"runtime error: p:1:1: boom\n  = help: fix it\n"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := headline(tt.f); got != tt.wantHeadline {
				t.Errorf("headline() = %q, want %q", got, tt.wantHeadline)
			}
			r := &Report{Error: tt.f}
			text := failureText(tt.f, theme(), columnWidth(r))
			for _, want := range tt.wantText {
				if !strings.Contains(text, want) {
					t.Errorf("text() =\n%s\nwant it to contain %q", text, want)
				}
			}
		})
	}
}

// TestText lays out a report built by hand, with what the goldens don't
// reach: a call chain, several conditions, and a collecting kind.
func TestText(t *testing.T) {
	winner := Entry{Decision: "review", Reason: "owner", Position: "team.sigil:5:1", Chain: []string{"team.sigil:2:1"}, Conditions: []string{"cleared", "owns"}, Outcome: true,
		Fields: []workspace.Field{{Name: "approvers", Value: []any{"a"}}}}
	loser := Entry{Decision: "approve", Reason: "sre", Position: "team.sigil:9:1", Conditions: []string{"on_call"}}
	r := &Report{Policy: "team", Decision: "review", Reason: "owner", Outcome: []Entry{winner}, Trace: []Entry{winner, loser}}
	want := "team: review(reason: owner)\n" +
		"  approvers = [\"a\"]\n" +
		"\n" +
		"trace: 2 candidates\n" +
		"  * review(reason: owner)  team.sigil:2:1 → team.sigil:5:1\n" +
		"      when cleared\n" +
		"       and owns\n" +
		"      approvers = [\"a\"]\n" +
		"    approve(reason: sre)   team.sigil:9:1\n" +
		"      when on_call\n"
	if got := Text(r, theme()); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}

	collect := &Report{Policy: "grants", Collect: true, Outcome: []Entry{loser}, Trace: []Entry{{Decision: "approve", Reason: "sre", Position: "team.sigil:9:1", Outcome: true}}}
	want = "grants: 1 decision\n" +
		"  approve(reason: sre)  team.sigil:9:1\n" +
		"\n" +
		"trace: 1 candidate\n" +
		"  * approve(reason: sre)  team.sigil:9:1\n"
	if got := Text(collect, theme()); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
}
