package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/result"
)

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

func TestHelp(t *testing.T) {
	for _, kind := range []string{FailAssertion, FailConflict, FailRuntime, FailCanceled} {
		if help(kind) == "" {
			t.Errorf("help(%s) is empty", kind)
		}
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
		{name: "canceled", fl: &result.Failure{Canceled: context.DeadlineExceeded}, wantHelp: help(FailCanceled)},
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

// TestAssertPhase checks that a failed assertion says which phase its
// asserts failed in and which policy each is in.
func TestAssertPhase(t *testing.T) {
	at := result.Position{File: "p.sigil", Line: 2, Column: 5}
	tests := []struct {
		name    string
		outcome bool
		want    string
	}{
		{name: "input", want: PhaseInput},
		{name: "outcome", outcome: true, want: PhaseOutcome},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := failure(nil, &result.Failure{OutcomeAsserts: tt.outcome, Asserts: []result.Assert{{Reason: "a", Policy: "team.main", Position: at}}})
			if f.Phase != tt.want || f.Asserts[0].Policy != "team.main" {
				t.Errorf("failure() = phase %q, policy %q; want %q, team.main", f.Phase, f.Asserts[0].Policy, tt.want)
			}
		})
	}
}
