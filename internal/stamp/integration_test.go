package stamp_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/stamp"
)

// helloArea is the size of the area in testdata/hello.
const helloArea = 1 << 20

// signer is a way to sign a darwin build.
type signer struct {
	name   string
	args   []string // codesign's, or nil to keep the linker's signature
	signed bool
}

// TestBuilt builds testdata/hello for each platform, signs the darwin
// builds with codesign as well when it's there, patches every binary, and
// checks its signature with Verify. The builds for this host run: patched,
// a binary prints its payload; patched without re-hashing, a signed darwin
// binary must be killed, which proves that the kernel checks the pages
// Patch re-hashes. On a host that doesn't enforce code signatures, the
// control runs, and that check is skipped.
func TestBuilt(t *testing.T) {
	if testing.Short() {
		t.Skip("builds testdata/hello for several platforms")
	}
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH")
	}
	codesign, _ := exec.LookPath("codesign")

	tests := []struct {
		goos, goarch string
		signed       bool // whether Go's linker signs it
	}{
		{goos: "darwin", goarch: "arm64", signed: true},
		{goos: "darwin", goarch: "amd64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "windows", goarch: "amd64"},
	}
	for _, tt := range tests {
		t.Run(tt.goos+"/"+tt.goarch, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			built := filepath.Join(dir, "hello")
			cmd := exec.Command(goTool, "build", "-o", built, ".")
			cmd.Dir = filepath.Join("testdata", "hello")
			cmd.Env = append(os.Environ(), "GOOS="+tt.goos, "GOARCH="+tt.goarch, "CGO_ENABLED=0", "GOWORK=off", "GOFLAGS=")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("go build: %v\n%s", err, out)
			}
			native := tt.goos == runtime.GOOS && tt.goarch == runtime.GOARCH
			if native {
				if out := run(t, built); out != "no payload\n" {
					t.Errorf("before the patch, hello printed %q", out)
				}
			}

			signers := []signer{{name: "linker", signed: tt.signed}}
			if tt.goos == "darwin" && codesign != "" {
				signers = append(signers,
					signer{name: "codesign", args: []string{"-f", "-s", "-"}, signed: true},
					signer{name: "codesign SHA-1 and SHA-256", args: []string{"-f", "-s", "-", "--digest-algorithm=sha1,sha256"}, signed: true},
				)
			}
			for _, s := range signers {
				t.Run(s.name, func(t *testing.T) {
					exe := filepath.Join(t.TempDir(), "hello")
					if err := copyFile(built, exe); err != nil {
						t.Fatal(err)
					}
					if s.args != nil {
						if out, err := exec.Command(codesign, append(s.args, exe)...).CombinedOutput(); err != nil {
							t.Fatalf("codesign: %v\n%s", err, out)
						}
					}
					checkBuilt(t, exe, native, s.signed, codesign)
				})
			}
		})
	}
}

// checkBuilt patches the built binary at exe and checks the result.
func checkBuilt(t *testing.T, exe string, native, signed bool, codesign string) {
	t.Helper()
	b, rerr := os.ReadFile(exe)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if err := stamp.Verify(b); err != nil {
		t.Fatalf("Verify() before the patch = %v", err)
	}
	payload := []byte("hello from stamp")

	// The control: the payload written, the signature left alone.
	control := bytes.Clone(b)
	copy(control[bytes.Index(control, marker())+len(marker()):], payload)
	if err := stamp.Verify(control); signed != errors.Is(err, stamp.ErrMismatch) {
		t.Errorf("Verify() of the binary patched without re-hashing = %v", err)
	}

	// Two patches, so the second one has to clear the first.
	if err := stamp.Patch(b, marker(), helloArea, bytes.Repeat([]byte{'x'}, 100_000)); err != nil {
		t.Fatalf("Patch() = %v", err)
	}
	if err := stamp.Patch(b, marker(), helloArea, payload); err != nil {
		t.Fatalf("Patch() = %v", err)
	}
	if err := stamp.Verify(b); err != nil {
		t.Fatalf("Verify() after the patch = %v", err)
	}

	// A new file each time: darwin caches a file's signature with its vnode,
	// so a binary that ran once must not be overwritten in place.
	patched := exe + ".patched"
	if err := os.WriteFile(patched, b, 0o755); err != nil {
		t.Fatal(err)
	}
	if signed && codesign != "" {
		if out, err := exec.Command(codesign, "--verify", "--strict", patched).CombinedOutput(); err != nil {
			t.Errorf("codesign --verify: %v\n%s", err, out)
		}
	}
	if !native {
		return
	}
	if out := run(t, patched); out != "payload: hello from stamp\n" {
		t.Errorf("the patched binary printed %q", out)
	}
	if !signed || runtime.GOARCH != "arm64" {
		return // only darwin/arm64 kills a process for an unsigned page
	}
	unhashed := exe + ".control"
	if err := os.WriteFile(unhashed, control, 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(unhashed).CombinedOutput()
	if exit, ok := errors.AsType[*exec.ExitError](err); ok && strings.Contains(exit.String(), "killed") {
		return
	}
	if err == nil {
		// Some hosts don't enforce code signatures, such as GitHub's macOS
		// runners. There the control proves nothing about the kernel;
		// Verify and codesign --verify above still checked the signature.
		t.Skipf("this host doesn't enforce code signatures: the binary patched without re-hashing ran (%s)", csrStatus())
	}
	t.Errorf("the binary patched without re-hashing failed, but wasn't killed: %v\n%s", err, out)
}

// csrStatus describes System Integrity Protection on this host, for the
// message of a skipped control.
func csrStatus() string {
	out, err := exec.Command("csrutil", "status").CombinedOutput()
	if err != nil {
		return "csrutil status: " + err.Error()
	}
	return strings.TrimSpace(string(out))
}

// run runs exe and returns its output.
func run(t *testing.T, exe string) string {
	t.Helper()
	out, err := exec.Command(exe).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", exe, err, out)
	}
	return string(out)
}

// copyFile copies the file at from to a new file at to.
func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, b, 0o755)
}
