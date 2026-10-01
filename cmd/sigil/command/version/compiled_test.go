package version

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/payload"
)

// compiled is the command tests' bundle: a kind file, two team policies,
// and the guardrails they're required to invoke, as trusted.
const compiled = "../testdata/compiled"

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestCompiledVersion compares what a compiled binary's version prints,
// in every format, with the golden files: the build's info, then the
// bundle's.
func TestCompiledVersion(t *testing.T) {
	built := time.Date(2026, 9, 30, 14, 0, 0, 0, time.FixedZone("CEST", 2*60*60))
	tests := []struct {
		name   string
		format output.Format
		change func(p *payload.Payload)
	}{
		{name: "compiled", format: output.Text},
		{name: "compiled_json", format: output.JSON},
		{name: "compiled_yaml", format: output.YAML},
		// No root, a requirement for every policy, and no build time.
		{name: "compiled_bare", format: output.Text, change: func(p *payload.Payload) {
			p.Bundle.Root, p.Built = "", time.Time{}
			p.Bundle.Require = []payload.Requirement{{Policy: "deploy.guardrails"}}
		}},
		{name: "compiled_bare_json", format: output.JSON, change: func(p *payload.Payload) {
			p.Bundle.Root, p.Bundle.Require, p.Built = "", nil, time.Time{}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := compiledPayload(t, built)
			if tt.change != nil {
				tt.change(p)
			}
			cmd := NewCommand(WithVersion("1.2.3"), WithBuildInfo(testBuildInfo), WithOutput(&tt.format), WithPayload(p, "gate"))
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetArgs([]string{})
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() = %v", err)
			}
			golden(t, tt.name, out.String())
		})
	}
}

// TestCompiledVersionErrors checks that a bundle that doesn't load fails
// version, and that a format it doesn't know does too.
func TestCompiledVersionErrors(t *testing.T) {
	broken := &payload.Payload{Bundle: payload.Bundle{Kinds: []payload.File{{Name: "k.sigil", Source: "policy a: K@1\n"}}}}
	xml := output.Format("xml")
	tests := []struct {
		name    string
		opts    []Option
		wantErr string
	}{
		{name: "a kind file with no kind", opts: []Option{WithPayload(broken, "gate")}, wantErr: "k.sigil"},
		{name: "an unknown format", opts: []Option{WithPayload(compiledPayload(t, time.Time{}), "gate"), WithOutput(&xml)}, wantErr: `unsupported output type "xml"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := NewCommand(tt.opts...)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs([]string{})
			if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Execute() = %v, want %q", err, tt.wantErr)
			}
		})
	}

	// The text report is written in three parts: the build's info, a
	// blank line and the bundle's. Each one's failure is reported.
	for ok := range 3 {
		w := &failingWriter{ok: ok}
		if err := run(w, options{output: new(output.Text), payload: compiledPayload(t, time.Time{})}); err == nil || !strings.Contains(err.Error(), "couldn't be written") {
			t.Errorf("run() after %d writes = %v, want the write error", ok, err)
		}
	}
}

// compiledPayload returns the payload sigil compile writes for the
// testdata bundle, built at built.
func compiledPayload(t *testing.T, built time.Time) *payload.Payload {
	t.Helper()
	f, err := project.Read(project.Sources{
		Kinds:   []string{compiled + "/deploy.sigil"},
		Paths:   []string{compiled + "/teams", compiled + "/deploy.sigil"},
		Trusted: []string{compiled + "/platform"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := f.Bundle()
	b.Root = "payments.production"
	b.Require = []payload.Requirement{{Policy: "deploy.guardrails", Trusted: []string{compiled + "/platform"}, Roots: []string{"payments.*", "checkout.*"}}}
	return &payload.Payload{Bundle: *b, Sigil: "1.2.0", Built: built}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got != string(want) {
		t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// failingWriter fails every write after the first ok ones.
type failingWriter struct{ ok int }

func (w *failingWriter) Write(p []byte) (int, error) {
	if w.ok == 0 {
		return 0, errors.New("closed pipe")
	}
	w.ok--
	return len(p), nil
}
