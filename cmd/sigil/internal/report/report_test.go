package report

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
	"github.com/spechtlabs/sigil/internal/result"
)

func theme() pretty.Theme {
	return pretty.New(&bytes.Buffer{}, pretty.WithEnviron(nil)).Theme()
}

func TestPlain(t *testing.T) {
	when := time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC)
	got := Plain(map[any]any{
		"ttl":  90 * time.Minute,
		"at":   when,
		"list": []any{time.Second, "x"},
		3:      "int key",
	})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("Plain() = %T, want a map with string keys", got)
	}
	if m["ttl"] != "1h30m" || m["at"] != "2026-09-28T14:00:00Z" || m["3"] != "int key" {
		t.Errorf("Plain() = %v", m)
	}
	if list, ok := m["list"].([]any); !ok || list[0] != "1s" || list[1] != "x" {
		t.Errorf("Plain(list) = %v", m["list"])
	}
	if got := Plain(42); got != 42 {
		t.Errorf("Plain(42) = %v", got)
	}
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
				"assert o failed at p:1:1\n  the outcome it read:\n    write(owner)   p:8:3\n    write(oncall)  p:9:3\n",
				"assert c failed at p:2:1\n  p:2:5: unbound\n    = help: use the host's binary\n",
				"  = help: h\n",
			},
		},
		{
			name: "an outcome assert shows the payloads it read",
			f: &Failure{Kind: FailAssertion, Asserts: []Assert{
				{Reason: "no_self_review", Position: "p:1:1", Outcome: []Entry{{Decision: "review", Reason: "a", Position: "p:3:3", values: []field{{name: "approvers", value: []any{"alice", "bob"}}}}}},
			}},
			wantHeadline: "an assert failed",
			wantText:     []string{"  the outcome it read:\n    review(a)  p:3:3\n      approvers = [\"alice\", \"bob\"]\n"},
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
			wantText:     []string{"conflict: 2 at the top\n", "    deny(a)       p:1:1\n", "    deny(longer)  p:2:1\n"},
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
			if got := tt.f.headline(); got != tt.wantHeadline {
				t.Errorf("headline() = %q, want %q", got, tt.wantHeadline)
			}
			r := &Report{Error: tt.f}
			text := tt.f.text(theme(), r.width())
			for _, want := range tt.wantText {
				if !strings.Contains(text, want) {
					t.Errorf("text() =\n%s\nwant it to contain %q", text, want)
				}
			}
		})
	}
}

func TestHelp(t *testing.T) {
	for _, kind := range []string{FailAssertion, FailConflict, FailRuntime} {
		if help(kind) == "" {
			t.Errorf("help(%s) is empty", kind)
		}
	}
}

// TestText lays out a report built by hand, with what the goldens don't
// reach: a call chain, several conditions, and a collecting kind.
func TestText(t *testing.T) {
	winner := Entry{Decision: "review", Reason: "owner", Position: "team.sigil:5:1", Chain: []string{"team.sigil:2:1"}, Conditions: []string{"cleared", "owns"}, Outcome: true,
		values: []field{{name: "approvers", value: []any{"a"}}}}
	loser := Entry{Decision: "approve", Reason: "sre", Position: "team.sigil:9:1", Conditions: []string{"on_call"}}
	r := &Report{Policy: "team", Decision: "review", Reason: "owner", Outcome: []Entry{winner}, Trace: []Entry{winner, loser}}
	want := "team: review(owner)\n" +
		"  approvers = [\"a\"]\n" +
		"\n" +
		"trace: 2 candidates\n" +
		"  * review(owner)  team.sigil:2:1 → team.sigil:5:1\n" +
		"      when cleared\n" +
		"       and owns\n" +
		"      approvers = [\"a\"]\n" +
		"    approve(sre)   team.sigil:9:1\n" +
		"      when on_call\n"
	if got := r.Text(theme()); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}

	collect := &Report{Policy: "grants", Collect: true, Outcome: []Entry{loser}, Trace: []Entry{{Decision: "approve", Reason: "sre", Position: "team.sigil:9:1", Outcome: true}}}
	want = "grants: 1 decision\n" +
		"  approve(sre)  team.sigil:9:1\n" +
		"\n" +
		"trace: 1 candidate\n" +
		"  * approve(sre)  team.sigil:9:1\n"
	if got := collect.Text(theme()); got != want {
		t.Errorf("Text() =\n%s\nwant\n%s", got, want)
	}
}

// TestFailureHelp checks that a runtime error that knows what to do
// about itself says so, in place of the generic advice.
func TestFailureHelp(t *testing.T) {
	at := result.Position{File: "p.sigil", Line: 2, Column: 5}
	tests := []struct {
		name      string
		fl        *result.Failure
		wantHelp  string
		wantCause string // the first assert's help
	}{
		{name: "runtime error", fl: &result.Failure{Runtime: &result.Runtime{Msg: "boom", Position: at}}, wantHelp: help(FailRuntime)},
		{name: "runtime error with help", fl: &result.Failure{Runtime: &result.Runtime{Msg: "unbound", Help: "use the host's binary", Position: at}}, wantHelp: "use the host's binary"},
		{
			name:      "assert whose cause has help",
			fl:        &result.Failure{Asserts: []result.Assert{{Reason: "a", Position: at, Cause: &result.Runtime{Msg: "unbound", Help: "use the host's binary", Position: at}}}},
			wantHelp:  help(FailAssertion),
			wantCause: "use the host's binary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := failure(nil, tt.fl)
			if f.Help != tt.wantHelp {
				t.Errorf("Help = %q, want %q", f.Help, tt.wantHelp)
			}
			if tt.wantCause != "" && f.Asserts[0].Help != tt.wantCause {
				t.Errorf("Asserts[0].Help = %q, want %q", f.Asserts[0].Help, tt.wantCause)
			}
		})
	}
}
