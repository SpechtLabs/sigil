package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/internal/lint"
)

func TestParse(t *testing.T) {
	// The file sits in repo/policies, so relative paths resolve there.
	file := filepath.Join("repo", "policies", config.FileName)
	dir := filepath.Dir(file)
	abs := filepath.Join(string(filepath.Separator), "opt", "kinds", "deploy.sigil")
	tests := []struct {
		name   string
		src    string
		want   *config.Config // Lints nil means none; File is always file
		err    string
		advice string
	}{
		{
			name: "levels",
			src:  "lints:\n  gated-deny: error\n  qualified-imports: warn\n  unused-let: off\n",
			want: &config.Config{Lints: map[string]lint.Level{lint.GatedDeny: lint.Error, lint.QualifiedImports: lint.Warn, lint.UnusedLet: lint.Off}},
		},
		{name: "empty file", src: "", want: &config.Config{}},
		{name: "only a comment", src: "# nothing configured yet\n", want: &config.Config{}},
		{name: "an empty map", src: "{}\n", want: &config.Config{}},
		{name: "a null document", src: "~\n", want: &config.Config{}},
		{name: "empty sections", src: "kinds:\nrequire:\nlints:\n", want: &config.Config{}},
		{
			name: "everything",
			src: "kinds:\n  - ../vendor/deploy_approval.sigil\n  - " + filepath.ToSlash(abs) + "\n" +
				"require:\n" +
				"  - policy: deploy.guardrails\n    trusted: [platform/deploy]\n    roots: [\"payments.*\", \"checkout.*\"]\n" +
				"  - policy: access.guardrails\n" +
				"lints:\n  gated-deny: error\n",
			want: &config.Config{
				Kinds: []string{filepath.Join("repo", "vendor", "deploy_approval.sigil"), abs},
				Require: []config.Require{
					{Policy: "deploy.guardrails", Trusted: []string{filepath.Join(dir, "platform", "deploy")}, Roots: []string{"payments.*", "checkout.*"}, Pos: config.Pos{Line: 5, Column: 13}},
					{Policy: "access.guardrails", Pos: config.Pos{Line: 8, Column: 13}},
				},
				Lints: map[string]lint.Level{lint.GatedDeny: lint.Error},
			},
		},
		{
			name: "a single path or root needs no list",
			src:  "kinds: k.sigil\nrequire:\n  - policy: deploy.guardrails\n    trusted: platform\n    roots: payments.*\n",
			want: &config.Config{
				Kinds:   []string{filepath.Join(dir, "k.sigil")},
				Require: []config.Require{{Policy: "deploy.guardrails", Trusted: []string{filepath.Join(dir, "platform")}, Roots: []string{"payments.*"}, Pos: config.Pos{Line: 3, Column: 13}}},
			},
		},
		{
			name: "an alias for a list",
			src:  "kinds: &k [k.sigil]\nrequire:\n  - policy: deploy.guardrails\n    trusted: *k\n",
			want: &config.Config{
				Kinds:   []string{filepath.Join(dir, "k.sigil")},
				Require: []config.Require{{Policy: "deploy.guardrails", Trusted: []string{filepath.Join(dir, "k.sigil")}, Pos: config.Pos{Line: 3, Column: 13}}},
			},
		},
		{name: "unknown lint", src: "lints:\n  gated-denies: error\n", err: `sigil.yaml:2:3: unknown lint "gated-denies"`, advice: `did you mean "gated-deny"?`},
		{name: "unknown lint without a near name", src: "lints:\n  frobnicate: warn\n", err: `unknown lint "frobnicate"`, advice: "lints: unused-import, shadowed-kind-name"},
		{name: "bad level", src: "lints:\n  gated-deny: fatal\n", err: `sigil.yaml:2:15: lint gated-deny has unknown level "fatal"`, advice: "off, warn or error"},
		{name: "a level that isn't a string", src: "lints:\n  gated-deny: [error]\n", err: `lint gated-deny has unknown level ""`, advice: "off, warn or error"},
		{name: "a lint set twice", src: "lints:\n  gated-deny: error\n  gated-deny: warn\n", err: "lint gated-deny is set twice"},
		{name: "lints isn't a map", src: "lints: [gated-deny]\n", err: "sigil.yaml:1:8: lints isn't a map", advice: "`gated-deny: error`"},
		{name: "unknown top-level key", src: "lint:\n  gated-deny: error\n", err: `sigil.yaml:1:1: unknown key "lint"`, advice: "did you mean \"lints\"?; sigil.yaml holds `kinds:`, `require:` and `lints:`"},
		{name: "unknown top-level key without a near name", src: "owners: [ada]\n", err: `unknown key "owners"`, advice: "sigil.yaml holds `kinds:`"},
		{name: "a key set twice", src: "kinds: [a.sigil]\nkinds: [b.sigil]\n", err: "sigil.yaml:2:1: kinds is set twice", advice: "first set at line 1"},
		{name: "a key that isn't a name", src: "[kinds]: a.sigil\n", err: "sigil.yaml:1:1: a key isn't a name"},
		{name: "not a map", src: "- kinds\n", err: "sigil.yaml:1:1: the configuration isn't a map"},
		{name: "not YAML", src: "kinds: [a\n", err: "isn't valid YAML"},
		{name: "several documents", src: "lints: {}\n---\nkinds: []\n", err: "holds more than one YAML document"},
		{name: "a kind that isn't a string", src: "kinds:\n  - {file: a.sigil}\n", err: "sigil.yaml:2:5: kinds[0] isn't a string", advice: "a kind file, relative to sigil.yaml"},
		{name: "an empty kind", src: "kinds:\n  - \"\"\n", err: "kinds[0] is empty"},
		{name: "a null kind", src: "kinds:\n  - ~\n", err: "kinds[0] is empty"},
		{name: "require isn't a list", src: "require: deploy.guardrails\n", err: "sigil.yaml:1:10: require isn't a list", advice: "`- policy: deploy.guardrails`"},
		{name: "a require entry that isn't a map", src: "require:\n  - deploy.guardrails\n", err: "sigil.yaml:2:5: require[0] isn't a map"},
		{name: "a require entry without a policy", src: "require:\n  - trusted: [platform]\n", err: "sigil.yaml:2:5: require[0] names no policy", advice: "add `policy:`"},
		{name: "an empty policy", src: "require:\n  - policy:\n", err: "require[0].policy is empty"},
		{name: "a policy that isn't a name", src: "require:\n  - policy: [a, b]\n", err: "sigil.yaml:2:13: require[0].policy isn't a name"},
		{name: "a policy pattern", src: "require:\n  - policy: deploy.*\n", err: `sigil.yaml:2:13: require[0].policy "deploy.*" is a pattern`, advice: "roots: takes the patterns"},
		{name: "an unknown require key", src: "require:\n  - policy: deploy.guardrails\n    root: [payments.*]\n", err: `sigil.yaml:3:5: unknown key "root" in require[0]`, advice: "did you mean \"roots\"?; a require entry holds `policy:`, `trusted:` and `roots:`"},
		{name: "a require key set twice", src: "require:\n  - policy: a\n    policy: b\n", err: "policy is set twice in require[0]"},
		{name: "a policy required twice", src: "require:\n  - policy: deploy.guardrails\n  - policy: deploy.guardrails\n", err: "sigil.yaml:3:13: deploy.guardrails is required twice", advice: "first required at line 2"},
		{name: "an empty root", src: "require:\n  - policy: a\n    roots: [\"\"]\n", err: "require[0].roots[0] is empty", advice: "a policy name or pattern"},
		{name: "a trusted path that isn't a string", src: "require:\n  - policy: a\n    trusted: [[platform]]\n", err: "require[0].trusted[0] isn't a string", advice: "a file or directory, relative to sigil.yaml"},
		{name: "a require entry's error comes after its own policy", src: "require:\n  - policy: a\n    trusted: [\"\"]\n", err: "sigil.yaml:3:15: require[0].trusted[0] is empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.Parse(file, []byte(tt.src))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Parse() error = %v, want %q", err, tt.err)
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.advice) {
					t.Errorf("advice = %q, want %q", advice, tt.advice)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			want := *tt.want
			want.File = file
			if want.Lints == nil {
				want.Lints = map[string]lint.Level{}
			}
			if !reflect.DeepEqual(c, &want) {
				t.Errorf("Parse() = %+v, want %+v", c, &want)
			}
		})
	}
}

// TestRequirements checks that --require replaces the file's
// requirements for the run, and --trusted or --policy alone doesn't.
func TestRequirements(t *testing.T) {
	c := &config.Config{File: config.FileName, Require: []config.Require{{Policy: "deploy.guardrails", Pos: config.Pos{Line: 2, Column: 13}}}}
	tests := []struct {
		name                     string
		policies, trusted, roots []string
		want                     []config.Require
	}{
		{name: "no flags keeps the file's", want: c.Require},
		{
			name:     "every flag",
			policies: []string{"a", "b"}, trusted: []string{"platform"}, roots: []string{"payments.*"},
			want: []config.Require{
				{Policy: "a", Trusted: []string{"platform"}, Roots: []string{"payments.*"}},
				{Policy: "b", Trusted: []string{"platform"}, Roots: []string{"payments.*"}},
			},
		},
		{name: "--require alone", policies: []string{"a"}, want: []config.Require{{Policy: "a"}}},
		{name: "--trusted alone keeps the file's", trusted: []string{"platform"}, want: c.Require},
		{name: "--policy alone keeps the file's", roots: []string{"payments.*"}, want: c.Require},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Requirements(tt.policies, tt.trusted, tt.roots); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Requirements() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestAt positions a requirement in the file, and one from the command
// line, which has no position, at the file alone.
func TestAt(t *testing.T) {
	c := &config.Config{File: "policies/sigil.yaml"}
	if got := c.At(config.Pos{Line: 5, Column: 13}); got != "policies/sigil.yaml:5:13" {
		t.Errorf("At() = %q", got)
	}
	if got := c.At(config.Pos{}); got != "policies/sigil.yaml" {
		t.Errorf("At(zero) = %q", got)
	}
}

// TestLoad finds the nearest sigil.yaml at or above a directory.
func TestLoad(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	nested := filepath.Join(repo, "teams", "payments")
	inner := filepath.Join(repo, "teams", "platform")
	for _, d := range []string{nested, inner} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write(t, filepath.Join(repo, config.FileName), "lints:\n  gated-deny: error\n")
	write(t, filepath.Join(inner, config.FileName), "lints:\n  unused-let: off\n")
	explicit := filepath.Join(root, "other.yaml")
	write(t, explicit, "lints:\n  path-matches-name: warn\n")
	// A directory named sigil.yaml isn't a configuration.
	if err := os.MkdirAll(filepath.Join(nested, config.FileName), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		dir  string
		file string // where the configuration came from; empty for the defaults
		want map[string]lint.Level
		err  string
	}{
		{name: "in the directory", dir: repo, file: filepath.Join(repo, config.FileName), want: map[string]lint.Level{lint.GatedDeny: lint.Error}},
		{name: "in a parent", dir: nested, file: filepath.Join(repo, config.FileName), want: map[string]lint.Level{lint.GatedDeny: lint.Error}},
		{name: "the nearest wins", dir: inner, file: filepath.Join(inner, config.FileName), want: map[string]lint.Level{lint.UnusedLet: lint.Off}},
		{name: "explicit path", path: explicit, dir: nested, file: explicit, want: map[string]lint.Level{lint.PathMatchesName: lint.Warn}},
		{name: "none found", dir: root},
		{name: "explicit path missing", path: filepath.Join(root, "nope.yaml"), dir: repo, err: "couldn't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := config.Load(tt.path, tt.dir)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Load() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if c.File != tt.file || len(c.Lints) != len(tt.want) || (tt.want != nil && !reflect.DeepEqual(c.Lints, tt.want)) {
				t.Errorf("Load() = %+v, want file %q with %v", c, tt.file, tt.want)
			}
		})
	}
}

// TestLoadResolvesPaths resolves the paths of a sigil.yaml found in a
// parent directory against that directory, not the one the command runs
// in. A file found above the working directory is named relative to it,
// and so are the paths resolved against it; one found elsewhere is
// absolute.
func TestLoadResolvesPaths(t *testing.T) {
	repo := t.TempDir()
	nested := filepath.Join(repo, "teams", "payments")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, config.FileName), "kinds: [kinds/deploy.sigil]\nrequire:\n  - policy: deploy.guardrails\n    trusted: [platform/deploy]\n")
	up := filepath.Join("..", "..")
	tests := []struct {
		name, cwd, dir string
		base           string // the directory the file and the paths are named in
	}{
		{name: "from a subdirectory", cwd: nested, dir: ".", base: up},
		{name: "from the repository root", cwd: repo, dir: ".", base: "."},
		{name: "from elsewhere", cwd: t.TempDir(), dir: nested, base: repo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Chdir(tt.cwd)
			c, err := config.Load("", tt.dir)
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(tt.base, config.FileName); c.File != want {
				t.Errorf("File = %q, want %q", c.File, want)
			}
			if want := []string{filepath.Join(tt.base, "kinds", "deploy.sigil")}; !reflect.DeepEqual(c.Kinds, want) {
				t.Errorf("Kinds = %v, want %v", c.Kinds, want)
			}
			if want := []string{filepath.Join(tt.base, "platform", "deploy")}; len(c.Require) != 1 || !reflect.DeepEqual(c.Require[0].Trusted, want) {
				t.Errorf("Require = %+v, want trusted %v", c.Require, want)
			}
		})
	}
}

func write(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}
