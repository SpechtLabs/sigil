package output_test

import (
	"os"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
)

func TestColorFlag(t *testing.T) {
	var c output.Color
	if err := c.Set("always"); err != nil || c != output.ColorAlways || c.String() != "always" {
		t.Errorf("Set(always) = %v, c = %q", err, c)
	}
	err := c.Set("purple")
	if err == nil || c != output.ColorAlways {
		t.Fatalf("Set(purple) = %v, c = %q, want an error and no change", err, c)
	}
	if got := err.Error(); got != `unsupported color mode "purple"` {
		t.Errorf("Set(purple) = %q", got)
	}
	if got := c.Type(); got != "mode" {
		t.Errorf("Type() = %q", got)
	}
}

func TestColorApply(t *testing.T) {
	tests := []struct {
		mode                 output.Color
		wantForce, wantNever string
	}{
		{output.ColorAlways, "1", ""},
		{output.ColorNever, "", "1"},
		{output.ColorAuto, "keep", "keep"},
	}
	for _, tt := range tests {
		t.Run(string(tt.mode), func(t *testing.T) {
			t.Setenv("CLICOLOR_FORCE", "keep")
			t.Setenv("NO_COLOR", "keep")
			tt.mode.Apply()
			if got := os.Getenv("CLICOLOR_FORCE"); got != tt.wantForce {
				t.Errorf("CLICOLOR_FORCE = %q, want %q", got, tt.wantForce)
			}
			if got := os.Getenv("NO_COLOR"); got != tt.wantNever {
				t.Errorf("NO_COLOR = %q, want %q", got, tt.wantNever)
			}
		})
	}
}
