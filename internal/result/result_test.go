package result_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/benchtest"
	"github.com/spechtlabs/sigil/internal/result"
)

func TestEvaluateTypedPayloads(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		source     string
		collect    bool
		wantTyped  []any // Typed of each outcome entry
		candidates int   // candidates across the trace, the conflict and the asserts' outcomes
	}{
		{
			name:       "collected outcome",
			source:     benchtest.Composed,
			collect:    true,
			wantTyped:  []any{benchtest.Payload{TTL: time.Hour}, benchtest.Payload{TTL: 2 * time.Hour}},
			candidates: 2,
		},
		{
			name:       "conflict falls back to the default",
			source:     benchtest.Composed,
			wantTyped:  []any{struct{}{}},
			candidates: 4,
		},
		{
			name:       "failed outcome assert",
			source:     "policy main: Bench@1\nwhen enabled { deny(reason: blocked) }\nassert(\"granted\", allow in outcome)\n",
			wantTyped:  []any{struct{}{}},
			candidates: 2,
		},
		{
			name:      "no candidate",
			source:    "policy main: Bench@1\nwhen not enabled { allow(reason: member) }\n",
			wantTyped: []any{struct{}{}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			in := benchtest.Value()
			res := result.Evaluate(benchtest.Compile(t, tt.source, tt.collect), &in)
			typed := make([]any, 0, len(res.Outcome))
			for _, e := range res.Outcome {
				typed = append(typed, e.Typed)
			}
			if !reflect.DeepEqual(typed, tt.wantTyped) {
				t.Errorf("outcome Typed = %#v, want %#v", typed, tt.wantTyped)
			}
			cands := append([]result.Candidate{}, res.Trace...)
			if f := res.Failure; f != nil {
				if f.Conflict != nil {
					cands = append(cands, f.Conflict.Candidates...)
				}
				for _, a := range f.Asserts {
					cands = append(cands, a.Outcome...)
				}
			}
			if len(cands) != tt.candidates {
				t.Errorf("got %d candidates, want %d", len(cands), tt.candidates)
			}
			for _, c := range cands {
				if c.Typed != nil {
					t.Errorf("candidate %s(%s) at %s has Typed = %#v, want nil", c.Decision, c.Reason, c.Position, c.Typed)
				}
			}
		})
	}
}
