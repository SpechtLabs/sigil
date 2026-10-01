package complete_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
	"github.com/spechtlabs/sigil/internal/payload"
)

func TestSigilFiles(t *testing.T) {
	got, directive := complete.SigilFiles(nil, nil, "")
	if len(got) != 1 || got[0] != "sigil" || directive != cobra.ShellCompDirectiveFilterFileExt {
		t.Errorf("SigilFiles() = %v, %v", got, directive)
	}
	upTo := complete.SigilFilesUpTo(1)
	if got, directive := upTo(nil, nil, ""); len(got) != 1 || directive != cobra.ShellCompDirectiveFilterFileExt {
		t.Errorf("SigilFilesUpTo(1) with no args = %v, %v", got, directive)
	}
	if got, directive := upTo(nil, []string{"one"}, ""); len(got) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("SigilFilesUpTo(1) with one arg = %v, %v", got, directive)
	}
}

// TestPolicies checks that --policy and --require complete the policy
// names in the paths, headers alone telling them apart, and that
// --trusted splits them the way the commands read them.
func TestPolicies(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		// No kind anywhere: completing needs only the headers.
		"teams/payments.sigil": "policy payments.production: DeployApproval@1\n\nwhen {\n",
		"teams/checkout.sigil": "policy checkout.production: DeployApproval@1\n---\nmodule checkout.common: DeployApproval@1\n",
		"platform/guard.sigil": "policy deploy.guardrails: DeployApproval@1\n",
		"notes.txt":            "policy not.read: DeployApproval@1\n",
	}
	for name, src := range files {
		if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A file that can't be read is left out, as silently as the rest.
	locked := filepath.Join(dir, "teams", "locked.sigil")
	if err := os.WriteFile(locked, []byte("policy locked.out: DeployApproval@1\n"), 0o000); err != nil {
		t.Fatal(err)
	}
	platform := filepath.Join(dir, "platform")
	tests := []struct {
		name    string
		fn      cobra.CompletionFunc
		args    []string
		trusted []string
		prefix  string
		want    []string
		inDir   bool // run in the directory instead of naming it
	}{
		{name: "policy", fn: complete.Policies, args: []string{dir}, want: []string{"checkout.production", "deploy.guardrails", "payments.production"}},
		{name: "policy with a prefix", fn: complete.Policies, args: []string{dir}, prefix: "pay", want: []string{"payments.production"}},
		{name: "policy leaves out trusted files", fn: complete.Policies, args: []string{dir}, trusted: []string{platform}, want: []string{"checkout.production", "payments.production"}},
		{name: "policy in the current directory", fn: complete.Policies, inDir: true, want: []string{"checkout.production", "deploy.guardrails", "payments.production"}},
		{name: "policy skips stdin and unreadable paths", fn: complete.Policies, args: []string{"-", filepath.Join(dir, "nope")}},
		{name: "require from trusted", fn: complete.Required, args: []string{dir}, trusted: []string{platform}, want: []string{"deploy.guardrails"}},
		{name: "require without trusted", fn: complete.Required, args: []string{filepath.Join(dir, "platform", "guard.sigil"), "-"}, want: []string{"deploy.guardrails"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.inDir {
				t.Chdir(dir)
			}
			cmd := &cobra.Command{}
			cmd.Flags().StringSlice("trusted", tt.trusted, "")
			got, directive := tt.fn(cmd, tt.args, tt.prefix)
			if !slices.Equal(got, tt.want) || directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("completion = %v, %v, want %v", got, directive, tt.want)
			}
		})
	}
}

// TestPoliciesWithoutTrusted checks that a command without --trusted,
// such as eval, completes every policy in its paths.
func TestPoliciesWithoutTrusted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "p.sigil"), []byte("policy p: K@1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := complete.Policies(&cobra.Command{}, []string{dir}, "")
	if !slices.Equal(got, []string{"p"}) {
		t.Errorf("completion = %v, want [p]", got)
	}
}

// TestCompiled checks that a compiled binary's --policy completes the
// policies in its bundle's headers, trusted files and kind files apart.
func TestCompiled(t *testing.T) {
	b := &payload.Bundle{
		Kinds: []payload.File{{Name: "k.sigil", Source: "kind K version 1\n"}},
		Paths: []payload.File{
			{Name: "teams/payments.sigil", Source: "policy payments.production: K@1\n\nwhen {\n"},
			{Name: "teams/checkout.sigil", Source: "policy checkout.production: K@1\n---\nmodule checkout.common: K@1\n---\npolicy payments.production: K@1\n"},
		},
		Trusted: []payload.File{{Name: "platform/guard.sigil", Source: "policy deploy.guardrails: K@1\n"}},
	}
	tests := []struct {
		name   string
		bundle *payload.Bundle
		prefix string
		want   []string
	}{
		{name: "every policy", bundle: b, want: []string{"checkout.production", "payments.production"}},
		{name: "a prefix", bundle: b, prefix: "pay", want: []string{"payments.production"}},
		{name: "no match", bundle: b, prefix: "deploy"},
		{name: "an empty bundle", bundle: &payload.Bundle{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, directive := complete.Compiled(tt.bundle)(nil, nil, tt.prefix)
			if !slices.Equal(got, tt.want) || directive != cobra.ShellCompDirectiveNoFileComp {
				t.Errorf("completion = %v, %v, want %v", got, directive, tt.want)
			}
		})
	}
}
