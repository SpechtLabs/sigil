package compile

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/internal/output"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/payload"
	"github.com/spechtlabs/sigil/internal/stamp"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// The testdata bundles: the Gate kind, a bundle with one policy, one with
// two that each invoke compile.guard from testdata/trusted, and one with
// a warning.
var (
	gate    = filepath.Join("testdata", "gate.sigil")
	single  = filepath.Join("testdata", "single")
	several = filepath.Join("testdata", "several")
	trusted = filepath.Join("testdata", "trusted")
	warn    = filepath.Join("testdata", "warn")
	hostfn  = filepath.Join("testdata", "hostfn")
	mixed   = filepath.Join("testdata", "mixed")
)

// bytesField is the size of the encoded bundle in a record, which the
// goldens leave out: it's compress/flate's, which a Go release may change.
var bytesField = regexp.MustCompile(`("bytes": |bytes: )\d+`)

// failingWriter fails every write, as a closed pipe does.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed pipe") }

// TestCompile runs compile over the testdata bundles with a stand-in
// binary and compares what it prints, and the error it fails with, with
// the golden files.
func TestCompile(t *testing.T) {
	const broken = "policy compile.broken: Gate@1\n\nwhen nobody {\n  allow(reason: admin)\n}\n"
	tests := []struct {
		name     string
		format   output.Format
		config   string // under testdata; defaults.yaml when empty
		paths    []string
		policy   string
		requires []string
		trusted  []string
		stdin    string
		epoch    string // SOURCE_DATE_EPOCH
	}{
		{name: "single", paths: []string{gate, single}},
		{name: "single_json", format: output.JSON, paths: []string{gate, single}},
		{name: "single_yaml", format: output.YAML, paths: []string{gate, single}},
		{name: "several", config: "require.yaml", paths: []string{gate, several}},
		{name: "several_json", config: "require.yaml", format: output.JSON, paths: []string{gate, several}},
		{name: "several_policy", config: "require.yaml", paths: []string{gate, several}, policy: "compile.b"},
		{name: "several_pattern", config: "require.yaml", paths: []string{gate, several}, policy: "compile.*"},
		{name: "policy_missing", paths: []string{gate, single}, policy: "compile.nope"},
		{name: "require_flags", paths: []string{gate, several}, requires: []string{"compile.guard"}, trusted: []string{trusted}},
		{name: "guard_missing", paths: []string{gate, several}},
		{name: "warnings", paths: []string{gate, warn}},
		{name: "warnings_json", format: output.JSON, paths: []string{gate, warn}},
		{name: "warnings_yaml", format: output.YAML, paths: []string{gate, warn}},
		{name: "errors", paths: []string{gate, single, "-"}, stdin: broken},
		{name: "errors_json", format: output.JSON, paths: []string{gate, single, "-"}, stdin: broken},
		{name: "errors_out_of_scope", paths: []string{gate, single, "-"}, stdin: broken, policy: "compile.single"},
		{name: "host_functions", paths: []string{hostfn}},
		{name: "no_policies", paths: []string{gate}},
		{name: "epoch_invalid", paths: []string{gate, single}, epoch: "yesterday"},
		{name: "epoch_negative", paths: []string{gate, single}, epoch: "-1"},
		{name: "path_missing", paths: []string{gate, filepath.Join("testdata", "nope")}},
		{name: "not_utf8", paths: []string{gate, "-"}, stdin: "policy compile.latin1: Gate@1\n\n// caf\xe9\n"},
		{name: "mixed", config: "mixed.yaml", paths: []string{mixed}},
		{name: "mixed_policy", config: "mixed.yaml", paths: []string{mixed}, policy: "compile.mixed_gate"},
		{name: "mixed_policy_lookup", config: "mixed.yaml", paths: []string{mixed}, policy: "compile.mixed_lookup"},
		{name: "riders_unguarded", config: "require.yaml", paths: []string{gate, riders("unguarded")}, policy: "compile.unguarded_a"},
		{name: "riders_unguarded_whole", config: "require.yaml", paths: []string{gate, riders("unguarded")}},
		{name: "riders_mistyped", config: "require.yaml", paths: []string{gate, riders("mistyped")}, policy: "compile.mistyped_a"},
		{name: "riders_mistyped_json", format: output.JSON, config: "require.yaml", paths: []string{gate, riders("mistyped")}, policy: "compile.mistyped_a"},
		{name: "riders_guarded", config: "require.yaml", paths: []string{gate, riders("guarded")}, policy: "compile.guarded_a"},
		{name: "riders_host_functions", paths: []string{gate, filepath.Join(hostfn, "lookup.sigil"), riders("lookup")}, policy: "compile.rider_gate"},
	}
	exe := fakeBinary(t, area())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", tt.epoch)
			format := tt.format
			if format == "" {
				format = output.Text
			}
			out := filepath.Join(t.TempDir(), "policies")
			var stdout bytes.Buffer
			err := run(&stdout, testOptions(format, exe), request{
				src:      project.Sources{Paths: tt.paths, Trusted: tt.trusted, Stdin: strings.NewReader(tt.stdin)},
				out:      out,
				policy:   tt.policy,
				config:   configFile(tt.config),
				requires: tt.requires,
			})
			got := strings.ReplaceAll(render(stdout.String(), err), filepath.Dir(out), "$TMP")
			golden(t, tt.name, bytesField.ReplaceAllString(got, "${1}<n>"))
			if _, serr := os.Stat(out); (serr == nil) != (err == nil) {
				t.Errorf("run() = %v, and %s exists: %v", err, out, serr == nil)
			}
		})
	}
}

// TestBundle checks the payload compile writes into the binary: the root,
// the files by their names relative to the working directory, the
// requirements, the build time and the version of the sigil that compiled
// it.
func TestBundle(t *testing.T) {
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "outside.sigil"), "policy compile.outside: Gate@1\n")
	tests := []struct {
		name    string
		req     request
		epoch   string
		root    string
		kinds   []string
		paths   []string
		trusted []string
		require []payload.Requirement
		built   time.Time // zero: about now
	}{
		{
			name:  "one policy is the root",
			req:   request{src: project.Sources{Paths: []string{single}, Kinds: []string{gate}}},
			epoch: "1700000000",
			root:  "compile.single",
			kinds: []string{"gate.sigil"},
			paths: []string{"single/main.sigil"},
			built: time.Unix(1700000000, 0).UTC(),
		},
		{
			name:  "absolute names are made relative to the configuration file.s directory",
			req:   request{src: project.Sources{Paths: []string{abs(gate), abs(single)}, Kinds: []string{abs(gate)}}},
			root:  "compile.single",
			kinds: []string{"gate.sigil"},
			paths: []string{"gate.sigil", "single/main.sigil"},
		},
		{
			name:  "absolute names outside it and the working directory stay as they are",
			req:   request{src: project.Sources{Paths: []string{gate, outside}}},
			root:  "compile.outside",
			paths: []string{"gate.sigil", filepath.ToSlash(filepath.Join(outside, "outside.sigil"))},
		},
		{
			name:    "the configuration's requirement, with no root",
			req:     request{src: project.Sources{Paths: []string{gate, several}}, config: configFile("require.yaml")},
			epoch:   "0",
			paths:   []string{"gate.sigil", "several/a.sigil", "several/b.sigil"},
			trusted: []string{"trusted/guard.sigil"},
			require: []payload.Requirement{{Policy: "compile.guard", Trusted: []string{"trusted"}}},
			built:   time.Unix(0, 0).UTC(),
		},
		{
			name:    "--require, with --policy: the root's scope only",
			req:     request{src: project.Sources{Paths: []string{gate, several}, Trusted: []string{abs(trusted)}}, requires: []string{"compile.guard"}, policy: "compile.a"},
			root:    "compile.a",
			paths:   []string{"gate.sigil", "several/a.sigil"},
			trusted: []string{"trusted/guard.sigil"},
			require: []payload.Requirement{{Policy: "compile.guard", Trusted: []string{"trusted"}, Roots: []string{"compile.a"}}},
		},
		{
			name:    "--policy leaves out the kinds and trusted files the root doesn't use",
			req:     request{src: project.Sources{Paths: []string{mixed}}, config: configFile("mixed.yaml"), policy: "compile.mixed_gate"},
			root:    "compile.mixed_gate",
			kinds:   []string{"gate.sigil"},
			paths:   []string{"mixed/gate/main.sigil"},
			trusted: []string{"trusted/guard.sigil"},
			require: []payload.Requirement{{Policy: "compile.guard", Trusted: []string{"trusted"}}},
		},
		{
			name:  "--policy leaves out a document of a kind nobody provides",
			req:   request{src: project.Sources{Paths: []string{gate, single, "-"}, Stdin: strings.NewReader("policy compile.orphan: Nope@1\n")}, policy: "compile.single"},
			root:  "compile.single",
			paths: []string{"gate.sigil", "single/main.sigil"},
		},
		{
			name:  "--policy keeps a file of the paths that holds the kind",
			req:   request{src: project.Sources{Paths: []string{gate, single, warn}}, policy: "compile.single"},
			root:  "compile.single",
			paths: []string{"gate.sigil", "single/main.sigil"},
		},
		{
			name:  "stdin",
			req:   request{src: project.Sources{Paths: []string{gate, "-"}, Stdin: strings.NewReader("policy compile.stdin: Gate@1\n")}},
			root:  "compile.stdin",
			paths: []string{"gate.sigil", "<stdin>"},
		},
	}
	exe := fakeBinary(t, area())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", tt.epoch)
			tt.req.out = filepath.Join(t.TempDir(), "policies")
			if tt.req.config == "" {
				tt.req.config = configFile("")
			}
			before := time.Now().UTC().Truncate(time.Second)
			if err := run(&bytes.Buffer{}, testOptions(output.Text, exe), tt.req); err != nil {
				t.Fatalf("run() = %v", err)
			}
			p := decode(t, tt.req.out)
			b := p.Bundle
			if b.Root != tt.root {
				t.Errorf("root = %q, want %q", b.Root, tt.root)
			}
			for _, c := range []struct {
				what      string
				got, want []string
			}{
				{"kinds", names(b.Kinds), tt.kinds},
				{"paths", names(b.Paths), tt.paths},
				{"trusted", names(b.Trusted), tt.trusted},
			} {
				if strings.Join(c.got, " ") != strings.Join(c.want, " ") {
					t.Errorf("%s = %q, want %q", c.what, c.got, c.want)
				}
			}
			if got, want := renderRequirements(b.Require), renderRequirements(tt.require); got != want {
				t.Errorf("require = %s, want %s", got, want)
			}
			switch {
			case !tt.built.IsZero() && !p.Built.Equal(tt.built):
				t.Errorf("built = %v, want %v", p.Built, tt.built)
			case tt.built.IsZero() && (p.Built.Before(before) || p.Built.After(time.Now()) || p.Built.Location() != time.UTC):
				t.Errorf("built = %v, want now in UTC", p.Built)
			}
			if p.Sigil != "v9.9.9" {
				t.Errorf("sigil = %q, want the version WithVersion set", p.Sigil)
			}
		})
	}
}

// TestFilesCountOnce checks that a kind file named with --kind and among
// the paths is counted once, and has one name in both lists.
func TestFilesCountOnce(t *testing.T) {
	exe := fakeBinary(t, area())
	out := filepath.Join(t.TempDir(), "policies")
	var stdout bytes.Buffer
	err := run(&stdout, testOptions(output.JSON, exe), request{src: project.Sources{Paths: []string{gate, single}, Kinds: []string{gate}}, out: out, config: configFile("")})
	if err != nil {
		t.Fatalf("run() = %v", err)
	}
	if !strings.Contains(stdout.String(), `"files": 2`) {
		t.Errorf("output = %s, want the kind file counted once", stdout.String())
	}
	b := decode(t, out).Bundle
	if len(b.Kinds) != 1 || len(b.Paths) != 2 || b.Kinds[0].Name != b.Paths[0].Name {
		t.Errorf("kinds = %q, paths = %q, want the kind file under one name in both", names(b.Kinds), names(b.Paths))
	}
}

// TestLinkedHostFunctions checks that a kind linked into the binary may
// declare host functions, since the binary compile copies implements
// them, and that a kind file for it must match it, as for check.
func TestLinkedHostFunctions(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(hostfn, "lookup.sigil"))
	if err != nil {
		t.Fatal(err)
	}
	k, errs := check.LoadKind("lookup.sigil", src)
	if errs != nil {
		t.Fatalf("LoadKind: %v", errs)
	}
	stale := filepath.Join(t.TempDir(), "lookup.sigil")
	writeFile(t, stale, strings.Replace(string(src), "version 1", "version 2", 1))
	tests := []struct {
		name  string
		kinds []string
		want  string // the output's start, or the error's
		err   bool
	}{
		{name: "linked", want: "✓ compiled compile.lookup from 1 file into "},
		{name: "a stale kind file", kinds: []string{stale}, want: stale + " doesn't match the kind Lookup linked into this binary", err: true},
	}
	exe := fakeBinary(t, area())
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := testOptions(output.Text, exe)
			o.kinds = []project.Linked{{Model: k, Binding: gokind.Synthesize(k)}}
			out := filepath.Join(t.TempDir(), "lookup")
			var stdout bytes.Buffer
			err := run(&stdout, o, request{src: project.Sources{Paths: []string{filepath.Join(hostfn, "main.sigil")}, Kinds: tt.kinds}, out: out, config: configFile("")})
			got := stdout.String()
			if err != nil {
				got = err.Error()
			}
			if (err != nil) != tt.err || !strings.HasPrefix(got, tt.want) {
				t.Errorf("run() = %v with output %q, want %q", err, stdout.String(), tt.want)
			}
		})
	}
}

// TestWriteErrors checks what compile says when it can't print its
// report, or can't put the binary in place.
func TestWriteErrors(t *testing.T) {
	exe := fakeBinary(t, area())
	for _, format := range []output.Format{output.Text, output.JSON} {
		err := run(failingWriter{}, testOptions(format, exe), request{src: project.Sources{Paths: []string{gate, warn}}, out: filepath.Join(t.TempDir(), "policies"), config: configFile("")})
		if err == nil || !strings.Contains(err.Error(), "couldn't be written") {
			t.Errorf("run() with %s to a closed pipe = %v, want the output's error", format, err)
		}
	}
	err := run(failingWriter{}, testOptions(output.YAML, exe), request{src: project.Sources{Paths: []string{gate, riders("mistyped")}}, out: filepath.Join(t.TempDir(), "policies"), config: configFile("require.yaml"), policy: "compile.mistyped_a"})
	if err == nil || !strings.Contains(err.Error(), "couldn't be written") {
		t.Errorf("run() with diagnostics as YAML to a closed pipe = %v, want the output's error", err)
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "keep"), "a directory that isn't empty")
	err = write(dir, []byte("binary"))
	if err == nil || !strings.Contains(strings.Join(err.Advice(), " "), "isn't a directory") {
		t.Errorf("write() over a directory = %v, want the rename's error", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(dir), "."+filepath.Base(dir)+".*")); leftovers != nil {
		t.Errorf("temporary files left behind: %v", leftovers)
	}
}

// TestOut checks the --out rules: it's required, it can't be a
// directory, its directory must exist, and a file that's there is
// replaced by an executable.
func TestOut(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "existing")
	writeFile(t, existing, "an older build")
	inputs := t.TempDir()
	policy := filepath.Join(inputs, "main.sigil")
	writeFile(t, policy, "policy compile.entry: Gate@1\n")
	cfg := filepath.Join(inputs, "sigil.yaml")
	absGate, err := filepath.Abs(gate)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, cfg, "lints: {}\n")
	exe := fakeBinary(t, area())
	tests := []struct {
		name string
		out  string
		want string // the error; empty for none
	}{
		{name: "missing", out: "", want: "--out is required: compile writes a binary, and never to stdout"},
		{name: "stdout", out: "-", want: "--out is required: compile writes a binary, and never to stdout"},
		{name: "a directory", out: dir, want: "--out " + dir + " is a directory"},
		{name: "in a missing directory", out: filepath.Join(dir, "nope", "policies"), want: filepath.Join(dir, "nope", "policies") + " couldn't be written"},
		{name: "the running binary", out: exe, want: "--out " + exe + " is the sigil binary compile runs as"},
		{name: "a policy file it reads", out: policy, want: "--out " + policy + " is one of the files compile reads"},
		{name: "the kind file it reads", out: gate, want: "--out " + gate + " is one of the files compile reads"},
		{name: "the configuration file", out: cfg, want: "--out " + cfg + " is one of the files compile reads"},
		{name: "a file it reads, named another way", out: absGate, want: "--out " + absGate + " is testdata/gate.sigil, one of the files compile reads"},
		{name: "a new file", out: filepath.Join(dir, "new")},
		{name: "an existing file", out: existing},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before, _ := os.ReadFile(tt.out)
			err := run(&bytes.Buffer{}, testOptions(output.Text, exe), request{src: project.Sources{Paths: []string{gate, policy}}, out: tt.out, config: cfg})
			if tt.want != "" {
				if err == nil || !strings.HasPrefix(err.Error(), tt.want) {
					t.Fatalf("run() = %v, want %q", err, tt.want)
				}
				if after, _ := os.ReadFile(tt.out); !bytes.Equal(before, after) {
					t.Errorf("%s changed", tt.out)
				}
				if len(err.Advice()) == 0 {
					t.Errorf("run() = %v without advice", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("run() = %v", err)
			}
			info, serr := os.Stat(tt.out)
			if serr != nil || info.Mode().Perm() != 0o755 {
				t.Fatalf("stat %s = %v, %v, want an executable", tt.out, info, serr)
			}
			if p := decode(t, tt.out); p.Bundle.Root != "compile.entry" {
				t.Errorf("root = %q, want compile.entry", p.Bundle.Root)
			}
			if leftovers, _ := filepath.Glob(filepath.Join(dir, ".*")); leftovers != nil {
				t.Errorf("temporary files left behind: %v", leftovers)
			}
		})
	}
}

// TestRiders checks that a document beside the root, in a file of its
// scope, goes into the binary checked: one that passes is compiled in
// and compiles from the binary's bundle, as the binary loads it. The
// ones that don't are refused in the goldens of TestCompile.
func TestRiders(t *testing.T) {
	exe := fakeBinary(t, area())
	out := filepath.Join(t.TempDir(), "policies")
	req := request{src: project.Sources{Paths: []string{gate, riders("guarded")}}, out: out, config: configFile("require.yaml"), policy: "compile.guarded_a"}
	if err := run(&bytes.Buffer{}, testOptions(output.Text, exe), req); err != nil {
		t.Fatalf("run() = %v", err)
	}
	b := decode(t, out).Bundle
	p, err := project.LoadFiles(project.FromBundle(&b), nil)
	if err != nil {
		t.Fatalf("LoadFiles() = %v", err)
	}
	p.Check()
	if errs := p.Errors(); errs != nil {
		t.Fatalf("the binary's bundle doesn't check: %v", errs)
	}
	for _, name := range []string{"compile.guarded_a", "compile.guarded_b"} {
		if _, errs := p.Group(name).Bundle.Compile(name, bundle.Options{Static: true, Require: []string{"compile.guard"}}); errs != nil {
			t.Errorf("Compile(%s) = %v", name, errs)
		}
	}
}

// TestSameDigestFromAnyDirectory checks that a repository compiles to the
// same digest from its root and from a directory in it: the names are
// relative to the configuration file's directory.
func TestSameDigestFromAnyDirectory(t *testing.T) {
	repo := t.TempDir()
	kind, err := os.ReadFile(gate)
	if err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"sigil.yaml":             "kinds: [kinds/gate.sigil]\nrequire:\n  - policy: compile.repo_guard\n    trusted: [platform]\n",
		"kinds/gate.sigil":       string(kind),
		"platform/guard.sigil":   "policy compile.repo_guard: Gate@1\n\nwhen user == \"\" {\n  deny(reason: nobody)\n}\n",
		"teams/payments.sigil":   "policy compile.repo_team: Gate@1\n\nuse compile.repo_guard\n\nrepo_guard()\n",
		"teams/payments/x.sigil": "policy compile.repo_x: Gate@1\n\nuse compile.repo_guard\n\nrepo_guard()\n",
	} {
		if err := os.MkdirAll(filepath.Join(repo, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(repo, name), src)
	}
	exe := fakeBinary(t, area())
	runs := []struct {
		dir   string
		paths []string
	}{
		{dir: repo, paths: []string{"teams", "platform"}},
		{dir: filepath.Join(repo, "teams"), paths: []string{".", "../platform"}},
		{dir: filepath.Join(repo, "teams", "payments"), paths: []string{"..", filepath.Join(repo, "platform")}},
	}
	digests := make([]string, 0, len(runs))
	for _, r := range runs {
		t.Chdir(r.dir)
		out := filepath.Join(t.TempDir(), "policies")
		if err := run(&bytes.Buffer{}, testOptions(output.Text, exe), request{src: project.Sources{Paths: r.paths}, out: out}); err != nil {
			t.Fatalf("run() in %s = %v", r.dir, err)
		}
		b := decode(t, out).Bundle
		digests = append(digests, b.Digest())
		if got := strings.Join(slices.Concat(names(b.Kinds), names(b.Paths), names(b.Trusted)), " "); got != "kinds/gate.sigil teams/payments.sigil teams/payments/x.sigil platform/guard.sigil" {
			t.Errorf("files compiled in %s = %s", r.dir, got)
		}
	}
	if digests[0] != digests[1] || digests[0] != digests[2] {
		t.Errorf("digests = %v, want one digest", digests)
	}
}

// TestBinaries checks what compile says about a binary it can't write
// into, and about a bundle that doesn't fit.
func TestBinaries(t *testing.T) {
	marker := payload.Marker()
	missing := filepath.Join(t.TempDir(), "missing")
	big := filepath.Join(t.TempDir(), "big.sigil")
	writeFile(t, big, "policy compile.big: Gate@1\n\n"+noise(2<<20))
	tests := []struct {
		name       string
		executable func() (string, error)
		paths      []string // gate and single when nil
		want       string
		advice     string
		is         error
	}{
		{
			name:       "the executable can't be found",
			executable: func() (string, error) { return "", os.ErrNotExist },
			want:       "the sigil binary to copy can't be found",
		},
		{
			name:       "the executable can't be read",
			executable: func() (string, error) { return missing, nil },
			want:       "the sigil binary " + missing + " couldn't be read",
		},
		{
			name:       "no area",
			executable: fixed(fakeBinary(t, nil)),
			want:       "has no room for a bundle",
			is:         stamp.ErrNoArea,
		},
		{
			name:       "two areas",
			executable: fixed(fakeBinary(t, append(area(), marker...))),
			want:       "holds more than one reserved area",
			is:         stamp.ErrManyAreas,
		},
		{
			name:       "a universal binary",
			executable: fixed(binary(t, append([]byte{0xca, 0xfe, 0xba, 0xbe}, area()...))),
			want:       "compile can't write into",
			advice:     "a universal macOS binary holds several",
			is:         stamp.ErrUnsupported,
		},
		{
			name:       "an area cut short",
			executable: fixed(fakeBinary(t, area()[:1024])),
			want:       "is damaged",
			is:         stamp.ErrMalformed,
		},
		{
			name:       "a bundle too large",
			executable: fixed(fakeBinary(t, area())),
			paths:      []string{gate, big},
			want:       "the bundle takes ",
			advice:     "compile fewer files",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := tt.paths
			if paths == nil {
				paths = []string{gate, single}
			}
			o := testOptions(output.Text, "")
			o.executable = tt.executable
			out := filepath.Join(t.TempDir(), "policies")
			err := run(&bytes.Buffer{}, o, request{src: project.Sources{Paths: paths}, out: out, config: configFile("")})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("run() = %v, want %q", err, tt.want)
			}
			if tt.advice != "" && !strings.Contains(strings.Join(err.Advice(), "\n"), tt.advice) {
				t.Errorf("advice = %q, want %q", err.Advice(), tt.advice)
			}
			if tt.is != nil && !errors.Is(err, tt.is) {
				t.Errorf("run() = %v, want it to wrap %v", err, tt.is)
			}
			if _, serr := os.Stat(out); serr == nil {
				t.Errorf("%s was written", out)
			}
		})
	}
}

// TestPatchError checks the message and advice of every error stamp
// returns, including the ones a stand-in binary can't produce.
func TestPatchError(t *testing.T) {
	tests := []struct {
		err    error
		want   string
		advice string
	}{
		{err: &stamp.TooLargeError{Size: 1100, Max: 1000}, want: "the bundle takes 1100 bytes compressed, 100 more than a binary has room for (1000 bytes)", advice: "compile fewer files"},
		{err: stamp.ErrSigned, want: "sigil is signed with an identity", advice: "sign the compiled binary afterwards"},
		{err: stamp.ErrNoArea, want: "sigil has no room for a bundle", advice: "pkg/cli"},
		{err: stamp.ErrManyAreas, want: "sigil holds more than one reserved area", advice: "a bug in sigil"},
		{err: stamp.ErrUnsupported, want: "compile can't write into sigil", advice: "one platform"},
		{err: stamp.ErrMalformed, want: "sigil is damaged", advice: "install sigil again"},
		{err: stamp.ErrMarkerInData, want: "the encoded bundle happens to hold the marker of the reserved area", advice: "report it as a bug"},
		{err: errors.New("stamp: something new"), want: "the bundle couldn't be written into a copy of sigil", advice: "a bug in sigil"},
	}
	for _, tt := range tests {
		err := patchError("sigil", tt.err)
		if err.Error() != tt.want && !strings.HasPrefix(err.Error(), tt.want) {
			t.Errorf("patchError(%v) = %q, want %q", tt.err, err.Error(), tt.want)
		}
		if !strings.Contains(strings.Join(err.Advice(), "\n"), tt.advice) {
			t.Errorf("patchError(%v) advice = %q, want %q", tt.err, err.Advice(), tt.advice)
		}
		if !errors.Is(err, tt.err) {
			t.Errorf("patchError(%v) doesn't wrap it", tt.err)
		}
	}
}

// TestBuildTime checks SOURCE_DATE_EPOCH: a number of seconds is the
// build time in UTC, anything else an error, and without it, the build
// time is now, to the second.
func TestBuildTime(t *testing.T) {
	tests := []struct {
		epoch string
		want  time.Time // zero: now
		err   bool
	}{
		{epoch: ""},
		{epoch: "0", want: time.Unix(0, 0).UTC()},
		{epoch: "1700000000", want: time.Unix(1700000000, 0).UTC()},
		{epoch: "-1", err: true},
		{epoch: "1.5", err: true},
		{epoch: "tomorrow", err: true},
		{epoch: "99999999999999999999", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.epoch, func(t *testing.T) {
			t.Setenv("SOURCE_DATE_EPOCH", tt.epoch)
			before := time.Now().UTC().Truncate(time.Second)
			got, err := buildTime()
			switch {
			case tt.err:
				if err == nil || !strings.Contains(err.Error(), "SOURCE_DATE_EPOCH is \""+tt.epoch+"\"") {
					t.Errorf("buildTime() = %v, %v, want an error naming the value", got, err)
				}
			case err != nil:
				t.Errorf("buildTime() = %v", err)
			case tt.want.IsZero():
				if got.Before(before) || got.After(time.Now()) || got.Location() != time.UTC || got.Nanosecond() != 0 {
					t.Errorf("buildTime() = %v, want now in UTC, to the second", got)
				}
			case !got.Equal(tt.want) || got.Location() != time.UTC:
				t.Errorf("buildTime() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestNamer checks how compile names a file in the bundle: relative to
// the configuration file's directory, or to the working directory.
func TestNamer(t *testing.T) {
	root := filepath.FromSlash("/build")
	if v := filepath.VolumeName(os.TempDir()); v != "" {
		root = v + root
	}
	repo := filepath.Join(root, "repo")
	sub := filepath.Join(repo, "policies")
	abs := func(parts ...string) string {
		return filepath.ToSlash(filepath.Join(append([]string{repo}, parts...)...))
	}
	tests := []struct {
		name     string
		wd, base string
		in, want string
	}{
		{name: "absolute, below", wd: repo, base: repo, in: abs("deploy", "a.sigil"), want: "deploy/a.sigil"},
		{name: "absolute, in it", wd: repo, base: repo, in: abs("a.sigil"), want: "a.sigil"},
		{name: "relative, below", wd: repo, base: repo, in: "deploy/a.sigil", want: "deploy/a.sigil"},
		{name: "relative, above", wd: repo, base: repo, in: "../other/a.sigil", want: "../other/a.sigil"},
		{name: "absolute, outside", wd: repo, base: repo, in: abs("..", "repo2", "a.sigil"), want: abs("..", "repo2", "a.sigil")},
		{name: "a name that starts with dots", wd: repo, base: repo, in: "..foo/a.sigil", want: "..foo/a.sigil"},
		{name: "stdin", wd: sub, base: repo, in: "<stdin>", want: "<stdin>"},
		{name: "from a subdirectory", wd: sub, base: repo, in: "a.sigil", want: "policies/a.sigil"},
		{name: "from a subdirectory, above it", wd: sub, base: repo, in: "../kinds/k.sigil", want: "kinds/k.sigil"},
		{name: "the configuration below the working directory", wd: repo, base: sub, in: "kinds/k.sigil", want: "../kinds/k.sigil"},
		{name: "no working directory", in: abs("a.sigil"), want: abs("a.sigil")},
	}
	for _, tt := range tests {
		if got := (namer{wd: tt.wd, base: tt.base}).name(tt.in); got != tt.want {
			t.Errorf("%s: name(%q) = %q, want %q", tt.name, tt.in, got, tt.want)
		}
	}
}

// TestCommand runs the command as the root runs it, through its flags.
func TestCommand(t *testing.T) {
	exe := fakeBinary(t, area())
	out := filepath.Join(t.TempDir(), "policies")
	format := output.JSON
	cmd := NewCommand(WithOutput(&format), WithOutput(nil), WithKinds(nil), WithVersion("v1.0.0"), WithExecutable(fixed(exe)), WithExecutable(nil))
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{"--out", out, "--policy", "compile.a", "--kind", gate, "--config", configFile(""), "--require", "compile.guard", "--trusted", trusted, several})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() = %v", err)
	}
	if want := `"root": "compile.a"`; !strings.Contains(stdout.String(), want) {
		t.Errorf("output = %s, want %s", stdout.String(), want)
	}
	p := decode(t, out)
	if p.Sigil != "v1.0.0" || len(p.Bundle.Require) != 1 || len(p.Bundle.Kinds) != 1 {
		t.Errorf("payload = %+v, want the flags' kind and requirement, compiled by v1.0.0", p)
	}
}

// TestDefaults checks the options a bare NewCommand starts from: text,
// and the running binary.
func TestDefaults(t *testing.T) {
	o := defaultOptions()
	if *o.output != output.Text || o.executable == nil {
		t.Fatalf("defaultOptions() = %+v, want text and os.Executable", o)
	}
	exe, err := o.executable()
	if err != nil || exe == "" {
		t.Errorf("executable() = %q, %v, want the test binary", exe, err)
	}
	if v := sigilVersion(""); v == "" {
		t.Errorf("sigilVersion(\"\") is empty, want the build info's version")
	}
}

// testOptions returns options for a test run: the format, a stand-in
// binary and a fixed release version.
func testOptions(format output.Format, exe string) *options {
	o := defaultOptions()
	o.output = &format
	o.version = "v9.9.9"
	o.executable = fixed(exe)
	return o
}

// configFile returns a configuration file under testdata, defaults.yaml
// when name is empty.
func configFile(name string) string {
	if name == "" {
		name = "defaults.yaml"
	}
	return filepath.Join("testdata", name)
}

// area returns a reserved area, the marker and zeros, as a binary that
// links package payload holds it.
func area() []byte {
	return append(payload.Marker(), make([]byte, payload.AreaSize-len(payload.Marker()))...)
}

// fakeBinary writes a stand-in for a sigil binary: an ELF magic number, a
// few bytes of code, the area given, and more bytes after it. Patch
// doesn't parse ELF headers, so it writes into one as into a real ELF.
func fakeBinary(t *testing.T, area []byte) string {
	t.Helper()
	bin := append([]byte("\x7fELF code"), area...)
	return binary(t, append(bin, "more code"...))
}

// binary writes bin to a file in a temporary directory and returns its
// path.
func binary(t *testing.T, bin []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sigil")
	if err := os.WriteFile(path, bin, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fixed returns an executable func that finds path.
func fixed(path string) func() (string, error) {
	return func() (string, error) { return path, nil }
}

// decode returns the payload in the area of the binary at path.
func decode(t *testing.T, path string) *payload.Payload {
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

// noise returns n bytes of comment lines that don't compress much.
func noise(n int) string {
	r := rand.New(rand.NewPCG(1, 2))
	raw := make([]byte, n*3/4)
	for i := range raw {
		raw[i] = byte(r.Uint32())
	}
	text := base64.StdEncoding.EncodeToString(raw)
	var b strings.Builder
	for len(text) > 0 {
		line := text[:min(100, len(text))]
		text = text[len(line):]
		b.WriteString("// " + line + "\n")
	}
	return b.String()
}

func names(files []payload.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Name)
	}
	return out
}

// renderRequirements renders requirements for comparison.
func renderRequirements(reqs []payload.Requirement) string {
	parts := make([]string, 0, len(reqs))
	for _, r := range reqs {
		parts = append(parts, r.Policy+" trusted "+strings.Join(r.Trusted, ",")+" roots "+strings.Join(r.Roots, ","))
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

func writeFile(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// render joins what the command printed and the error it returned.
func render(out string, err humane.Error) string {
	if err == nil {
		return out + "--- ok ---\n"
	}
	s := out + "--- error ---\n" + err.Error() + "\n"
	for _, a := range err.Advice() {
		s += "advice: " + a + "\n"
	}
	return s
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

// riders returns a file under testdata/riders, which holds a policy and,
// beside it, another one.
func riders(name string) string { return filepath.Join("testdata", "riders", name+".sigil") }
