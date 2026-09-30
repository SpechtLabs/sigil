package check

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
)

// TestScopeKeepsKindErrors checks that --policy never hides a problem
// in a kind document: every policy is checked against a kind, so a kind
// file that differs from another source, or doesn't check, fails the
// run whichever policies it narrows to.
func TestScopeKeepsKindErrors(t *testing.T) {
	const broken = "kind Other version 1\n\ninput x: nope\n\ndecision allow {\n  reason: yes\n}\n\ncollect one\n\ndefault allow(reason: yes)\n"
	tests := []struct {
		name  string
		paths []string
		stdin string
		want  string
	}{
		{name: "a kind file that differs", paths: []string{"testdata/scope", "testdata/stale/deploy_approval.sigil"}, want: "kind document DeployApproval differs"},
		{name: "a kind document that doesn't check", paths: []string{"testdata/scope", "-"}, stdin: broken, want: "<stdin>:3:"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := output.Text
			var out bytes.Buffer
			src := project.Sources{Paths: tt.paths, Kinds: []string{filepath.Join("testdata", "deploy_approval.sigil")}, Stdin: strings.NewReader(tt.stdin)}
			err := run(&out, &options{output: &format}, filepath.Join("testdata", "config", "defaults.yaml"), src, []string{"scope.a"}, nil)
			if err == nil || !strings.Contains(out.String(), tt.want) {
				t.Errorf("run() = %v with output %q, want it to fail on %q", err, out.String(), tt.want)
			}
		})
	}
}
