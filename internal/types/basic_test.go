package types_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/types"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name string
		want types.Basic
		ok   bool
	}{
		{"bool", types.Bool, true},
		{"int", types.Int, true},
		{"float", types.Float, true},
		{"string", types.String, true},
		{"duration", types.Duration, true},
		{"timestamp", types.Timestamp, true},
		{"decision", types.Invalid, false}, // not nameable in source
		{"invalid", types.Invalid, false},
		{"list", types.Invalid, false},
		{"map", types.Invalid, false},
		{"Service", types.Invalid, false},
		{"String", types.Invalid, false},
		{"", types.Invalid, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := types.Lookup(tt.name)
			if ok != tt.ok || (ok && got != tt.want) {
				t.Errorf("Lookup(%q) = %v, %v; want %v, %v", tt.name, got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestScalarNames(t *testing.T) {
	got := strings.Join(types.ScalarNames(), ", ")
	if want := "bool, int, float, string, duration, timestamp"; got != want {
		t.Errorf("ScalarNames() = %q, want %q", got, want)
	}
	for _, name := range types.ScalarNames() {
		if _, ok := types.Lookup(name); !ok {
			t.Errorf("Lookup(%q) failed for a name ScalarNames returned", name)
		}
	}
}
