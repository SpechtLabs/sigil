package policy_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/pkg/policy"
)

const gate = `policy deploy.gate: DeployApproval@1

when release.hotfix {
  approve(reason: release_manager)
}
`

// TestLoadLayouts loads the same policy from the directory layouts a
// host meets: a plain directory, nested directories, a mounted ConfigMap
// with kubelet's symlinked keys, and a map from the Kubernetes API.
func TestLoadLayouts(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string // relative path to content; a value starting with "->" is a symlink target
		dirs  []string          // extra directories to create first
		root  string            // the policy to load; deploy.gate when empty
		mapfs bool              // load through MapFS instead of os.DirFS
		err   string            // a substring of the error, when the load fails
	}{
		{name: "one file", files: map[string]string{"gate.sigil": gate}},
		{name: "nested directories", files: map[string]string{"deploy/gate.sigil": gate, "teams/README.md": "not a policy"}},
		{name: "mounted ConfigMap", dirs: []string{"..2026_09_28_10_00_00.123"}, files: map[string]string{
			"..2026_09_28_10_00_00.123/policies.sigil": gate,
			"..data":         "->..2026_09_28_10_00_00.123",
			"policies.sigil": "->..data/policies.sigil",
		}},
		{name: "dot entries and other extensions are skipped", files: map[string]string{
			"gate.sigil": gate, ".hidden.sigil": "not parsed", "notes.txt": "policy broken", ".git/config.sigil": "nope",
		}},
		{name: "MapFS", mapfs: true, files: map[string]string{"policies.sigil": gate, "README.md": "ignored"}},
		{name: "no such policy", files: map[string]string{"gate.sigil": gate}, root: "deploy.other", err: "bundle has no policy deploy.other"},
		{name: "empty directory", err: "the bundle defines no policies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, d := range tt.dirs {
				if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for name, content := range tt.files {
				path := filepath.Join(dir, name)
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				var err error
				if len(content) > 2 && content[:2] == "->" {
					err = os.Symlink(content[2:], path)
				} else {
					err = os.WriteFile(path, []byte(content), 0o644)
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			root := tt.root
			if root == "" {
				root = "deploy.gate"
			}
			var p *policy.Policy[Input]
			var err error
			if tt.mapfs {
				p, err = Deploy.Load(policy.MapFS(tt.files), root)
			} else {
				p, err = Deploy.Load(os.DirFS(dir), root)
			}
			if tt.err != "" {
				var ce *policy.CompileError
				if !errors.As(err, &ce) || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Load() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			res, err := p.Eval(context.Background(), with(func(in *Input) { in.Release.Hotfix = true }))
			if err != nil || res.Reason != "release_manager" {
				t.Errorf("Eval = %+v, %v", res, err)
			}
		})
	}
}
