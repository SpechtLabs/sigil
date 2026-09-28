// Package gotool runs the go and git commands devtool is built around, and
// finds test functions in the packages go list reports.
package gotool

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// topLevelFunc matches a top-level function declaration and captures its
// name, e.g. FuzzParse in `func FuzzParse(f *testing.F) {`.
var topLevelFunc = regexp.MustCompile(`(?m)^func (\w+)\(`)

// commandOutput is what a failed command printed to standard error: the
// cause of its failure.
type commandOutput string

// Package is the part of `go list -json` devtool uses.
type Package struct {
	ImportPath   string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
}

// Func is a test function and the file it's declared in, relative to the
// package directory.
type Func struct {
	File string
	Name string
}

// Output runs name with args in dir and returns its standard output. env
// replaces the inherited environment when it isn't nil. A failure carries
// the command's standard error.
func Output(ctx context.Context, dir string, env []string, name string, args ...string) (string, humane.Error) {
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // running go, git and benchstat is what devtool is for
	cmd.Dir = dir
	cmd.Env = env
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// What the command printed says why; its exit status adds nothing.
		// Continuation lines are indented to sit under the first one in
		// the error's "Caused by" list.
		msg := strings.Join(append([]string{name}, args...), " ") + " failed"
		advice := "fix the problem it reports, then run devtool again"
		if s := strings.TrimSpace(stderr.String()); s != "" {
			return "", humane.Wrap(commandOutput(strings.ReplaceAll(s, "\n", "\n    ")), msg, advice)
		}
		return "", humane.Wrap(err, msg, advice)
	}
	return stdout.String(), nil
}

// ModuleRoot returns the root directory of the Go module containing dir.
func ModuleRoot(ctx context.Context, dir string) (string, humane.Error) {
	gomod, err := Output(ctx, dir, nil, "go", "env", "GOMOD")
	if err != nil {
		return "", err
	}
	gomod = strings.TrimSpace(gomod)
	if gomod == "" || gomod == os.DevNull {
		return "", humane.New("not inside a Go module", "run devtool from the sigil repository")
	}
	return filepath.Dir(gomod), nil
}

// List runs `go list -json` on patterns in dir. Packages of nested modules
// aren't matched by ./..., so they're never listed.
func List(ctx context.Context, dir string, patterns ...string) ([]Package, humane.Error) {
	out, err := Output(ctx, dir, nil, "go", append([]string{"list", "-json"}, patterns...)...)
	if err != nil {
		return nil, err
	}
	var pkgs []Package
	dec := json.NewDecoder(strings.NewReader(out))
	for {
		var p Package
		derr := dec.Decode(&p)
		if errors.Is(derr, io.EOF) {
			return pkgs, nil
		}
		if derr != nil {
			return nil, humane.Wrap(derr, "go list printed JSON devtool can't read", "check the Go toolchain version pinned in .mise.toml")
		}
		pkgs = append(pkgs, p)
	}
}

// Funcs returns the package's test functions whose names start with
// prefix, in file order.
func (p Package) Funcs(prefix string) ([]Func, humane.Error) {
	var funcs []Func
	for _, file := range append(append([]string(nil), p.TestGoFiles...), p.XTestGoFiles...) {
		src, err := os.ReadFile(filepath.Join(p.Dir, file)) //nolint:gosec // a test file go list reported
		if err != nil {
			return nil, humane.Wrap(err, "can't read test file "+file, "check the file's permissions")
		}
		for _, m := range topLevelFunc.FindAllSubmatch(src, -1) {
			if name := string(m[1]); strings.HasPrefix(name, prefix) {
				funcs = append(funcs, Func{File: file, Name: name})
			}
		}
	}
	return funcs, nil
}

// Error returns the output.
func (c commandOutput) Error() string { return string(c) }

// RelDir returns dir relative to root in the ./dir form go and devtool
// accept as a package pattern, or dir itself when it's outside root.
func RelDir(root, dir string) string {
	rel, err := filepath.Rel(root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return dir
	}
	if rel == "." {
		return "."
	}
	return "./" + filepath.ToSlash(rel)
}
