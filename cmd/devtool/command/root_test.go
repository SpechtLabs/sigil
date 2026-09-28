package command

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// TestCommandsShareTheirSurface keeps the commands alike: every run command
// takes the same shared flags, and so does every list command, with the
// same shorthand and type, so using one teaches the others.
func TestCommandsShareTheirSurface(t *testing.T) {
	shared := map[string][]string{
		"run":  {"filter", "time", "cpu", "timeout", "verbose", "results"},
		"list": {"filter", "packages", "output"},
	}
	root := NewCommand()
	for sub, names := range shared {
		var first *pflag.FlagSet
		for _, group := range []string{"bench", "fuzz"} {
			cmd, _, err := root.Find([]string{group, sub})
			if err != nil || cmd.Name() != sub {
				t.Fatalf("devtool %s %s: %v", group, sub, err)
			}
			if !strings.HasPrefix(cmd.Use, sub+" [PACKAGE...]") {
				t.Errorf("devtool %s %s: Use = %q, want %q", group, sub, cmd.Use, sub+" [PACKAGE...]")
			}
			if first == nil {
				first = cmd.Flags()
				continue
			}
			for _, name := range names {
				want, got := first.Lookup(name), cmd.Flags().Lookup(name)
				if want == nil || got == nil || got.Shorthand != want.Shorthand || got.Value.Type() != want.Value.Type() {
					t.Errorf("--%s differs between devtool bench %s and devtool fuzz %s", name, sub, sub)
				}
			}
		}
	}
}

func TestExecute(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{name: "help", args: []string{"--help"}, wantOut: "bench"},
		{name: "fuzz help", args: []string{"fuzz", "--help"}, wantOut: "report"},
		{name: "unknown command", args: []string{"nope"}, wantCode: 1, wantErr: `unknown command "nope"`},
		{name: "humane advice", args: []string{"bench", "run", "--count", "0"}, wantCode: 1, wantErr: "pass at least one sample"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			cmd := NewCommand()
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			if code := Execute(t.Context(), cmd, tt.args); code != tt.wantCode {
				t.Errorf("Execute() = %d, want %d; stderr:\n%s", code, tt.wantCode, errOut.String())
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout %q doesn't contain %q", out.String(), tt.wantOut)
			}
			if !strings.Contains(errOut.String(), tt.wantErr) {
				t.Errorf("stderr %q doesn't contain %q", errOut.String(), tt.wantErr)
			}
		})
	}
}
