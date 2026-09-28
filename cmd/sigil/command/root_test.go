package command

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
)

func TestOutputFlagReachesSubcommand(t *testing.T) {
	cmd := NewCommand(WithVersion("1.2.3"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version", "-o", "json"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !strings.HasPrefix(out.String(), `{"version":"1.2.3"`) {
		t.Errorf("output = %q, want JSON", out.String())
	}
}

func TestOutputFlagRejectsUnknownFormat(t *testing.T) {
	cmd := NewCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"version", "-o", "xml"})

	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want error for unknown output format")
	}
}

func TestCommandSurface(t *testing.T) {
	const (
		notImplemented = "is not implemented yet"
		noKindFile     = "the kind file couldn't be read"
	)

	tests := []struct {
		args    []string
		wantErr string
	}{
		{args: []string{"fmt", "a.sigil"}, wantErr: "a.sigil can't be read"},
		{args: []string{"check", "--kind", "k.sigil", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"check", "p.sigil"}, wantErr: "no kind file given"},
		{args: []string{"check", "--kind", "k.sigil", "--require", "deploy.guardrails", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"check", "--kind", "k.sigil", "--recursive", "."}, wantErr: noKindFile},
		{args: []string{"check", "-k", "k.sigil", "--require", "deploy.guardrails", "--trusted", "deploy/", "-p", "payments.*", "payments/"}, wantErr: noKindFile},
		{args: []string{"eval", "--kind", "k.sigil", "--input", "in.json", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"eval", "--kind", "k.sigil", "p.sigil"}, wantErr: `required flag(s) "input" not set`},
		{args: []string{"eval", "-k", "k.sigil", "-i", "in.json", "-p", "payments.production", "-R", "deploy/", "payments/"}, wantErr: noKindFile},
		{args: []string{"eval", "--kind", "k.sigil", "--input", "-", "-"}, wantErr: "can't both come from stdin"},
		{args: []string{"eval", "--kind", "k.sigil", "--input", "in.json"}, wantErr: "requires at least 1 arg(s)"},
		{args: []string{"explain", "--kind", "k.sigil", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"explain", "p.sigil"}, wantErr: "no kind file given"},
		{args: []string{"explain", "--kind", "k.sigil", "--policy", "payments.production", "a.sigil", "b.sigil"}, wantErr: noKindFile},
		{args: []string{"explain", "--kind", "k.sigil", "-"}, wantErr: noKindFile},
		{args: []string{"explain", "--kind", "k.sigil"}, wantErr: "requires at least 1 arg(s)"},
		{args: []string{"flatten"}, wantErr: "Did you mean this?\n\texplain"},
		{args: []string{"test", "--kind", "k.sigil"}, wantErr: noKindFile},
		{args: []string{"test"}, wantErr: "no kind file given"},
		{args: []string{"test", "--kind", "k.sigil", "--run", "("}, wantErr: "--run isn't a valid regular expression"},
		{args: []string{"export"}, wantErr: "no kind is linked into this binary"},
		{args: []string{"export", "a", "b"}, wantErr: "accepts at most 1 arg(s)"},
		{args: []string{"breaking", "old.sigil", "new.sigil"}, wantErr: notImplemented},
		{args: []string{"breaking", "old.sigil"}, wantErr: "accepts 2 arg(s)"},
		{args: []string{"gen", "go", "k.sigil"}, wantErr: notImplemented},
		{args: []string{"gen"}},
		{args: []string{"lsp"}, wantErr: notImplemented},
		{args: []string{"fmt", "--write", "--check"}, wantErr: "none of the others can be"},
		{args: []string{"evaluate", "-k", "k.sigil", "-i", "in.json", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"schema"}, wantErr: "Did you mean this?\n\texport"},
		{args: []string{"generate", "golang", "k.sigil"}, wantErr: notImplemented},
		{args: []string{"chek"}, wantErr: "Did you mean this?\n\tcheck"},
		{args: []string{"validate"}, wantErr: "Did you mean this?\n\tcheck"},
		{args: []string{"gen", "rust"}, wantErr: `unknown command "rust" for "sigil gen"`},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := NewCommand()
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(tt.args)

			err := cmd.Execute()
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Execute() error = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Execute() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// TestEveryCommandIsDocumented keeps help output complete: every command needs
// a short and long description and examples, and every top-level command a
// help group.
func TestEveryCommandIsDocumented(t *testing.T) {
	root := NewCommand()

	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		t.Run(c.CommandPath(), func(t *testing.T) {
			if c.Short == "" {
				t.Error("missing Short")
			}
			if c.Long == "" {
				t.Error("missing Long")
			}
			if c.Example == "" {
				t.Error("missing Example")
			}
			if c.Parent() == root && c.GroupID == "" {
				t.Error("missing GroupID")
			}
		})
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

func TestOutputFlagCompletion(t *testing.T) {
	cmd := NewCommand()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{cobra.ShellCompRequestCmd, "--output", ""})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	for _, format := range output.Formats {
		if !strings.Contains(out.String(), format+"\n") {
			t.Errorf("completion output %q is missing %q", out.String(), format)
		}
	}
}
