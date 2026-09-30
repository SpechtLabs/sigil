package policy_test

import (
	"os/exec"
	"strings"
	"testing"
)

// TestDependencies checks that a host linking package policy doesn't link
// sigil's tooling: the stub parser and the YAML decoder are for the CLI
// and for policytest, not for a service evaluating policies.
func TestDependencies(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("go isn't on PATH")
	}
	out, err := exec.CommandContext(t.Context(), gobin, "list", "-deps", ".").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for dep := range strings.FieldsSeq(string(out)) {
		if dep == "github.com/spechtlabs/sigil/internal/stub" || strings.Contains(dep, "yaml") {
			t.Errorf("package policy depends on %s", dep)
		}
	}
}
