package command

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecuteExitCodes(t *testing.T) {
	url := testService(t)
	tests := []struct {
		args []string
		code int
	}{
		{[]string{"deploy", "owner"}, 0},
		{[]string{"deploy", "sre"}, 0},
		{[]string{"deploy", "short-soak"}, 2},
		{[]string{"access", "outsider"}, 2},
		{[]string{"access", "break-glass-platform"}, 2},
		{[]string{"deploy", "unnamed-actor"}, 2},
		{[]string{"deploy", "--team", "unknown"}, 1},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			cmd := NewCommand()
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			args := append([]string{"--url", url, "--json"}, tt.args...)
			if code := Execute(t.Context(), cmd, args); code != tt.code {
				t.Fatalf("exit = %d, want %d; stderr = %s", code, tt.code, stderr.String())
			}
			if !json.Valid(stdout.Bytes()) || stderr.Len() != 0 {
				t.Fatalf("response was lost or printed twice: stdout = %s, stderr = %s", stdout.String(), stderr.String())
			}
		})
	}
}

func TestExecuteUsageError(t *testing.T) {
	cmd := NewCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if code := Execute(t.Context(), cmd, []string{"deploy", "misspelled"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if stdout.Len() != 0 || !strings.Contains(stderr.String(), "demo-cli scenarios") {
		t.Fatalf("missing error advice on stderr: stdout = %s, stderr = %s", stdout.String(), stderr.String())
	}
}

func TestVersionOption(t *testing.T) {
	cmd := NewCommand(WithVersion("1.2.3"))
	var out bytes.Buffer
	cmd.SetOut(&out)
	if code := Execute(t.Context(), cmd, []string{"version"}); code != 0 || out.String() != "demo-cli 1.2.3\n" {
		t.Fatalf("exit = %d, output = %q", code, out.String())
	}
}
