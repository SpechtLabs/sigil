package command

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
)

func TestExecute(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string
		wantErr  string
	}{
		{name: "success", args: []string{"--color=never", "version", "-o", "json"}, wantOut: `{"version":"1.2.3"`},
		{name: "usage error is translated", args: []string{"--color=never", "chekc"}, wantCode: 1, wantErr: "did you mean sigil check?"},
		{name: "missing argument", args: []string{"--color=never", "check"}, wantCode: 1, wantErr: "check needs at least one PATH"},
		{name: "failed command prints why", args: []string{"--color=never", "check", "--kind", "nope.sigil", "p.sigil"}, wantCode: 1, wantErr: "Error: the kind file couldn't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("NO_COLOR", "")
			t.Setenv("CLICOLOR_FORCE", "")
			cmd := NewCommand(WithVersion("1.2.3"))
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			if got := Execute(context.Background(), cmd, tt.args); got != tt.wantCode {
				t.Errorf("Execute() = %d, want %d (stderr %q)", got, tt.wantCode, errOut.String())
			}
			if !strings.Contains(out.String(), tt.wantOut) {
				t.Errorf("stdout = %q, want it to contain %q", out.String(), tt.wantOut)
			}
			if !strings.Contains(errOut.String(), tt.wantErr) {
				t.Errorf("stderr = %q, want it to contain %q", errOut.String(), tt.wantErr)
			}
		})
	}
}

func TestColorMode(t *testing.T) {
	tests := []struct {
		args []string
		want output.Color
	}{
		{nil, output.ColorAuto},
		{[]string{"check", "-k", "k.sigil", "p.sigil"}, output.ColorAuto},
		{[]string{"--color=always", "check"}, output.ColorAlways},
		{[]string{"check", "--color", "never", "-o", "json", "p"}, output.ColorNever},
		{[]string{"--color=purple"}, output.ColorAuto},
		{[]string{"--nope", "-x", "--color=never"}, output.ColorNever},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			if got := colorMode(tt.args); got != tt.want {
				t.Errorf("colorMode() = %q, want %q", got, tt.want)
			}
		})
	}
}
