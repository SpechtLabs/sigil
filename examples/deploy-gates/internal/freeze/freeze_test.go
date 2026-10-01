package freeze_test

import (
	"slices"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/freeze"
)

func TestStatic(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{name: "nothing frozen", in: nil, want: []string{}},
		{name: "one environment", in: []string{"production"}, want: []string{"production"}},
		{name: "sorted and deduplicated", in: []string{"staging", "production", "staging"}, want: []string{"production", "staging"}},
		{name: "blanks and spaces dropped", in: []string{" production ", "", "  "}, want: []string{"production"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := freeze.NewStatic(tt.in...)
			got := src.Freeze()
			if got.Environments == nil || !slices.Equal(got.Environments, tt.want) || got.Unknown {
				t.Errorf("Freeze() = %+v, want environments %v, known", got, tt.want)
			}

			// The caller owns what Freeze returns.
			if len(got.Environments) > 0 {
				got.Environments[0] = "changed"
				if again := src.Freeze(); !slices.Equal(again.Environments, tt.want) {
					t.Errorf("changing an answer changed the source: %v", again.Environments)
				}
			}
		})
	}
}

func TestWallClock(t *testing.T) {
	var c freeze.WallClock
	if since := time.Since(c.Now()); since < 0 || since > time.Minute {
		t.Errorf("Now() is %s away from time.Now", since)
	}
	tick, stop := c.Tick(time.Millisecond)
	defer stop()
	select {
	case <-tick:
	case <-time.After(5 * time.Second):
		t.Fatal("Tick(1ms) didn't tick within 5s")
	}
}
