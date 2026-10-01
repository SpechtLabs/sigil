package payload_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/payload"
)

// TestBinary builds testdata/embedded, a program that prints its payload,
// for several platforms, and checks that the reserved area is in the
// binary file, all of it, with the marker exactly once. The native build
// runs, prints that it has no payload, and, with a payload written after
// the marker, prints that one: the area the program reads is the one in
// the file.
func TestBinary(t *testing.T) {
	if testing.Short() {
		t.Skip("builds binaries")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go toolchain on PATH")
	}
	tests := []struct {
		name         string
		goos, goarch string
		ldflags      string
	}{
		{name: "native", goos: runtime.GOOS, goarch: runtime.GOARCH},
		{name: "native, stripped", goos: runtime.GOOS, goarch: runtime.GOARCH, ldflags: "-s -w"},
		{name: "linux/amd64", goos: "linux", goarch: "amd64"},
		{name: "linux/arm64", goos: "linux", goarch: "arm64"},
		{name: "darwin/arm64", goos: "darwin", goarch: "arm64"},
		{name: "darwin/amd64", goos: "darwin", goarch: "amd64"},
		{name: "windows/amd64", goos: "windows", goarch: "amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			exe := filepath.Join(t.TempDir(), "embedded")
			build := exec.Command(goTool, "build", "-trimpath", "-ldflags="+tt.ldflags, "-o", exe, "./testdata/embedded")
			build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS="+tt.goos, "GOARCH="+tt.goarch)
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			bin, err := os.ReadFile(exe)
			if err != nil {
				t.Fatal(err)
			}
			if n := bytes.Count(bin, payload.Marker()); n != 1 {
				t.Fatalf("the binary holds the marker %d times, want once", n)
			}
			at := bytes.Index(bin, payload.Marker())
			if len(bin)-at < payload.AreaSize || bytes.Count(bin[at+8:at+payload.AreaSize], []byte{0}) != payload.AreaSize-8 {
				t.Fatalf("the area after the marker at %d isn't %d zero bytes in the file", at, payload.AreaSize-8)
			}
			if tt.goos != runtime.GOOS || tt.goarch != runtime.GOARCH {
				return
			}
			if got := run(t, exe); got != "none" {
				t.Fatalf("an unpatched binary printed %q, want none", got)
			}

			data, err := payload.Encode(&payload.Payload{Bundle: *base(), Sigil: "v1.2.3"})
			if err != nil {
				t.Fatal(err)
			}
			copy(bin[at+8:], data)
			if err := os.WriteFile(exe, bin, 0o755); err != nil {
				t.Fatal(err)
			}
			resign(t, exe)
			if got, want := run(t, exe), "team.main "+baseDigest; got != want {
				t.Errorf("a patched binary printed %q, want %q", got, want)
			}
		})
	}
}

// run runs a test binary and returns what it printed, trimmed.
func run(t *testing.T, exe string) string {
	t.Helper()
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", exe, err, out)
	}
	return strings.TrimSpace(string(out))
}

// resign signs a patched binary again, ad hoc, where the kernel insists
// on a valid signature, as on darwin; package stamp does this itself by
// re-hashing only the changed pages.
func resign(t *testing.T, exe string) {
	t.Helper()
	if runtime.GOOS != "darwin" {
		return
	}
	codesign, err := exec.LookPath("codesign")
	if err != nil {
		t.Skip("no codesign to sign the patched binary again")
	}
	if out, err := exec.Command(codesign, "--force", "--sign", "-", exe).CombinedOutput(); err != nil {
		t.Fatalf("codesign: %v\n%s", err, out)
	}
}
