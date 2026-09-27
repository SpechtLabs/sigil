package suggest_test

import (
	"testing"

	"github.com/spechtlabs/sigil/internal/suggest"
)

func TestClosest(t *testing.T) {
	fields := []string{"name", "tier", "owners", "labels"}
	tests := []struct {
		name       string
		candidates []string
		want       string
		ok         bool
	}{
		{"teir", fields, "tier", true},     // transposition
		{"tie", fields, "tier", true},      // deletion
		{"tiers", fields, "tier", true},    // insertion
		{"Tier", fields, "tier", true},     // case
		{"ownres", fields, "owners", true}, // transposition in a longer name
		{"lables", fields, "labels", true}, // transposition
		{"label", fields, "labels", true},  // deletion
		{"labeling", fields, "", false},    // three edits is too far
		{"nam", fields, "name", true},
		{"tier", fields, "", false},   // exact matches aren't suggestions
		{"region", fields, "", false}, // nothing close
		{"x", fields, "", false},      // a single letter matches nothing
		{"names", []string{"name", "names"}, "", false},
		{"naem", []string{"name", "names"}, "name", true}, // ties go to the earlier candidate
		{"aprovers", []string{"approvers", "tiers"}, "approvers", true},
		{"min_sock", []string{"min_soak", "approvers"}, "min_soak", true},
		{"release", []string{"Release", "Service"}, "Release", true},
		{"anything", nil, "", false},
		{"", fields, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := suggest.Closest(tt.name, tt.candidates)
			if got != tt.want || ok != tt.ok {
				t.Errorf("Closest(%q) = %q, %v; want %q, %v", tt.name, got, ok, tt.want, tt.ok)
			}
		})
	}
}
