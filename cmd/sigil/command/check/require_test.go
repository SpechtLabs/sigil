package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// TestTrustedNeedsRequire checks that --trusted without --require is an
// error: it can't replace sigil.yaml's require:, which would drop the
// guardrails, and nothing it reads as trusted would be required.
func TestTrustedNeedsRequire(t *testing.T) {
	format := output.Text
	var out bytes.Buffer
	src := project.Sources{
		Paths:   []string{filepath.Join("testdata", "lints", "teams")},
		Trusted: []string{filepath.Join("testdata", "lints", "deploy")},
		Kinds:   []string{filepath.Join("testdata", "deploy_approval.sigil")},
	}
	err := run(&out, &options{output: &format}, filepath.Join("testdata", "lints", "require_trusted.yaml"), src, nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "--trusted names where required policies come from") {
		t.Errorf("run() = %v with output %q, want --trusted rejected without --require", err, out.String())
	}
}

// TestRequiredOrigin checks that a requirement with trusted paths finds
// its policy there, below that requirement's own paths: a team can't
// replace the platform's guardrails with a policy of the same name.
func TestRequiredOrigin(t *testing.T) {
	lints := filepath.Join("testdata", "lints")
	// The platform's documents, but with deploy.guardrails moved out of
	// the trusted directory into a team's, where it approves everything.
	files := map[string]string{
		"platform/common.sigil":     filepath.Join(lints, "deploy", "common.sigil"),
		"platform/production.sigil": filepath.Join(lints, "deploy", "production.sigil"),
		"access/guardrails.sigil":   filepath.Join(lints, "deploy", "guardrails.sigil"),
		"teams/payments.sigil":      filepath.Join(lints, "teams", "payments.sigil"),
	}
	fake := "policy deploy.guardrails: DeployApproval@1\n\nwhen true {\n  approve(reason: release_manager)\n}\n"
	tests := []struct {
		name     string
		config   string // sigil.yaml
		requires []string
		trusted  []string
		fake     bool // replace access/guardrails.sigil with teams/guardrails.sigil, outside every trusted path
		want     string
	}{
		{
			name:   "defined among the paths",
			config: "require:\n  - policy: deploy.guardrails\n    trusted: [platform]\n",
			fake:   true,
			want:   "sigil.yaml:2:13: deploy.guardrails must come from platform, but it's defined at teams/guardrails.sigil",
		},
		{
			name:     "defined among the paths, with the flags",
			config:   "lints: {}\n",
			requires: []string{"deploy.guardrails"},
			trusted:  []string{"platform"},
			fake:     true,
			want:     "--require deploy.guardrails: deploy.guardrails must come from platform, but it's defined at teams/guardrails.sigil",
		},
		{
			name:   "trusted, but by another entry",
			config: "require:\n  - policy: deploy.guardrails\n    trusted: [platform]\n  - policy: access.nothing\n    trusted: [access]\n",
			want:   "sigil.yaml:2:13: deploy.guardrails must come from platform, but it's defined at access/guardrails.sigil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			write := func(name string, src []byte) {
				path := filepath.Join(dir, filepath.FromSlash(name))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, src, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			for to, from := range files {
				src, err := os.ReadFile(from)
				if err != nil {
					t.Fatal(err)
				}
				if tt.fake && to == "access/guardrails.sigil" {
					to, src = "teams/guardrails.sigil", []byte(fake)
				}
				write(to, src)
			}
			write("sigil.yaml", []byte(tt.config))
			kind, err := filepath.Abs(filepath.Join("testdata", "deploy_approval.sigil"))
			if err != nil {
				t.Fatal(err)
			}
			t.Chdir(dir)
			format := output.Text
			var out bytes.Buffer
			herr := run(&out, &options{output: &format}, "", project.Sources{Trusted: tt.trusted, Kinds: []string{kind}}, nil, tt.requires)
			if herr == nil || !strings.Contains(herr.Error(), tt.want) {
				t.Errorf("run() = %v with output %q, want %q", herr, out.String(), tt.want)
			}
			if herr != nil && !strings.Contains(strings.Join(herr.Advice(), " "), "policy.From") {
				t.Errorf("advice = %v, want it to explain the trusted source", herr.Advice())
			}
		})
	}
}
