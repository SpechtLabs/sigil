package releasecheck

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The repository, three levels above this package.
const repoRoot = "../../../.."

// TestRepository is the guard: every manifest the repository ships has its
// version bumped by release-please, and every bumped version is the
// release's.
func TestRepository(t *testing.T) {
	problems, err := Check(repoRoot)
	if err != nil {
		t.Fatal(err.Display())
	}
	for _, p := range problems {
		t.Error(p)
	}

	shipped, err := Discover(repoRoot)
	if err != nil {
		t.Fatal(err.Display())
	}
	// The ones this guard was written for, so a broken search can't pass by
	// finding nothing.
	for _, want := range []string{
		"bindings/typescript/package.json",
		"bindings/rust/Cargo.toml",
		"bindings/rust/Cargo.lock",
		"examples/feature-flags/Cargo.lock",
		"editors/vscode/package.json",
		"editors/tree-sitter-sigil/tree-sitter.json",
		"editors/tree-sitter-sigil/src/parser.c",
	} {
		if !containsPath(shipped, want) {
			t.Errorf("Discover didn't find %s", want)
		}
	}
}

const config = `{"packages": {".": {"extra-files": [%s]}}}`

func TestCheck(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		extra string
		want  []string // a substring of each problem, in order
	}{
		{
			name:  "everything listed and in step",
			files: map[string]string{"pkg/package.json": `{"name": "p", "version": "1.2.3"}`},
			extra: `{"type": "json", "path": "pkg/package.json", "jsonpath": "$.version"}`,
		},
		{
			name:  "an unlisted package.json",
			files: map[string]string{"editors/new/package.json": `{"name": "n", "version": "0.1.0"}`},
			want: []string{
				`editors/new/package.json: ships with a version release-please doesn't bump; add {"type":"json","path":"editors/new/package.json","jsonpath":"$.version"} to packages["."]["extra-files"] in .release-please-config.json, and set its version to 1.2.3`,
			},
		},
		{
			name: "private packages, examples, docs, node_modules and testdata don't ship",
			files: map[string]string{
				"tools/package.json":                   `{"name": "t", "version": "0.0.1", "private": true}`,
				"examples/app/package.json":            `{"name": "a", "version": "0.0.1"}`,
				"docs/package.json":                    `{"name": "d", "version": "0.0.1"}`,
				"pkg/node_modules/dep/package.json":    `{"name": "dep", "version": "9.9.9"}`,
				"internal/x/testdata/pkg/package.json": `{"name": "x", "version": "0.0.1"}`,
				"cfg/package.json":                     `{"name": "c"}`,
			},
		},
		{
			name: "an unlisted crate, and its entry in a lockfile",
			files: map[string]string{
				"crate/Cargo.toml": "[package]\nname = \"my-crate\"\nversion = \"1.2.3\"\n",
				"crate/Cargo.lock": "[[package]]\nname = \"my-crate\"\nversion = \"1.2.3\"\n\n[[package]]\nname = \"serde\"\nversion = \"1.0.0\"\nsource = \"registry+https://github.com/rust-lang/crates.io-index\"\n",
			},
			want: []string{
				`crate/Cargo.lock: ships with a version release-please doesn't bump; add {"type":"toml","path":"crate/Cargo.lock","jsonpath":"$.package[?(@.name.value=='my-crate')].version"}`,
				`crate/Cargo.toml: ships with a version release-please doesn't bump; add {"type":"toml","path":"crate/Cargo.toml","jsonpath":"$.package.version"}`,
			},
		},
		{
			name: "a crate listed with its lockfiles, an example's included",
			files: map[string]string{
				"crate/Cargo.toml":        "[package]\nname = \"my-crate\"\nversion = \"1.2.3\"\n",
				"crate/Cargo.lock":        "[[package]]\nname = \"my-crate\"\nversion = \"1.2.3\"\n",
				"examples/app/Cargo.lock": "[[package]]\nname = \"my-crate\"\nversion = \"1.2.3\"\n\n[[package]]\nname = \"app\"\nversion = \"0.1.0\"\n",
				"examples/app/Cargo.toml": "[package]\nname = \"app\"\nversion = \"0.1.0\"\n",
				"workspace/Cargo.toml":    "[workspace]\nmembers = [\"crate\"]\n",
				"internal/Cargo.toml":     "[package]\nname = \"internal\"\nversion = \"0.1.0\"\npublish = false\n",
			},
			extra: `{"type": "toml", "path": "crate/Cargo.toml", "jsonpath": "$.package.version"},
				{"type": "toml", "path": "crate/Cargo.lock", "jsonpath": "$.package[?(@.name.value=='my-crate')].version"},
				{"type": "toml", "path": "examples/app/Cargo.lock", "jsonpath": "$.package[?(@.name.value=='my-crate')].version"}`,
		},
		{
			name: "an unlisted tree-sitter grammar, and the version in its parser",
			files: map[string]string{
				"editors/ts/tree-sitter.json": `{"metadata": {"version": "1.2.3"}}`,
				"editors/ts/src/parser.c":     parserC(1, 2, 3),
			},
			want: []string{
				`add {"type":"generic","path":"editors/ts/src/parser.c"} to`,
				`add {"type":"json","path":"editors/ts/tree-sitter.json","jsonpath":"$.metadata.version"}`,
			},
		},
		{
			name: "a listed grammar behind the release",
			files: map[string]string{
				"editors/ts/tree-sitter.json": `{"metadata": {"version": "0.1.0"}}`,
				"editors/ts/src/parser.c":     parserC(0, 1, 0),
			},
			extra: `{"type": "json", "path": "editors/ts/tree-sitter.json", "jsonpath": "$.metadata.version"},
				{"type": "generic", "path": "editors/ts/src/parser.c"}`,
			want: []string{
				"editors/ts/tree-sitter.json: is at 0.1.0 ($.metadata.version), but .release-please-manifest.json is at 1.2.3; set it to 1.2.3",
				"editors/ts/src/parser.c: is at 0.1.0",
			},
		},
		{
			name:  "a generic file with an x-release-please-version line",
			files: map[string]string{"VERSION.txt": "version 1.2.3 // x-release-please-version\n"},
			extra: `{"type": "generic", "path": "VERSION.txt"}`,
		},
		{
			name:  "a generic file without markers",
			files: map[string]string{"VERSION.txt": "1.2.3\n"},
			extra: `{"type": "generic", "path": "VERSION.txt"}`,
			want:  []string{"VERSION.txt: no x-release-please-version line, and not all of the -major, -minor and -patch lines"},
		},
		{
			name:  "a marked line without a number",
			files: map[string]string{"VERSION.txt": "major // x-release-please-major\n"},
			extra: `{"type": "generic", "path": "VERSION.txt"}`,
			want:  []string{"the x-release-please-major line has no version"},
		},
		{
			name:  "a generic file that's gone",
			extra: `{"type": "generic", "path": "gone.c"}`,
			want:  []string{"gone.c: failed to read gone.c"},
		},
		{
			name:  "a lockfile entry behind the release",
			files: map[string]string{"Cargo.lock": "[[package]]\nname = \"my-crate\"\nversion = \"1.2.2\"\n"},
			extra: `{"type": "toml", "path": "Cargo.lock", "jsonpath": "$.package[?(@.name.value=='my-crate')].version"}`,
			want:  []string{"Cargo.lock: is at 1.2.2"},
		},
		{
			name:  "a listed file that's gone",
			extra: `{"type": "json", "path": "gone/package.json", "jsonpath": "$.version"}`,
			want:  []string{"gone/package.json: failed to read gone/package.json"},
		},
		{
			name:  "a jsonpath that doesn't resolve",
			files: map[string]string{"cfg/settings.json": `{"version": {"major": 1}}`},
			extra: `{"type": "json", "path": "cfg/settings.json", "jsonpath": "$.version.minor"}`,
			want:  []string{`cfg/settings.json: jsonpath "$.version.minor" doesn't resolve: no "minor"`},
		},
		{
			name:  "a jsonpath that isn't a string",
			files: map[string]string{"cfg/settings.json": `{"version": 1}`},
			extra: `{"type": "json", "path": "cfg/settings.json", "jsonpath": "$.version"}`,
			want:  []string{`jsonpath "$.version" isn't a string`},
		},
		{
			name:  "a jsonpath this check can't evaluate",
			files: map[string]string{"cfg/settings.json": `{"version": "1.2.3"}`},
			extra: `{"type": "json", "path": "cfg/settings.json", "jsonpath": "version"}`,
			want:  []string{`jsonpath "version" isn't $.key.key or the Cargo.lock filter`},
		},
		{
			name:  "a lockfile filter that matches nothing",
			files: map[string]string{"Cargo.lock": "[[package]]\nname = \"other\"\nversion = \"1.2.3\"\n"},
			extra: `{"type": "toml", "path": "Cargo.lock", "jsonpath": "$.package[?(@.name.value=='my-crate')].version"}`,
			want:  []string{`0 entries named "my-crate" have a version, want exactly one`},
		},
		{
			name:  "an extra-file type this check can't read",
			files: map[string]string{"pom.xml": "<version>1.2.3</version>\n"},
			extra: `{"type": "xml", "path": "pom.xml", "jsonpath": "//version"}`,
			want:  []string{`pom.xml has extra-file type "xml", which this check can't read`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, ConfigFile, strings.Replace(config, "%s", tt.extra, 1))
			write(t, root, ManifestFile, `{".": "1.2.3"}`)
			for path, content := range tt.files {
				write(t, root, path, content)
			}

			problems, err := Check(root)
			if err != nil {
				t.Fatal(err.Display())
			}
			if len(problems) != len(tt.want) {
				t.Fatalf("got %d problems, want %d:\n%v", len(problems), len(tt.want), problems)
			}
			for i, p := range problems {
				if !strings.Contains(p.String(), tt.want[i]) {
					t.Errorf("problem %d is\n  %s\nwant it to contain\n  %s", i, p, tt.want[i])
				}
			}
		})
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name     string
		config   string
		manifest string
		want     string
	}{
		{name: "no config", manifest: `{".": "1.0.0"}`, want: "failed to read .release-please-config.json"},
		{name: "a config that isn't JSON", config: "{", manifest: `{".": "1.0.0"}`, want: ".release-please-config.json isn't valid JSON"},
		{name: "no manifest", config: `{}`, want: "failed to read .release-please-manifest.json"},
		{name: "no root package in the manifest", config: `{}`, manifest: `{"other": "1.0.0"}`, want: `has no version for the root package "."`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if tt.config != "" {
				write(t, root, ConfigFile, tt.config)
			}
			if tt.manifest != "" {
				write(t, root, ManifestFile, tt.manifest)
			}
			_, err := Check(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Check() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestDiscoverErrors(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{name: "a package.json that isn't JSON", files: map[string]string{"p/package.json": "{"}, want: "package.json isn't valid JSON"},
		{name: "a Cargo.toml that isn't TOML", files: map[string]string{"c/Cargo.toml": "[package"}, want: "c/Cargo.toml isn't valid TOML"},
		{name: "a Cargo.lock that isn't TOML", files: map[string]string{"c/Cargo.lock": "[[package"}, want: "c/Cargo.lock isn't valid TOML"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			for path, content := range tt.files {
				write(t, root, path, content)
			}
			_, err := Discover(root)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Discover() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}

func TestReadTOMLErrors(t *testing.T) {
	root := t.TempDir()
	write(t, root, "bad.toml", "[x")
	if _, err := Read(root, ExtraFile{Type: "toml", Path: "bad.toml", JSONPath: "$.x"}); err == nil || !strings.Contains(err.Error(), "isn't valid TOML") {
		t.Errorf("Read(bad.toml) error = %v", err)
	}
	if _, err := Read(root, ExtraFile{Type: "toml", Path: "missing.toml", JSONPath: "$.x"}); err == nil || !strings.Contains(err.Error(), "failed to read missing.toml") {
		t.Errorf("Read(missing.toml) error = %v", err)
	}
}

// parserC is the end of a generated parser, with its version marked the way
// grammar_test.go marks it.
func parserC(major, minor, patch int) string {
	return fmt.Sprintf(`    .metadata = {
      .major_version = %d, // x-release-please-major
      .minor_version = %d, // x-release-please-minor
      .patch_version = %d, // x-release-please-patch
    },
`, major, minor, patch)
}

func containsPath(files []ExtraFile, path string) bool {
	for _, f := range files {
		if f.Path == path {
			return true
		}
	}
	return false
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCheckDiscoverError(t *testing.T) {
	root := t.TempDir()
	write(t, root, ConfigFile, `{}`)
	write(t, root, ManifestFile, `{".": "1.0.0"}`)
	write(t, root, "p/package.json", "{")
	if _, err := Check(root); err == nil || !strings.Contains(err.Error(), "package.json isn't valid JSON") {
		t.Fatalf("Check() error = %v", err)
	}
}

func TestLookupThroughAScalar(t *testing.T) {
	root := t.TempDir()
	write(t, root, "p.json", `{"version": "1.2.3"}`)
	_, err := Read(root, ExtraFile{Type: "json", Path: "p.json", JSONPath: "$.version.major"})
	if err == nil || !strings.Contains(err.Error(), `jsonpath "$.version.major" doesn't resolve`) {
		t.Fatalf("Read() error = %v", err)
	}
}

// Files and directories the check can't read fail it, rather than being
// skipped as if they weren't there.
func TestUnreadable(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root reads everything")
	}
	tests := []struct {
		name string
		path string
		want string
	}{
		{name: "a Cargo.toml", path: "c/Cargo.toml", want: "failed to read c/Cargo.toml"},
		{name: "a Cargo.lock", path: "c/Cargo.lock", want: "failed to read c/Cargo.lock"},
		{name: "a directory", path: "d/inner", want: "failed to search the repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			write(t, root, tt.path, "")
			target := filepath.Join(root, tt.path)
			if tt.name == "a directory" {
				target = filepath.Dir(target)
			}
			if err := os.Chmod(target, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(target, 0o755) })
			if _, err := Discover(root); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Discover() error = %v, want it to contain %q", err, tt.want)
			}
		})
	}
}
