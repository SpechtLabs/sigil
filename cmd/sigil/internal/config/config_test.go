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
	file := filepath.Join("repo", "policies", "sigil.yaml")
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
		{name: "$schema is ignored", src: "# yaml-language-server: $schema=https://sigil.specht-labs.de/schema/config.json\n$schema: https://sigil.specht-labs.de/schema/config.json\n", want: &config.Config{}},
		{name: "a misspelled $schema", src: "schema: x\n", err: `unknown key "schema"`, advice: `did you mean "$schema"?`},
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
		{name: "not YAML", src: "kinds: [a\n", err: "invalid YAML"},
		{name: "not YAML, without a line", src: "a: b: c\n", err: file + " isn't valid YAML", advice: "fix the YAML syntax"},
		{name: "not YAML, with a line", src: "kinds: [a\n", err: file + ":1: invalid YAML: did not find expected"},
		{name: "several documents", src: "lints: {}\n---\nkinds: []\n", err: file + ":2:1: a second YAML document starts here"},
		{name: "a kind that isn't a string", src: "kinds:\n  - {file: a.sigil}\n", err: "sigil.yaml:2:5: kinds[0] isn't a string", advice: "a kind file, relative to sigil.yaml"},
		{name: "an empty kind", src: "kinds:\n  - \"\"\n", err: "kinds[0] is empty"},
		{name: "a null kind", src: "kinds:\n  - ~\n", err: "kinds[0] is empty"},
		{name: "require isn't a list", src: "require: deploy.guardrails\n", err: "sigil.yaml:1:10: require isn't a list", advice: "`- policy: deploy.guardrails`"},
		{name: "a require entry that isn't a map", src: "require:\n  - deploy.guardrails\n", err: "sigil.yaml:2:5: require[0] isn't a map"},
		{name: "a require entry without a policy", src: "require:\n  - trusted: [platform]\n", err: "sigil.yaml:2:5: require[0] names no policy", advice: "add `policy:`"},
		{name: "an empty policy", src: "require:\n  - policy:\n", err: "require[0].policy is empty"},
		{name: "a policy that isn't a name", src: "require:\n  - policy: [a, b]\n", err: "sigil.yaml:2:13: require[0].policy isn't a name"},
		{name: "a policy pattern", src: "require:\n  - policy: deploy.*\n", err: `sigil.yaml:2:13: require[0].policy "deploy.*" is a pattern`, advice: "`roots:` takes the patterns"},
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
	c := &config.Config{File: "sigil.yaml", Require: []config.Require{{Policy: "deploy.guardrails", Pos: config.Pos{Line: 2, Column: 13}}}}
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
	if got := c.Base(); got != "sigil.yaml" {
		t.Errorf("Base() = %q", got)
	}
	if got := (&config.Config{}).Base(); got != "the configuration file" {
		t.Errorf("Base() of the defaults = %q", got)
	}
}

// TestLoad finds the configuration file of the nearest directory at or
// above a directory, under any of its six names, or reads the one --config
// names in the format of its extension.
func TestLoad(t *testing.T) {
	// Each format sets a different lint, so the test sees which file was
	// read.
	sources := map[string]string{
		".yaml": "lints:\n  gated-deny: error\n",
		".yml":  "lints:\n  gated-deny: error\n",
		".json": `{"lints": {"unused-let": "off"}}`,
		".toml": "[lints]\npath-matches-name = \"warn\"\n",
	}
	levels := map[string]map[string]lint.Level{
		".yaml": {lint.GatedDeny: lint.Error},
		".yml":  {lint.GatedDeny: lint.Error},
		".json": {lint.UnusedLet: lint.Off},
		".toml": {lint.PathMatchesName: lint.Warn},
	}
	tests := []struct {
		name  string
		files []string // below the tree's root; each holds its format's source
		dirs  []string // directories below the root, besides the files' own
		path  string   // --config, below the root
		dir   string   // where discovery starts, below the root
		file  string   // the file read, below the root; empty for the defaults
		err   string
	}{
		{name: "sigil.yaml", files: []string{"sigil.yaml"}, file: "sigil.yaml"},
		{name: "sigil.json", files: []string{"sigil.json"}, file: "sigil.json"},
		{name: "sigil.toml", files: []string{"sigil.toml"}, file: "sigil.toml"},
		{name: ".sigil.yaml", files: []string{".sigil.yaml"}, file: ".sigil.yaml"},
		{name: ".sigil.json", files: []string{".sigil.json"}, file: ".sigil.json"},
		{name: ".sigil.toml", files: []string{".sigil.toml"}, file: ".sigil.toml"},
		{name: "in a parent", files: []string{"sigil.toml"}, dirs: []string{"teams/payments"}, dir: "teams/payments", file: "sigil.toml"},
		{name: "the nearest wins", files: []string{"sigil.yaml", "teams/.sigil.json"}, dirs: []string{"teams/payments"}, dir: "teams/payments", file: "teams/.sigil.json"},
		{name: "the nearest wins over two further up", files: []string{"sigil.yaml", "sigil.toml", "teams/sigil.json"}, dir: "teams", file: "teams/sigil.json"},
		{name: "a directory isn't a configuration", files: []string{"sigil.toml"}, dirs: []string{"teams/sigil.yaml"}, dir: "teams", file: "sigil.toml"},
		{name: "none", dirs: []string{"teams"}, dir: "teams"},
		{name: "two in one directory", files: []string{"sigil.yaml", ".sigil.toml"}, err: "sigil.yaml and .sigil.toml in ROOT are both configuration files"},
		{name: "three in one directory", files: []string{"sigil.json", "sigil.toml", ".sigil.yaml"}, err: "sigil.json, sigil.toml and .sigil.yaml in ROOT are all configuration files"},
		{name: "--config yaml", files: []string{"sigil.toml", "conf/other.yaml"}, path: "conf/other.yaml", file: "conf/other.yaml"},
		{name: "--config yml", files: []string{"conf/other.yml"}, path: "conf/other.yml", file: "conf/other.yml"},
		{name: "--config json", files: []string{"conf/other.json"}, path: "conf/other.json", file: "conf/other.json"},
		{name: "--config toml", files: []string{"conf/other.toml"}, path: "conf/other.toml", file: "conf/other.toml"},
		{name: "--config skips discovery", files: []string{"sigil.yaml", "sigil.json", "conf/other.toml"}, path: "conf/other.toml", file: "conf/other.toml"},
		{name: "--config with another extension", files: []string{"sigil.yaml"}, path: "sigil.conf", err: "sigil.conf isn't a .yaml, .yml, .json or .toml file"},
		{name: "--config without an extension", path: "sigil", err: "sigil isn't a .yaml, .yml, .json or .toml file"},
		{name: "--config missing", path: "nope.toml", err: "couldn't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for _, d := range tt.dirs {
				if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			for _, f := range tt.files {
				p := filepath.Join(root, f)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				write(t, p, sources[filepath.Ext(f)])
			}
			path := ""
			if tt.path != "" {
				path = filepath.Join(root, tt.path)
			}
			c, err := config.Load(path, filepath.Join(root, tt.dir))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), strings.ReplaceAll(tt.err, "ROOT", root)) {
					t.Fatalf("Load() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			want := &config.Config{}
			if tt.file != "" {
				want = &config.Config{File: filepath.Join(root, tt.file), Lints: levels[filepath.Ext(tt.file)]}
			}
			if !reflect.DeepEqual(c, want) {
				t.Errorf("Load() = %+v, want %+v", c, want)
			}
		})
	}
}

// TestParseUnknownFormat rejects a file whose extension names no format.
func TestParseUnknownFormat(t *testing.T) {
	if _, err := config.Parse("sigil.ini", nil); err == nil || !strings.Contains(err.Error(), "sigil.ini isn't a .yaml, .yml, .json or .toml file") {
		t.Fatalf("Parse() error = %v", err)
	}
}

// TestLoadTwoHere names the working directory, rather than ".", when it
// holds two configuration files, and names them relative to it.
func TestLoadTwoHere(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	write(t, "sigil.toml", "")
	write(t, ".sigil.json", "{}")
	_, err := config.Load("", ".")
	if want := "sigil.toml and .sigil.json in the working directory are both configuration files"; err == nil || err.Error() != want {
		t.Fatalf("Load() error = %v, want %q", err, want)
	}
	if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, "keep one of them") {
		t.Errorf("advice = %q", advice)
	}
}

// TestLoadUnreadable fails when a directory on the way up can't be
// searched, rather than skipping a configuration file it may hold.
func TestLoadUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can search any directory")
	}
	dir := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(dir, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := config.Load("", dir); err == nil || !strings.Contains(err.Error(), "sigil.yaml can't be read") {
		t.Fatalf("Load() error = %v, want the first name that can't be read", err)
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
	write(t, filepath.Join(repo, "sigil.yaml"), "kinds: [kinds/deploy.sigil]\nrequire:\n  - policy: deploy.guardrails\n    trusted: [platform/deploy]\n")
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
			if want := filepath.Join(tt.base, "sigil.yaml"); c.File != want {
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
