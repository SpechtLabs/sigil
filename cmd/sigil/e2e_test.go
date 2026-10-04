package main

import (
	"bytes"
	"debug/buildinfo"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/command/compile"
	"github.com/spechtlabs/sigil/internal/payload"
	"github.com/spechtlabs/sigil/internal/stamp"
)

// The end-to-end tests build sigil as a release does, compile bundles
// with it, and run what it wrote as a user would: a separate process with
// no policy file around. They need the go command, and they're skipped
// under -short.
const (
	// release is the version the tests build sigil with, as GoReleaser
	// injects it, so every compiled binary records it.
	release = "v0.0.0-e2e"
	// epoch is SOURCE_DATE_EPOCH for every compile, 2026-01-01T00:00:00Z,
	// so every compiled binary records it as its build time.
	epoch = "1767225600"
	// gates is the deploy-gates example, a module of its own, whose
	// policies need the host function split.
	gates = "../../examples/deploy-gates"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata/golden")

// sigil is the binary TestMain built: empty under -short, or when there's
// no go command to build it with.
var sigil string

// What differs between machines, which the golden files leave out: the
// Go version and the platform of the binary that compiled, and the size
// of the encoded bundle, which is compress/flate's and may change with a
// Go release.
var (
	bytesField = regexp.MustCompile(`("bytes": |bytes: )\d+`)
	buildField = regexp.MustCompile(`("(?:goVersion|platform)":)"[^"]*"`)
	buildLine  = regexp.MustCompile(`(?m)^((?:Go version|Platform):\s+).+$`)
	// digest is a bundle's digest, whole or shortened, which the goldens
	// built from the deploy-gates example leave out: it changes with any
	// edit to the example's policies.
	digest = regexp.MustCompile(`sha256:[0-9a-f]+…?`)
)

// step is one run of a binary, and the golden file that holds what it
// printed and the status it exited with.
type step struct {
	name  string   // the golden file under testdata/golden, without .golden
	bin   string   // the binary that runs, by the name the test built it under
	args  []string // its arguments
	dir   string   // where it runs: testdata when empty
	stdin string   // a file it reads as stdin, under testdata; none when empty
	want  string   // what the output must hold besides matching the golden file
	mask  bool     // whether the golden file leaves the bundle's digest out
}

// TestMain builds cmd/sigil once for every test, as a release builds it:
// with cgo off, stripped, and with the version injected.
func TestMain(m *testing.M) {
	flag.Parse()
	os.Exit(runTests(m))
}

// TestCompile compiles the testdata bundle with and without a default
// policy and compares what compile prints with the golden files. The
// binary it writes has a code signature that matches its contents and
// carries the bundle, the build time from SOURCE_DATE_EPOCH, and the
// version of the sigil that compiled it.
func TestCompile(t *testing.T) {
	exe := built(t)
	tests := []struct {
		name string
		args []string
		root string
	}{
		{name: "compile", args: []string{"teams"}},
		{name: "compile_json", args: []string{"-o", "json", "teams"}},
		{name: "compile_policy", args: []string{"--policy", "shop.refunds", "teams"}, root: "shop.refunds"},
		{name: "compile_flags", args: []string{"--require", "refunds.guardrails", "--trusted", "platform", "--policy", "support.refunds", "teams/support.sigil"}, root: "support.refunds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			bin, printed := compileWith(t, exe, "testdata", "refunds", tt.args...)
			golden(t, tt.name, printed)
			p := embedded(t, bin)
			if p.Bundle.Root != tt.root || p.Sigil != release || strconv.FormatInt(p.Built.Unix(), 10) != epoch {
				t.Errorf("root %q, sigil %q, built %v: want %q, %q, 2026-01-01", p.Bundle.Root, p.Sigil, p.Built, tt.root, release)
			}
		})
	}
}

// TestCompiled compiles the testdata bundle twice, with no default
// policy and with shop.refunds, runs each binary's commands where the
// policies are, and once where nothing is, and compares what they print
// with the golden files.
func TestCompiled(t *testing.T) {
	exe := built(t)
	refunds, _ := compileWith(t, exe, "testdata", "refunds", "teams")
	shop, _ := compileWith(t, exe, "testdata", "shop", "--policy", "shop.refunds", "teams")
	bins := map[string]string{"refunds": refunds, "shop": shop}
	nowhere := t.TempDir()
	steps := []step{
		{name: "several_no_policy", bin: "refunds", args: []string{"eval", "-i", "inputs/small.json"}},
		{name: "several_input", bin: "refunds", args: []string{"eval", "-p", "shop.refunds", "-i", "inputs/small.json"}, want: "approve(reason: within_limit)"},
		{name: "several_stdin", bin: "refunds", args: []string{"eval", "-p", "support.refunds"}, stdin: "inputs/large.json", want: `queue = "support-leads"`},
		{name: "several_json", bin: "refunds", args: []string{"eval", "-o", "json", "-p", "support.refunds", "-i", "inputs/old.json"}, want: `"bundle": "sha256:`},
		{name: "several_assert_json", bin: "refunds", args: []string{"-o", "json", "eval", "-p", "support.refunds", "-i", "inputs/unnamed.yaml"}, want: `"reason": "named_agent"`},
		{name: "several_explain", bin: "refunds", args: []string{"explain"}},
		{name: "several_explain_json", bin: "refunds", args: []string{"explain", "-o", "json", "-p", "support.*"}},
		{name: "several_test", bin: "refunds", args: []string{"test", "-v", "tests"}},
		{name: "several_test_failing", bin: "refunds", args: []string{"test", "failing"}},
		{name: "several_version_json", bin: "refunds", args: []string{"version", "-o", "json"}},
		{name: "shop_input", bin: "shop", args: []string{"eval", "--input", "inputs/large.json"}, want: "review(reason: large_amount)"},
		{name: "shop_stdin", bin: "shop", stdin: "inputs/unnamed.yaml", dir: nowhere, args: []string{"eval"}, want: "named_agent"},
		{name: "shop_stdin_json", bin: "shop", stdin: "inputs/small.json", dir: nowhere, args: []string{"eval", "-o", "json"}},
		{name: "shop_left_out", bin: "shop", args: []string{"eval", "-p", "support.refunds", "-i", "inputs/small.json"}},
		{name: "shop_explain", bin: "shop", args: []string{"explain"}},
		{name: "shop_test", bin: "shop", args: []string{"test", "tests"}},
		{name: "shop_version", bin: "shop", args: []string{"version"}},
		{name: "shop_version_json", bin: "shop", args: []string{"version", "-o", "json"}, dir: nowhere},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			s.run(t, bins, "")
		})
	}
}

// TestMoved moves a compiled binary away from where compile wrote it, as
// a download or a copy into a container would, deletes the original, and
// runs it somewhere with no file around: its code signature has to hold
// for the new file, and the bundle is all it needs.
func TestMoved(t *testing.T) {
	exe := built(t)
	bin, _ := compileWith(t, exe, "testdata", "shop", "--policy", "shop.refunds", "teams")
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(t.TempDir(), "moved")
	if err = os.WriteFile(dst, data, 0o755); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(bin); err != nil {
		t.Fatal(err)
	}
	moved := step{name: "moved", bin: "moved", dir: t.TempDir(), stdin: "inputs/small.json", args: []string{"eval"}, want: "shop.refunds: approve(reason: within_limit)"}
	moved.run(t, map[string]string{"moved": dst}, "")
}

// TestDeployGates compiles the deploy-gates example with the stock
// sigil. Its deploy policies are written against DeployApproval, which
// declares the host function split, so compile refuses the whole
// directory. Its access policy is pure: compiled with --policy, the
// bundle narrows to it and what it needs, and the binary decides the
// example's access requests as the service does.
func TestDeployGates(t *testing.T) {
	exe := built(t)
	tmp := t.TempDir()
	refused := step{name: "gates_refused", bin: "sigil", dir: gates, args: []string{"compile", "--out", "$TMP/gates", "--config", "policies/sigil.yaml", "policies"}, want: "DeployApproval@2 declares split", mask: true}
	refused.run(t, map[string]string{"sigil": exe}, tmp)
	if _, err := os.Stat(filepath.Join(tmp, "gates")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused compile wrote %s: %v", filepath.Join(tmp, "gates"), err)
	}

	access, printed := compileWith(t, exe, gates, "access", "--config", "policies/sigil.yaml", "--policy", "access.main", "policies")
	golden(t, "access_compile", digest.ReplaceAllString(printed, "sha256:<digest>"))
	bins := map[string]string{"access": access}
	steps := []step{
		{name: "access_member", bin: "access", dir: gates, args: []string{"eval", "-i", "requests/access-member.json"}, want: "deployer(reason: team_member)"},
		{name: "access_outsider", bin: "access", dir: gates, args: []string{"eval", "-i", "requests/access-outsider.json"}, want: "access.main: no decisions"},
		{name: "access_break_glass", bin: "access", dir: gates, args: []string{"eval", "-i", "requests/access-break-glass-platform.json"}, want: "conflict: exclusive admin, release_manager"},
		{name: "access_compliance", bin: "access", dir: gates, args: []string{"eval", "-i", "requests/access-compliance-member.json"}, want: "assert sod_auditor_deployer failed"},
		{name: "access_test", bin: "access", dir: gates, args: []string{"test", "policies/access"}},
		{name: "access_version_json", bin: "access", args: []string{"version", "-o", "json"}},
	}
	for _, s := range steps {
		s.mask = true
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			s.run(t, bins, "")
		})
	}
}

// TestHostBinary builds the deploy-gates example's sigilc, which is
// cli.Main with the DeployApproval and AccessGrant kinds linked in, and
// compiles every policy of the example with it. The binary it writes has
// the real split linked in: the example's policy tests pass, and payments
// decides on the regions a service's label lists.
func TestHostBinary(t *testing.T) {
	built(t)
	sigilc := filepath.Join(t.TempDir(), "sigilc")
	if err := goBuild(gates, sigilc, "./cmd/sigilc"); err != nil {
		t.Fatal(err)
	}
	bin, printed := compileWith(t, sigilc, gates, "gates", "--config", "policies/sigil.yaml", "policies")
	golden(t, "host_compile", digest.ReplaceAllString(printed, "sha256:<digest>"))
	bins := map[string]string{"gates": bin}
	payments := "policies/teams/payments/testdata/"
	steps := []step{
		{name: "host_test", bin: "gates", dir: gates, args: []string{"test", "policies"}, want: "49 cases passed"},
		{name: "host_cleared", bin: "gates", dir: gates, args: []string{"eval", "-p", "payments.production", "-i", payments + "owner.json"}, want: "review(reason: service_owner)"},
		{name: "host_uncleared", bin: "gates", dir: gates, args: []string{"eval", "-p", "payments.production", "-i", payments + "uncleared-region.json"}, want: "deny(reason: no_rule_matched)"},
		{name: "host_access", bin: "gates", dir: gates, args: []string{"eval", "-p", "access.main", "-i", "requests/access-member.json"}, want: "reader(reason: team_member)"},
		{name: "host_version_json", bin: "gates", args: []string{"version", "-o", "json"}},
	}
	for _, s := range steps {
		s.mask = true
		t.Run(s.name, func(t *testing.T) {
			t.Parallel()
			s.run(t, bins, "")
		})
	}
}

// TestCrossPlatform builds sigil for a platform this host doesn't run,
// darwin/arm64 on Linux and linux/amd64 on macOS, and compiles the
// testdata bundle with compile's command in this process, copying that
// binary instead of the running one. The output is still a Go binary
// for that platform, its code signature, if it has one, matches its
// contents, and it carries the bundle.
func TestCrossPlatform(t *testing.T) {
	built(t)
	goos, goarch := "darwin", "arm64"
	if runtime.GOOS == "darwin" {
		goos, goarch = "linux", "amd64"
	}
	exe := filepath.Join(t.TempDir(), "sigil")
	if err := goBuild(".", exe, ".", "GOOS="+goos, "GOARCH="+goarch); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SOURCE_DATE_EPOCH", epoch)
	out := filepath.Join(t.TempDir(), "shop")
	cmd := compile.NewCommand(compile.WithExecutable(func() (string, error) { return exe, nil }))
	cmd.SetArgs([]string{"--out", out, "--config", "testdata/sigil.yaml", "--policy", "shop.refunds", "testdata/teams"})
	var printed bytes.Buffer
	cmd.SetOut(&printed)
	cmd.SetErr(&printed)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("compile: %v\n%s", err, printed.String())
	}
	info, err := buildinfo.ReadFile(out)
	if err != nil {
		t.Fatalf("the compiled binary isn't a Go binary any more: %v", err)
	}
	if got := setting(info, "GOOS") + "/" + setting(info, "GOARCH"); got != goos+"/"+goarch {
		t.Errorf("the compiled binary is built for %s, want %s/%s", got, goos, goarch)
	}
	bin, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err = stamp.Verify(bin); err != nil {
		t.Errorf("Verify() = %v", err)
	}
	if p := embedded(t, out); p.Bundle.Root != "shop.refunds" {
		t.Errorf("root %q, want shop.refunds", p.Bundle.Root)
	}
}

// runTests builds sigil into a temporary directory, unless -short skips
// the tests, runs them, and removes the directory.
func runTests(m *testing.M) int {
	if testing.Short() {
		return m.Run()
	}
	if _, err := exec.LookPath("go"); err != nil {
		return m.Run()
	}
	dir, err := os.MkdirTemp("", "sigil-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = os.RemoveAll(dir) }()
	sigil = filepath.Join(dir, "sigil")
	if err = goBuild(".", sigil, "."); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return m.Run()
}

// built returns the sigil TestMain built, or skips the test when there's
// none.
func built(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("builds sigil and runs it")
	}
	if sigil == "" {
		t.Skip("no go command on PATH to build sigil with")
	}
	return sigil
}

// goBuild builds the package pkg of the module in dir into out, an
// absolute path, as a release does. env adds to the environment, such as
// GOOS=linux to build for another platform.
func goBuild(dir, out, pkg string, env ...string) error {
	args := []string{"build", "-buildvcs=false", "-ldflags", "-s -w -X=main.version=" + release, "-o", out, pkg}
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), append([]string{"CGO_ENABLED=0"}, env...)...)
	if got, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("go %s: %w\n%s", strings.Join(args, " "), err, got)
	}
	return nil
}

// compileWith runs exe's compile in dir with args, writing to a binary
// called name in a temporary directory, and fails the test unless it
// succeeds. It returns the binary, which has a code signature that
// matches its contents, and what compile printed.
func compileWith(t *testing.T, exe, dir, name string, args ...string) (bin, printed string) {
	t.Helper()
	tmp := t.TempDir()
	s := step{name: name, bin: filepath.Base(exe), dir: dir, args: append([]string{"compile", "--out", "$TMP/" + name}, args...)}
	code, printed := s.exec(t, map[string]string{s.bin: exe}, tmp)
	if code != 0 {
		t.Fatalf("compile failed:\n%s", printed)
	}
	bin = filepath.Join(tmp, name)
	data, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	if err = stamp.Verify(data); err != nil {
		t.Fatalf("Verify(%s) = %v", bin, err)
	}
	return bin, printed
}

// run runs the step and compares what it printed with its golden file.
func (s step) run(t *testing.T, bins map[string]string, tmp string) {
	t.Helper()
	_, printed := s.exec(t, bins, tmp)
	if s.mask {
		printed = digest.ReplaceAllString(printed, "sha256:<digest>")
	}
	golden(t, s.name, printed)
	if !strings.Contains(printed, s.want) {
		t.Errorf("%s printed\n%s\nwant it to hold %q", s.name, printed, s.want)
	}
}

// exec runs the step's binary, with $TMP in its arguments standing for
// tmp, and returns its exit status and a transcript: the command line,
// stdout, stderr, and the exit status when it isn't 0, with what differs
// between machines left out.
func (s step) exec(t *testing.T, bins map[string]string, tmp string) (int, string) {
	t.Helper()
	args := make([]string, len(s.args))
	for i, a := range s.args {
		args[i] = strings.ReplaceAll(a, "$TMP", tmp)
	}
	cmd := exec.Command(bins[s.bin], args...)
	cmd.Dir = s.dir
	if cmd.Dir == "" {
		cmd.Dir = "testdata"
	}
	cmd.Env = environ()
	if s.stdin != "" {
		f, err := os.Open(filepath.Join("testdata", s.stdin))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		cmd.Stdin = f
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exit := (*exec.ExitError)(nil); errors.As(err, &exit) {
		code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", s.bin, err)
	}

	var b strings.Builder
	b.WriteString("$ " + strings.Join(append([]string{s.bin}, s.args...), " "))
	if s.stdin != "" {
		b.WriteString(" < " + s.stdin)
	}
	b.WriteString("\n" + stdout.String())
	if stderr.Len() > 0 {
		b.WriteString("--- stderr\n" + stderr.String())
	}
	if code != 0 {
		fmt.Fprintf(&b, "--- exit status %d\n", code)
	}
	got := b.String()
	if tmp != "" {
		got = strings.ReplaceAll(got, tmp, "$TMP")
	}
	got = bytesField.ReplaceAllString(got, "${1}<n>")
	got = buildField.ReplaceAllString(got, `$1"<build>"`)
	return code, buildLine.ReplaceAllString(got, "${1}<build>")
}

// environ is the environment the binaries run in: the test's, without
// color, and with SOURCE_DATE_EPOCH set to epoch.
func environ() []string {
	var env []string
	for _, kv := range os.Environ() {
		switch k, _, _ := strings.Cut(kv, "="); k {
		case "CLICOLOR_FORCE", "FORCE_COLOR":
		default:
			env = append(env, kv)
		}
	}
	return append(env, "NO_COLOR=1", "SOURCE_DATE_EPOCH="+epoch)
}

// embedded returns the payload compiled into the binary at path, which
// must hold the reserved area once.
func embedded(t *testing.T, path string) *payload.Payload {
	t.Helper()
	bin, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := payload.Marker()
	at := bytes.Index(bin, marker)
	if at < 0 || bytes.Count(bin, marker) != 1 {
		t.Fatalf("%s holds the marker %d times, want once", path, bytes.Count(bin, marker))
	}
	p, herr := payload.Decode(bin[at+len(marker) : at+payload.AreaSize])
	if herr != nil || p == nil {
		t.Fatalf("Decode() = %v, %v, want a payload", p, herr)
	}
	return p
}

// setting returns the build setting key of info, or empty.
func setting(info *buildinfo.BuildInfo, key string) string {
	for _, s := range info.Settings {
		if s.Key == key {
			return s.Value
		}
	}
	return ""
}

// golden compares got with testdata/golden/name.golden, or rewrites the
// file under -update.
func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name+".golden")
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
