package explain

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
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
		{name: "no policies", paths: []string{"testdata/deploy_approval.sigil"}, err: "the bundle holds no policies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind := filepath.Join("testdata", "deploy_approval.sigil")
			if tt.name == "unknown kind file" {
				kind = "nope.sigil"
			}
			var out bytes.Buffer
			format := tt.format
			if format == "" {
				format = output.Text
			}
			src := project.Sources{Paths: tt.paths, Recursive: true, Stdin: strings.NewReader("")}
			err := run(&out, &options{output: &format}, kind, tt.pattern, src)
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
			want, rerr := os.ReadFile(golden)
			if rerr != nil {
				t.Fatalf("%v (run with -update to create it)", rerr)
			}
			if out.String() != string(want) {
				t.Errorf("output differs from %s (run with -update to accept):\n--- got ---\n%s\n--- want ---\n%s", golden, out.String(), want)
			}
		})
	}
}
