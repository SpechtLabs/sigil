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
		noKind  bool // no --kind: the kind comes from the paths
	}{
		{name: "team", pattern: "payments.production", format: output.Text, paths: []string{"testdata/deploy", "testdata/payments"}},
		{name: "team_json", pattern: "payments.production", format: output.JSON, paths: []string{"testdata/deploy", "testdata/payments"}},
		{name: "pattern", pattern: "deploy.*", format: output.Text, paths: []string{"testdata"}},
		{name: "kind_among_paths", pattern: "deploy.*", paths: []string{"testdata"}, noKind: true},
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
			src := project.Sources{Paths: tt.paths, Kinds: []string{kind}, Stdin: strings.NewReader("")}
			if tt.noKind {
				src.Kinds = nil
			}
			err := run(&out, &options{output: &format}, "", tt.pattern, src)
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

// TestCurrentDirectory checks that without paths, explain reads the
// working directory, every level of it, kind file included.
func TestCurrentDirectory(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("testdata", "pattern.golden"))
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir("testdata")
	format := output.Text
	var out bytes.Buffer
	if err := run(&out, &options{output: &format}, "", "deploy.*", project.Sources{}); err != nil {
		t.Fatalf("run() = %v", err)
	}
	if out.String() != string(want) {
		t.Errorf("output = %q, want pattern.golden's", out.String())
	}
}

func TestCount(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{n: 0, want: "0 rules"},
		{n: 1, want: "1 rule"},
		{n: 9, want: "9 rules"},
	}
	for _, tt := range tests {
		if got := count(tt.n, "rule", "rules"); got != tt.want {
			t.Errorf("count(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

// TestConfigKinds checks that explain loads the kind files the nearest
// sigil.yaml, or the one --config names, lists under kinds:.
func TestConfigKinds(t *testing.T) {
	tests := []struct {
		name    string
		config  string // sigil.yaml, one level above the policies
		flag    string // --config, relative to the policies
		wantErr string
	}{
		{name: "nearest", config: "kinds: [vendor/deploy_approval.sigil]\n"},
		{name: "named", config: "kinds: vendor/deploy_approval.sigil\n", flag: "../sigil.yaml"},
		{name: "missing kind file", config: "kinds: [vendor/nope.sigil]\n", wantErr: "the kind file ../vendor/nope.sigil can't be read"},
		{name: "invalid", config: "kinds: [vendor/deploy_approval.sigil\n", wantErr: "isn't valid YAML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{"sigil.yaml": tt.config, "vendor/deploy_approval.sigil": "deploy_approval.sigil"}
			for _, f := range []string{"common", "guardrails", "production"} {
				files["policies/deploy/"+f+".sigil"] = "deploy/" + f + ".sigil"
			}
			files["policies/payments/production.sigil"] = "payments/production.sigil"
			for to, from := range files {
				src := []byte(tt.config)
				if to != "sigil.yaml" {
					var err error
					if src, err = os.ReadFile(filepath.Join("testdata", filepath.FromSlash(from))); err != nil {
						t.Fatal(err)
					}
				}
				path := filepath.Join(dir, filepath.FromSlash(to))
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, src, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(filepath.Join(dir, "policies"))
			format := output.Text
			var out bytes.Buffer
			err := run(&out, &options{output: &format}, tt.flag, "payments.production", project.Sources{})
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("run() = %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("run() = %v, want %q", err, tt.wantErr)
			case tt.wantErr == "" && !strings.HasPrefix(out.String(), "payments.production: "):
				t.Errorf("output = %q, want payments.production explained", out.String())
			}
		})
	}
}
