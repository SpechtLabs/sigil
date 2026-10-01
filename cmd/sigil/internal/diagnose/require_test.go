package diagnose

import (
	"path/filepath"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
)

// TestWhole checks when a run reads the whole repository sigil.yaml
// configures, which makes every roots: pattern count.
func TestWhole(t *testing.T) {
	cfg := &config.Config{File: filepath.Join("repo", "policies", "sigil.yaml")}
	tests := []struct {
		paths []string
		want  bool
	}{
		{paths: []string{filepath.Join("repo", "policies")}, want: true},
		{paths: []string{"repo"}, want: true},
		{paths: []string{"."}, want: true},
		{paths: []string{filepath.Join("repo", "policies", "teams"), "-"}},
		{paths: []string{filepath.Join("repo", "other")}},
		{paths: []string{"-"}},
	}
	for _, tt := range tests {
		if got := whole(cfg, tt.paths); got != tt.want {
			t.Errorf("whole(%v) = %v, want %v", tt.paths, got, tt.want)
		}
	}
}
