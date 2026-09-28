package placeholder

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestNotImplemented checks the error in every output format: returned in
// text, and printed as a record, with the error already reported, in JSON
// and YAML.
func TestNotImplemented(t *testing.T) {
	const msg = `"sigil lsp" is not implemented yet`
	tests := []struct {
		name   string
		format output.Format
	}{
		{name: "text", format: output.Text},
		{name: "json", format: output.JSON},
		{name: "yaml", format: output.YAML},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := &cobra.Command{Use: "sigil"}
			cmd := &cobra.Command{Use: "lsp"}
			root.AddCommand(cmd)
			var out bytes.Buffer
			cmd.SetOut(&out)

			err := NotImplemented(cmd, tt.format)
			if err == nil || err.Error() != msg {
				t.Fatalf("NotImplemented() = %v, want %q", err, msg)
			}
			if got := pretty.Reported(err); got != (tt.format != output.Text) {
				t.Errorf("Reported() = %v, want the error printed only in JSON and YAML", got)
			}
			if tt.format == output.Text {
				if out.Len() != 0 {
					t.Errorf("printed %q in text, want nothing", out.String())
				}
				return
			}
			golden(t, tt.name, out.String())
		})
	}
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
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
