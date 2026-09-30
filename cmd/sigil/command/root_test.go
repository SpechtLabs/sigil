package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
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
		{args: []string{"check", "p.sigil"}, wantErr: "p.sigil can't be read"},
		{args: []string{"check", "--kind", "k.sigil", "--require", "deploy.guardrails", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"check", "--kind", "k.sigil", "--recursive", "."}, wantErr: noKindFile},
		{args: []string{"check", "-k", "k.sigil", "--require", "deploy.guardrails", "--trusted", "deploy/", "-p", "payments.*", "payments/"}, wantErr: noKindFile},
		{args: []string{"eval", "--kind", "k.sigil", "--input", "in.json", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"eval", "--kind", "k.sigil", "-"}, wantErr: "--input is required when the bundle comes from stdin"},
		{args: []string{"eval", "-k", "k.sigil", "-i", "in.json", "-p", "payments.production", "-R", "deploy/", "payments/"}, wantErr: noKindFile},
		{args: []string{"eval", "--kind", "k.sigil", "--input", "-", "-"}, wantErr: "can't both come from stdin"},
		{args: []string{"eval", "--kind", "k.sigil", "--input", "in.json"}, wantErr: noKindFile},
		{args: []string{"explain", "--kind", "k.sigil", "p.sigil"}, wantErr: noKindFile},
		{args: []string{"explain", "p.sigil"}, wantErr: "p.sigil can't be read"},
		{args: []string{"explain", "--kind", "k.sigil", "--policy", "payments.production", "a.sigil", "b.sigil"}, wantErr: noKindFile},
		{args: []string{"explain", "--kind", "k.sigil", "-"}, wantErr: noKindFile},
		{args: []string{"explain", "--kind", "k.sigil"}, wantErr: noKindFile},
		{args: []string{"flatten"}, wantErr: "Did you mean this?\n\texplain"},
		{args: []string{"test", "--kind", "k.sigil"}, wantErr: noKindFile},
		{args: []string{"test", "nope"}, wantErr: "nope can't be read"},
		{args: []string{"test", "--kind", "k.sigil", "--run", "("}, wantErr: "--run isn't a valid regular expression"},
		{args: []string{"export"}, wantErr: "no kind is linked into this binary"},
		{args: []string{"export", "a", "b"}, wantErr: "export takes at most one KIND, got 2"},
		{args: []string{"breaking", "old.sigil", "new.sigil"}, wantErr: notImplemented},
		{args: []string{"breaking", "old.sigil"}, wantErr: "breaking needs OLD_KIND_FILE and NEW_KIND_FILE, got 1"},
		{args: []string{"gen", "go", "k.sigil"}, wantErr: notImplemented},
		{args: []string{"gen"}},
		{args: []string{"lsp"}, wantErr: notImplemented},
		{args: []string{"fmt", "--write", "--check"}, wantErr: "none of the others can be"},
		{args: []string{"evaluate", "-k", "k.sigil", "-i", "in.json", "p.sigil"}, wantErr: noKindFile},
		// export is hidden without a linked kind, so nothing suggests it.
		{args: []string{"schema"}, wantErr: `unknown command "schema" for "sigil"`},
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
// a short and long description and examples, and every top-level command help
// lists a help group. It builds a host binary, so export is listed too.
func TestEveryCommandIsDocumented(t *testing.T) {
	root := NewCommand(WithKind(hostAccess))

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
			if c.Parent() == root && !c.Hidden && c.GroupID == "" {
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

// TestOutputFlagReachesEveryCommand checks that the commands with no
// report of their own honor --output too: fmt prints its records, and a
// planned command its error.
func TestOutputFlagReachesEveryCommand(t *testing.T) {
	notImplemented := func(path string) string {
		return "{\n  \"error\": {\n    \"kind\": \"not_implemented\",\n    \"message\": \"\\\"sigil " + path + "\\\" is not implemented yet\","
	}
	tests := []struct {
		args  []string
		stdin string
		want  string // the start of the output
	}{
		{args: []string{"fmt", "-o", "json", "-"}, stdin: "policy a: K@1\n", want: "[\n  {\n    \"file\": \"\\u003cstdin\\u003e\",\n    \"formatted\": true,"},
		{args: []string{"fmt", "-o", "yaml", "--check", "-"}, stdin: "policy a: K@1\n", want: "- file: <stdin>\n  formatted: true\n"},
		{args: []string{"lsp", "-o", "json"}, want: notImplemented("lsp")},
		{args: []string{"gen", "go", "-o", "json", "k.sigil"}, want: notImplemented("gen go")},
		{args: []string{"breaking", "-o", "json", "old.sigil", "new.sigil"}, want: notImplemented("breaking")},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := NewCommand()
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetIn(strings.NewReader(tt.stdin))
			cmd.SetArgs(tt.args)
			_ = cmd.Execute() // the planned commands fail; the output is what's checked
			if !strings.HasPrefix(out.String(), tt.want) {
				t.Errorf("output = %q, want it to start with %q", out.String(), tt.want)
			}
		})
	}
}

// TestHelpListsWhatRuns checks the root help: the planned commands are
// hidden, and export is hidden unless a kind is linked in, which also
// leaves a group whose commands are all hidden out of the help. Hidden
// commands still run, and a host binary still suggests export.
func TestHelpListsWhatRuns(t *testing.T) {
	tests := []struct {
		name   string
		opts   []Option
		listed []string // commands and groups the help shows
		absent []string // commands and groups it doesn't
	}{
		{
			name:   "stock binary",
			listed: []string{"POLICY COMMANDS", "fmt [PATH...]", "OTHER COMMANDS", "version"},
			absent: []string{"KIND COMMANDS", "EDITOR INTEGRATION", "export", "breaking", "gen", "lsp", "--kind"},
		},
		{
			name:   "host binary",
			opts:   []Option{WithKind(hostAccess)},
			listed: []string{"POLICY COMMANDS", "KIND COMMANDS", "export [KIND]", "OTHER COMMANDS"},
			absent: []string{"EDITOR INTEGRATION", "breaking", "gen", "lsp"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR_FORCE", "")
			cmd := NewCommand(tt.opts...)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&bytes.Buffer{})
			if code := Execute(context.Background(), cmd, []string{"--color=never", "--help"}); code != 0 {
				t.Fatalf("Execute() = %d", code)
			}
			help := out.String()
			for _, s := range tt.listed {
				if !strings.Contains(help, s) {
					t.Errorf("help doesn't list %q:\n%s", s, help)
				}
			}
			for _, s := range tt.absent {
				if strings.Contains(help, s) {
					t.Errorf("help lists %q:\n%s", s, help)
				}
			}
			// cobra's own usage template prints every group it knows,
			// so an empty one shows up there as a bare heading.
			if usage := cmd.UsageString(); strings.Contains(usage, groupEditor.Title) {
				t.Errorf("usage lists the empty %q group:\n%s", groupEditor.Title, usage)
			}
			if got := cmd.ContainsGroup(groupKind.ID); got != (len(tt.opts) > 0) {
				t.Errorf("ContainsGroup(%q) = %v, want it only with a linked kind", groupKind.ID, got)
			}
		})
	}

	host := NewCommand(WithKind(hostAccess))
	host.SetOut(&bytes.Buffer{})
	host.SetArgs([]string{"schema"})
	if err := host.Execute(); err == nil || !strings.Contains(err.Error(), "Did you mean this?\n\texport") {
		t.Errorf("Execute(schema) in a host binary = %v, want a suggestion of export", err)
	}
}
