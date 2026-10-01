package compile

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/stamp"
)

// TestRealBinary builds cmd/sigil, natively and for the platforms whose
// binaries differ in what compile has to keep valid, compiles the
// several bundle with each, and checks the output: its code signature
// matches its contents, and it holds the bundle. The native output runs:
// its version names the bundle's digest, and its eval evaluates a policy
// from it with no file around.
func TestRealBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds sigil")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	tests := []struct {
		name         string
		goos, goarch string
	}{
		{name: "native", goos: runtime.GOOS, goarch: runtime.GOARCH},
		{name: "darwin/arm64", goos: "darwin", goarch: "arm64"},
		{name: "darwin/amd64", goos: "darwin", goarch: "amd64"},
		{name: "linux/amd64", goos: "linux", goarch: "amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exe := filepath.Join(t.TempDir(), "sigil")
			build := exec.Command(goTool, "build", "-o", exe, "github.com/spechtlabs/sigil/cmd/sigil")
			build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+tt.goos, "GOARCH="+tt.goarch)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			out := filepath.Join(t.TempDir(), "gates")
			var stdout bytes.Buffer
			req := request{src: project.Sources{Paths: []string{gate, several}}, out: out, policy: "compile.b", config: configFile("require.yaml")}
			if err := run(&stdout, testOptions(output.JSON, exe), req); err != nil {
				t.Fatalf("run() = %v", err)
			}
			bin, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if err := stamp.Verify(bin); err != nil {
				t.Fatalf("Verify() = %v", err)
			}
			p := decode(t, out)
			digest := p.Bundle.Digest()
			if p.Bundle.Root != "compile.b" || !strings.Contains(stdout.String(), digest) {
				t.Fatalf("root %q, digest %s, output %s: want compile.b, and the digest reported", p.Bundle.Root, digest, stdout.String())
			}
			if tt.goos != runtime.GOOS || tt.goarch != runtime.GOARCH {
				return
			}
			// The output runs somewhere the policies aren't.
			version := exec.Command(out, "version", "-o", "json")
			version.Dir = t.TempDir()
			if got, err := version.CombinedOutput(); err != nil || !strings.Contains(string(got), digest) {
				t.Errorf("version: %v\n%s\nwant the digest %s", err, got, digest)
			}
			evaluate := exec.Command(out, "eval")
			evaluate.Dir = t.TempDir()
			evaluate.Stdin = strings.NewReader(`{"user": ""}`)
			if got, err := evaluate.CombinedOutput(); err != nil || !strings.HasPrefix(string(got), "compile.b: deny(reason: nobody)") {
				t.Errorf("eval: %v\n%s\nwant compile.b's decision from compile.guard", err, got)
			}
		})
	}
}
