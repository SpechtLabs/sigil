package policies_test

import (
	"io/fs"
	"testing"

	"github.com/spechtlabs/sigil/examples/policies"
)

// TestEmbeddedTrees guards the embed directives: a renamed directory would
// otherwise only show up as a service that can't load its guardrails.
func TestEmbeddedTrees(t *testing.T) {
	tests := []struct {
		fsys fs.FS
		name string
		file string
	}{
		{name: "platform guardrails", fsys: policies.Platform, file: "deploy/guardrails.sigil"},
		{name: "payments policy", fsys: policies.Teams, file: "payments/production.sigil"},
		{name: "checkout policy", fsys: policies.Teams, file: "checkout/production.sigil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := fs.Stat(tt.fsys, tt.file); err != nil {
				t.Fatalf("stat %s: %v", tt.file, err)
			}
		})
	}
}
