package pretty

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/sierrasoftworks/humane-errors-go"
)

// plain returns a Printer writing to a non-terminal, as when output is piped.
func plain(buf *bytes.Buffer) *Printer {
	return New(buf, WithEnviron(nil))
}

// forced returns a Printer that styles output as if writing to a terminal.
func forced(buf *bytes.Buffer) *Printer {
	return New(buf, WithEnviron([]string{"CLICOLOR_FORCE=1", "TERM=xterm-256color"}), WithDarkBackground(true))
}

func TestStatusLines(t *testing.T) {
	var buf bytes.Buffer
	p := plain(&buf)

	_ = p.Ok("formatted 3 files", "deploy/production.sigil")
	_ = p.Info("checking")
	_ = p.Warn("slow policy")
	_ = p.Fail("2 policies failed")

	want := "✓ formatted 3 files\n  deploy/production.sigil\nℹ checking\n! slow policy\n✗ 2 policies failed\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestKeyValuesPlain(t *testing.T) {
	var buf bytes.Buffer
	_ = plain(&buf).KeyValues("sigil", KV{Key: "Version", Value: "1.2.3"}, KV{Key: "Commit time", Value: "now"})

	want := "Version:     1.2.3\nCommit time: now\n"
	if buf.String() != want {
		t.Errorf("output = %q, want %q", buf.String(), want)
	}
}

func TestKeyValuesOnTerminal(t *testing.T) {
	var buf bytes.Buffer
	_ = forced(&buf).KeyValues("sigil", KV{Key: "Version", Value: "1.2.3"})

	out := buf.String()
	for _, want := range []string{"╭", "sigil", "Version:", "1.2.3", "\x1b["} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q is missing %q", out, want)
		}
	}
}

func TestErr(t *testing.T) {
	cause := humane.Wrap(errors.New("open k.sigil: no such file or directory"), "failed to read kind", "check the --kind path")
	err := humane.Wrap(cause, "failed to load kind", "run sigil check first")

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "humane error with advice and causes",
			err:  err,
			want: "Error: failed to load kind\n\n" +
				"What you can do\n  • check the --kind path\n  • run sigil check first\n\n" +
				"Caused by\n  • failed to read kind\n  • open k.sigil: no such file or directory\n",
		},
		{
			name: "plain error, as from cobra",
			err:  errors.New(`unknown flag: --nope`),
			want: "Error: unknown flag: --nope\n\nWhat you can do\n  • " + usageAdvice + "\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			_ = plain(&buf).Err(tt.err)
			if buf.String() != tt.want {
				t.Errorf("output = %q, want %q", buf.String(), tt.want)
			}
		})
	}
}

func TestErrOnTerminal(t *testing.T) {
	var buf bytes.Buffer
	_ = forced(&buf).Err(humane.New("boom", "try again"))

	out := buf.String()
	for _, want := range []string{"ERROR", "boom", "try again", "\x1b["} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q is missing %q", out, want)
		}
	}
}

func TestErrNil(t *testing.T) {
	var buf bytes.Buffer
	if err := plain(&buf).Err(nil); err != nil || buf.Len() != 0 {
		t.Errorf("Err(nil) = %v and wrote %q, want nothing", err, buf.String())
	}
}
