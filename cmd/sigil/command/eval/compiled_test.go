package eval

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/payload"
)

// TestCompiledInput checks where a compiled binary's eval reads the input
// from, and that its advice calls the binary by its name.
func TestCompiledInput(t *testing.T) {
	p := &payload.Payload{Bundle: payload.Bundle{
		Root:  "access.main",
		Kinds: []payload.File{{Name: "access.sigil", Source: read(t, "testdata/access.sigil")}},
		Paths: []payload.File{{Name: "access/main.sigil", Source: read(t, "testdata/access/main.sigil")}},
	}}
	tests := []struct {
		name     string
		input    string
		stdin    string
		terminal bool
		want     string // the start of the output
		wantErr  string
	}{
		{name: "stdin", stdin: `{"user": {"name": "ada", "admin": true}}`, want: "access.main: allow(reason: admin)"},
		{name: "a file", input: "testdata/inputs/admin.yaml", terminal: true, want: "access.main: allow(reason: admin)"},
		{name: "a terminal", terminal: true, wantErr: "--input is required when stdin is a terminal"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			format := output.Text
			o := &options{output: &format, payload: p, name: "gate"}
			var out bytes.Buffer
			err := runCompiled(context.Background(), &out, o, request{input: tt.input, terminal: tt.terminal, src: project.Sources{Stdin: strings.NewReader(tt.stdin)}})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || !strings.Contains(strings.Join(err.Advice(), " "), "`gate eval < input.json`") {
					t.Fatalf("runCompiled() = %v (advice %q), want %q with advice calling gate", err, err.Advice(), tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("runCompiled() = %v", err)
			}
			if !strings.HasPrefix(out.String(), tt.want) {
				t.Errorf("output = %q, want it to start with %q", out.String(), tt.want)
			}
		})
	}
}
