package explain

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// TestExplain runs explain over the testdata bundle in every output
// format and compares with the golden files.
func TestExplain(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		format  output.Format
		paths   []string
		err     string
	}{
		{name: "team", pattern: "payments.production", format: output.Text, paths: []string{"testdata/deploy", "testdata/payments"}},
		{name: "team_json", pattern: "payments.production", format: output.JSON, paths: []string{"testdata/deploy", "testdata/payments"}},
		{name: "pattern", pattern: "deploy.*", format: output.Text, paths: []string{"testdata"}},
		{name: "no match", pattern: "nope.*", paths: []string{"testdata"}, err: `no policy matches "nope.*"`},
		{name: "unknown kind file", pattern: "", paths: []string{"testdata"}, err: "the kind file couldn't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind := filepath.Join("testdata", "deploy_approval.sigil")
			if tt.name == "unknown kind file" {
				kind = "nope.sigil"
			}
			var out bytes.Buffer
			err := run(&out, strings.NewReader(""), kind, tt.pattern, true, tt.paths, tt.format)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("run() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			golden := filepath.Join("testdata", tt.name+".golden")
			if *update {
				if werr := os.WriteFile(golden, out.Bytes(), 0o644); werr != nil {
					t.Fatal(werr)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if out.String() != string(want) {
				t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", golden, out.String(), want)
			}
		})
	}
}
