package policy_test

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// The platform's trusted source for TestTrusted: a vocabulary module no
// required policy imports, and a guardrail.
var platformFiles = map[string]string{
	"deploy/vocabulary.sigil": `module deploy.vocabulary: DeployApproval@1

pub let is_hotfix = release.hotfix
`,
	"deploy/guardrails.sigil": `policy deploy.guardrails: DeployApproval@1

when release.soak < 1h {
  deny(reason: soak_too_short)
}
`,
}

// teamPolicy imports the platform's vocabulary and invokes its guardrail.
const teamPolicy = `policy payments.production: DeployApproval@1

use deploy.vocabulary.{is_hotfix}
use deploy.guardrails

guardrails()

when is_hotfix {
  approve(reason: release_manager)
}
`

// TestTrusted loads a team bundle with the platform's source passed to
// Trusted, alone and with Require and From, and checks what resolves
// where, which names the team can't take, and how often the source is
// read.
func TestTrusted(t *testing.T) {
	tests := []struct {
		name     string
		platform map[string]string                        // files added to platformFiles
		team     string                                   // the team's bundle; teamPolicy when empty
		compile  bool                                     // compile team as a string instead of loading it
		opts     func(platform fs.FS) []policy.LoadOption // the load options, given the platform's source
		input    Input                                    // evaluated when the load succeeds
		reason   string                                   // the reason the evaluation decides
		err      string                                   // a substring of the error, when the load fails
		reads    int                                      // how often the platform's vocabulary is read
	}{
		{
			name:   "a team policy imports a trusted module",
			opts:   func(p fs.FS) []policy.LoadOption { return []policy.LoadOption{policy.Trusted(p)} },
			input:  Input{Release: Release{Soak: 2 * time.Hour, Hotfix: true}},
			reason: "release_manager", reads: 1,
		},
		{
			name:    "a policy compiled from a string imports a trusted module",
			compile: true,
			opts:    func(p fs.FS) []policy.LoadOption { return []policy.LoadOption{policy.Trusted(p)} },
			input:   Input{Release: Release{Soak: 2 * time.Hour, Hotfix: true}},
			reason:  "release_manager", reads: 1,
		},
		{
			name: "Trusted and From name the same source, which is read once",
			opts: func(p fs.FS) []policy.LoadOption {
				return []policy.LoadOption{policy.Trusted(p), policy.Require("deploy.guardrails", policy.From(p))}
			},
			input:  Input{Release: Release{Hotfix: true}},
			reason: "soak_too_short", reads: 1,
		},
		{
			name: "Trusted twice, and a nil source, read the source once",
			opts: func(p fs.FS) []policy.LoadOption {
				return []policy.LoadOption{policy.Trusted(nil), policy.Trusted(p), policy.Trusted(p)}
			},
			input:  Input{Release: Release{Soak: 2 * time.Hour, Hotfix: true}},
			reason: "release_manager", reads: 1,
		},
		{
			name: "a team module can't redefine trusted vocabulary",
			team: teamPolicy + "---\nmodule deploy.vocabulary: DeployApproval@1\n\npub let is_hotfix = true\n",
			opts: func(p fs.FS) []policy.LoadOption { return []policy.LoadOption{policy.Trusted(p)} },
			err:  "module deploy.vocabulary is defined twice", reads: 1,
		},
		{
			name: "a required policy without From comes from the trusted source",
			team: teamPolicy + "---\npolicy deploy.guardrails: DeployApproval@1\n",
			opts: func(p fs.FS) []policy.LoadOption {
				return []policy.LoadOption{policy.Trusted(p), policy.Require("deploy.guardrails")}
			},
			err: "the name belongs to the trusted source", reads: 1,
		},
		{
			name:     "a broken trusted document fails the load though nothing uses it",
			platform: map[string]string{"deploy/broken.sigil": "module deploy.broken: DeployApproval@1\n\npub let late = release.sok > 1h\n"},
			opts:     func(p fs.FS) []policy.LoadOption { return []policy.LoadOption{policy.Trusted(p)} },
			err:      `unknown field "sok" on type Release`, reads: 1,
		},
		{
			name: "two trusted sources can't hold the same file",
			opts: func(p fs.FS) []policy.LoadOption {
				other := policy.MapFS(map[string]string{"deploy/vocabulary.sigil": "module deploy.other: DeployApproval@1\n"})
				return []policy.LoadOption{policy.Trusted(p), policy.Trusted(other)}
			},
			err: "policy file deploy/vocabulary.sigil is in two of the sources read into one bundle", reads: 1,
		},
		{
			name: "an unreadable trusted source fails the load",
			opts: func(fs.FS) []policy.LoadOption {
				return []policy.LoadOption{policy.Trusted(os.DirFS(filepath.Join(t.TempDir(), "missing")))}
			},
			err: "directory . of the policy bundle couldn't be read",
		},
		{
			name:    "an unreadable trusted source fails a compile",
			compile: true,
			opts: func(fs.FS) []policy.LoadOption {
				return []policy.LoadOption{policy.Trusted(os.DirFS(filepath.Join(t.TempDir(), "missing")))}
			},
			err: "directory . of the policy bundle couldn't be read",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			files := maps.Clone(platformFiles)
			maps.Copy(files, tt.platform)
			platform := &countingFS{FS: policy.MapFS(files), reads: map[string]int{}}
			team := tt.team
			if team == "" {
				team = teamPolicy
			}
			var p *policy.Policy[Input]
			var err error
			if tt.compile {
				p, err = Deploy.Compile(team, "payments.production", tt.opts(platform)...)
			} else {
				p, err = Deploy.Load(policy.MapFS(map[string]string{"teams/payments.sigil": team}), "payments.production", tt.opts(platform)...)
			}
			if got := platform.reads["deploy/vocabulary.sigil"]; got != tt.reads {
				t.Errorf("the platform's vocabulary was read %d times, want %d", got, tt.reads)
			}
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Load() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			res, err := p.Eval(context.Background(), tt.input)
			if err != nil || res.Reason != tt.reason {
				t.Errorf("Eval = %+v, %v; want reason %s", res, err, tt.reason)
			}
		})
	}
}

// countingFS counts how often each file is read. A pointer to one is
// comparable, so options naming it name the same source.
type countingFS struct {
	fs.FS
	reads map[string]int
}

func (c *countingFS) ReadFile(name string) ([]byte, error) {
	c.reads[name]++
	return fs.ReadFile(c.FS, name)
}
