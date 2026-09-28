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
		{name: "access root", fsys: policies.Access, file: "main.sigil"},
		{name: "deploy platform view", fsys: policies.PlatformDeploy, file: "deploy/guardrails.sigil"},
		{name: "access platform view", fsys: policies.PlatformAccess, file: "access/guardrails.sigil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := fs.Stat(tt.fsys, tt.file); err != nil {
				t.Fatalf("stat %s: %v", tt.file, err)
			}
		})
	}
}

// TestOnly checks that a view shows its directory, with the prefix, and hides
// everything else, which is what keeps one kind's documents out of another
// kind's bundle.
func TestOnly(t *testing.T) {
	view := policies.Only(policies.Platform, "deploy")

	root, err := fs.ReadDir(view, ".")
	if err != nil {
		t.Fatalf("ReadDir(.): %v", err)
	}
	if len(root) != 1 || root[0].Name() != "deploy" {
		t.Errorf("root lists %v, want only deploy", root)
	}

	tests := []struct {
		name string
		want bool
	}{
		{name: "deploy/guardrails.sigil", want: true},
		{name: "deploy", want: true},
		{name: "access/guardrails.sigil"},
		{name: "access"},
		{name: "deployment/x.sigil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := fs.Stat(view, tt.name)
			if (err == nil) != tt.want {
				t.Errorf("Stat(%s) error = %v, want visible %v", tt.name, err, tt.want)
			}
			if _, err := fs.ReadDir(view, tt.name); !tt.want && err == nil {
				t.Errorf("ReadDir(%s) succeeded on a hidden path", tt.name)
			}
		})
	}
}
