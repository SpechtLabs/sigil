package policies_test

import (
	"io/fs"
	"testing"

	"github.com/spechtlabs/sigil/examples/alert-routing/policies"
)

// TestEmbeddedTrees guards the embed directives: a renamed directory would
// otherwise only show up as a service that can't load platform.paging.
func TestEmbeddedTrees(t *testing.T) {
	tests := []struct {
		fsys fs.FS
		name string
		file string
	}{
		{name: "platform paging", fsys: policies.Platform, file: "platform/paging.sigil"},
		{name: "platform routing", fsys: policies.Platform, file: "platform/routing.sigil"},
		{name: "platform alerts module", fsys: policies.Platform, file: "platform/alerts.sigil"},
		{name: "checkout policy", fsys: policies.Teams, file: "checkout/alerts.sigil"},
		{name: "payments policy", fsys: policies.Teams, file: "payments/alerts.sigil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := fs.Stat(tt.fsys, tt.file); err != nil {
				t.Fatalf("stat %s: %v", tt.file, err)
			}
		})
	}
}
