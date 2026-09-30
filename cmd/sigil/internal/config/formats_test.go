package config_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/internal/lint"
)

// TestParseFormats reads the same configuration from JSON and TOML as
// TestParse does from YAML, and checks that every error is the YAML
// one, positioned where the format writes what it's about, with advice
// written in the format. TOML arrays have no position of their own, so
// an error about an array is at its key. An empty source skips the
// format: TOML has no null, and JSON can't repeat a table.
func TestParseFormats(t *testing.T) {
	file := func(ext string) string { return filepath.Join("repo", "policies", "sigil"+ext) }
	dir := filepath.Join("repo", "policies")
	tests := []struct {
		name             string
		json, toml       string
		jsonErr, tomlErr string // the start of the message, after the file name
		advice           []string
	}{
		{
			name:    "unknown lint",
			json:    `{"lints": {"gated-denies": "error"}}`,
			toml:    "[lints]\ngated-denies = \"error\"\n",
			jsonErr: `:1:12: unknown lint "gated-denies"`, tomlErr: `:2:1: unknown lint "gated-denies"`,
			advice: []string{`did you mean "gated-deny"?`, `did you mean "gated-deny"?`},
		},
		{
			name:    "bad level",
			json:    `{"lints": {"gated-deny": "fatal"}}`,
			toml:    "[lints]\ngated-deny = \"fatal\"\n",
			jsonErr: `:1:26: lint gated-deny has unknown level "fatal"`, tomlErr: `:2:14: lint gated-deny has unknown level "fatal"`,
			advice: []string{"off, warn or error", "off, warn or error"},
		},
		{
			name:    "a level that isn't a string",
			json:    `{"lints": {"gated-deny": ["error"]}}`,
			toml:    "[lints]\ngated-deny = [\"error\"]\n",
			jsonErr: `:1:26: lint gated-deny has unknown level ""`, tomlErr: `:2:1: lint gated-deny has unknown level ""`,
		},
		{
			name:    "a lint set twice",
			json:    `{"lints": {"gated-deny": "error", "gated-deny": "warn"}}`,
			toml:    "[lints]\ngated-deny = \"error\"\ngated-deny = \"warn\"\n",
			jsonErr: ":1:35: lint gated-deny is set twice", tomlErr: ":3:1: lint gated-deny is set twice",
		},
		{
			name:    "lints isn't a map",
			json:    `{"lints": ["gated-deny"]}`,
			toml:    "lints = [\"gated-deny\"]\n",
			jsonErr: ":1:11: lints isn't a map", tomlErr: ":1:1: lints isn't a map",
			advice: []string{"`\"gated-deny\": \"error\"`", "`gated-deny = \"error\"`"},
		},
		{
			name:    "a key that holds something else takes a dotted key",
			toml:    "lints = 1\nlints.gated-deny = \"error\"\n",
			tomlErr: ":1:9: lints isn't a map",
		},
		{
			name:    "unknown top-level key",
			json:    `{"lint": {}}`,
			toml:    "[lint]\ngated-deny = \"error\"\n",
			jsonErr: `:1:2: unknown key "lint"`, tomlErr: `:1:2: unknown key "lint"`,
			advice: []string{"did you mean \"lints\"?; sigil.json holds `\"kinds\"`, `\"require\"` and `\"lints\"`", "did you mean \"lints\"?; sigil.toml holds `kinds`, `require` and `lints`"},
		},
		{
			name:    "a key set twice",
			json:    "{\"kinds\": [\"a.sigil\"],\n \"kinds\": [\"b.sigil\"]}",
			toml:    "kinds = [\"a.sigil\"]\nkinds = [\"b.sigil\"]\n",
			jsonErr: ":2:2: kinds is set twice", tomlErr: ":2:1: kinds is set twice",
			advice: []string{"first set at line 1", "first set at line 1"},
		},
		{
			name:    "a table set twice",
			toml:    "[lints]\ngated-deny = \"error\"\n[lints]\nunused-let = \"off\"\n",
			tomlErr: ":3:2: lints is set twice",
		},
		{
			name:    "an array of tables after an array",
			toml:    "require = [{policy = \"a\"}]\n[[require]]\npolicy = \"b\"\n",
			tomlErr: ":2:3: require is set twice",
		},
		{
			name:    "a table inside the last array table",
			toml:    "[[require]]\npolicy = \"a\"\n\n[require.x]\ny = 1\n",
			tomlErr: `:4:10: unknown key "x" in require[0]`,
		},
		{
			name:    "not a map",
			json:    `["kinds"]`,
			jsonErr: ":1:1: the configuration isn't a map",
		},
		{
			name:    "not the format",
			json:    "{\"kinds\": [\"a\"\n}",
			toml:    "kinds = [\"a\"\n",
			jsonErr: ":2:1: invalid JSON: invalid character '}' after array element", tomlErr: ":1:13: invalid TOML: array is incomplete",
			advice: []string{"a comment, for one, isn't JSON", "fix the TOML syntax"},
		},
		{
			name:    "a comment in JSON",
			json:    "{\"kinds\": [\"a\"] // vendored\n}",
			jsonErr: ":1:17: invalid JSON: invalid character '/'",
		},
		{
			name:    "more after the object",
			json:    `{} {}`,
			jsonErr: ":1:4: invalid JSON: more follows the configuration's object",
		},
		{
			name:    "the file ends early",
			json:    `{"kinds": `,
			jsonErr: ":1:11: invalid JSON: the file ends before the JSON does",
		},
		{
			name:    "a kind that isn't a string",
			json:    `{"kinds": [{"file": "a.sigil"}]}`,
			toml:    "kinds = [{file = \"a.sigil\"}]\n",
			jsonErr: ":1:12: kinds[0] isn't a string", tomlErr: ":1:10: kinds[0] isn't a string",
			advice: []string{"a kind file, relative to sigil.json", "a kind file, relative to sigil.toml"},
		},
		{
			name:    "an empty kind",
			json:    `{"kinds": [""]}`,
			toml:    "kinds = [\"\"]\n",
			jsonErr: ":1:12: kinds[0] is empty", tomlErr: ":1:10: kinds[0] is empty",
		},
		{
			name:    "a null kind",
			json:    `{"kinds": [null]}`,
			jsonErr: ":1:12: kinds[0] is empty",
		},
		{
			name:    "require isn't a list",
			json:    `{"require": "deploy.guardrails"}`,
			toml:    "require = \"deploy.guardrails\"\n",
			jsonErr: ":1:13: require isn't a list", tomlErr: ":1:11: require isn't a list",
			advice: []string{"`{\"policy\": \"deploy.guardrails\"}`", "`[[require]]` table"},
		},
		{
			name:    "a require entry that isn't a map",
			json:    `{"require": ["deploy.guardrails"]}`,
			toml:    "require = [\"deploy.guardrails\"]\n",
			jsonErr: ":1:14: require[0] isn't a map", tomlErr: ":1:12: require[0] isn't a map",
		},
		{
			name:    "a require entry without a policy",
			json:    `{"require": [{"trusted": ["platform"]}]}`,
			toml:    "[[require]]\ntrusted = [\"platform\"]\n",
			jsonErr: ":1:14: require[0] names no policy", tomlErr: ":1:3: require[0] names no policy",
			advice: []string{"add `\"policy\"`", "add `policy`"},
		},
		{
			name:    "an inline require entry without a policy",
			toml:    "require = [{policy = \"a\"}, {trusted = \"x\"}]\n",
			tomlErr: ":1:28: require[1] names no policy",
		},
		{
			name:    "an empty policy",
			json:    `{"require": [{"policy": ""}]}`,
			toml:    "[[require]]\npolicy = \"\"\n",
			jsonErr: ":1:25: require[0].policy is empty", tomlErr: ":2:10: require[0].policy is empty",
		},
		{
			name:    "a policy that isn't a name",
			json:    `{"require": [{"policy": ["a", "b"]}]}`,
			toml:    "[[require]]\npolicy = [\"a\", \"b\"]\n",
			jsonErr: ":1:25: require[0].policy isn't a name", tomlErr: ":2:1: require[0].policy isn't a name",
		},
		{
			name:    "a policy pattern",
			json:    `{"require": [{"policy": "deploy.*"}]}`,
			toml:    "[[require]]\npolicy = \"deploy.*\"\n",
			jsonErr: `:1:25: require[0].policy "deploy.*" is a pattern`, tomlErr: `:2:10: require[0].policy "deploy.*" is a pattern`,
			advice: []string{"`\"roots\"` takes the patterns", "`roots` takes the patterns"},
		},
		{
			name:    "an unknown require key",
			json:    `{"require": [{"policy": "a", "root": ["payments.*"]}]}`,
			toml:    "[[require]]\npolicy = \"a\"\nroot = [\"payments.*\"]\n",
			jsonErr: `:1:30: unknown key "root" in require[0]`, tomlErr: `:3:1: unknown key "root" in require[0]`,
			advice: []string{"did you mean \"roots\"?; a require entry holds `\"policy\"`, `\"trusted\"` and `\"roots\"`", "did you mean \"roots\"?; a require entry holds `policy`, `trusted` and `roots`"},
		},
		{
			name:    "a require key set twice",
			json:    `{"require": [{"policy": "a", "policy": "b"}]}`,
			toml:    "[[require]]\npolicy = \"a\"\npolicy = \"b\"\n",
			jsonErr: ":1:30: policy is set twice in require[0]", tomlErr: ":3:1: policy is set twice in require[0]",
		},
		{
			name:    "a policy required twice",
			json:    "{\"require\": [\n  {\"policy\": \"a\"},\n  {\"policy\": \"a\"}\n]}",
			toml:    "[[require]]\npolicy = \"a\"\n\n[[require]]\npolicy = \"a\"\n",
			jsonErr: ":3:14: a is required twice", tomlErr: ":5:10: a is required twice",
			advice: []string{"first required at line 2; merge the two entries' `\"trusted\"` and `\"roots\"`", "first required at line 2; merge the two entries' `trusted` and `roots`"},
		},
		{
			name:    "an empty root",
			json:    `{"require": [{"policy": "a", "roots": [""]}]}`,
			toml:    "[[require]]\npolicy = \"a\"\nroots = [\"\"]\n",
			jsonErr: ":1:40: require[0].roots[0] is empty", tomlErr: ":3:10: require[0].roots[0] is empty",
		},
		{
			name:    "a trusted path that isn't a string",
			json:    `{"require": [{"policy": "a", "trusted": [["platform"]]}]}`,
			toml:    "[[require]]\npolicy = \"a\"\ntrusted = [[\"platform\"]]\n",
			jsonErr: ":1:42: require[0].trusted[0] isn't a string", tomlErr: ":3:1: require[0].trusted[0] isn't a string",
		},
	}
	for _, tt := range tests {
		for i, f := range []struct{ ext, src, err string }{{".json", tt.json, tt.jsonErr}, {".toml", tt.toml, tt.tomlErr}} {
			if f.src == "" {
				continue
			}
			t.Run(tt.name+f.ext, func(t *testing.T) {
				_, err := config.Parse(file(f.ext), []byte(f.src))
				if want := file(f.ext) + f.err; err == nil || !strings.HasPrefix(err.Error(), want) {
					t.Fatalf("Parse() error = %v, want it to start with %q", err, want)
				}
				if tt.advice == nil {
					return
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.advice[i]) {
					t.Errorf("advice = %q, want %q", advice, tt.advice[i])
				}
			})
		}
	}

	// The configuration of TestParse's "everything", in JSON and TOML.
	want := func(ext string, deploy, access config.Pos) *config.Config {
		return &config.Config{
			File:  file(ext),
			Kinds: []string{filepath.Join("repo", "vendor", "deploy_approval.sigil")},
			Require: []config.Require{
				{Policy: "deploy.guardrails", Trusted: []string{filepath.Join(dir, "platform", "deploy")}, Roots: []string{"payments.*", "checkout.*"}, Pos: deploy},
				{Policy: "access.guardrails", Pos: access},
			},
			Lints: map[string]lint.Level{lint.GatedDeny: lint.Error, lint.UnusedLet: lint.Off},
		}
	}
	valid := []struct {
		name, ext, src string
		want           *config.Config
	}{
		{
			name: "everything", ext: ".json",
			src: `{
  "kinds": ["../vendor/deploy_approval.sigil"],
  "require": [
    {"policy": "deploy.guardrails", "trusted": ["platform\/deploy"], "roots": ["payments.*", "checkout.*"]},
    {"policy": "access.guardrails"}
  ],
  "lints": {"gated-deny": "error", "unused-let": "off"}
}
`,
			want: want(".json", config.Pos{Line: 4, Column: 16}, config.Pos{Line: 5, Column: 16}),
		},
		{
			name: "everything", ext: ".toml",
			src: `kinds = ["../vendor/deploy_approval.sigil"]

[[require]]
policy = "deploy.guardrails"
trusted = ["platform/deploy"]
roots = ["payments.*", "checkout.*"]

[[require]]
policy = "access.guardrails"

[lints]
gated-deny = "error"
unused-let = 'off'
`,
			want: want(".toml", config.Pos{Line: 4, Column: 10}, config.Pos{Line: 9, Column: 10}),
		},
		{
			name: "dotted keys and inline tables", ext: ".toml",
			src: `kinds = "../vendor/deploy_approval.sigil"
lints.gated-deny = "error"
lints."unused-let" = "off"
require = [
  {policy = "deploy.guardrails", trusted = "platform/deploy", roots = ["payments.*", "checkout.*"]},
  {policy = "access.guardrails"},
]
`,
			want: want(".toml", config.Pos{Line: 5, Column: 13}, config.Pos{Line: 6, Column: 13}),
		},
		{name: "empty", ext: ".json", src: " \n", want: &config.Config{File: file(".json"), Lints: map[string]lint.Level{}}},
		{name: "null", ext: ".json", src: "null", want: &config.Config{File: file(".json"), Lints: map[string]lint.Level{}}},
		{name: "empty object", ext: ".json", src: "{}", want: &config.Config{File: file(".json"), Lints: map[string]lint.Level{}}},
		{name: "empty", ext: ".toml", src: "# nothing yet\n", want: &config.Config{File: file(".toml"), Lints: map[string]lint.Level{}}},
		{name: "scalars read as strings", ext: ".json", src: `{"kinds": [1, 2.5, true, false]}`, want: &config.Config{File: file(".json"), Kinds: []string{filepath.Join(dir, "1"), filepath.Join(dir, "2.5"), filepath.Join(dir, "true"), filepath.Join(dir, "false")}, Lints: map[string]lint.Level{}}},
		{name: "scalars read as strings", ext: ".toml", src: "kinds = [1, 2.5, true, 1979-05-27]\n", want: &config.Config{File: file(".toml"), Kinds: []string{filepath.Join(dir, "1"), filepath.Join(dir, "2.5"), filepath.Join(dir, "true"), filepath.Join(dir, "1979-05-27")}, Lints: map[string]lint.Level{}}},
		{
			name: "$schema is ignored", ext: ".json",
			src:  `{"$schema": "https://sigil.specht-labs.de/schema/config.json", "lints": {"unused-let": "off"}}`,
			want: &config.Config{File: file(".json"), Lints: map[string]lint.Level{lint.UnusedLet: lint.Off}},
		},
		{
			name: "$schema is ignored", ext: ".toml",
			src:  "#:schema https://sigil.specht-labs.de/schema/config.json\n\"$schema\" = \"https://sigil.specht-labs.de/schema/config.json\"\n[lints]\nunused-let = \"off\"\n",
			want: &config.Config{File: file(".toml"), Lints: map[string]lint.Level{lint.UnusedLet: lint.Off}},
		},
		{name: "an upper-case extension", ext: ".TOML", src: "[lints]\nunused-let = \"off\"\n", want: &config.Config{File: file(".TOML"), Lints: map[string]lint.Level{lint.UnusedLet: lint.Off}}},
	}
	for _, tt := range valid {
		t.Run(tt.name+tt.ext, func(t *testing.T) {
			c, err := config.Parse(file(tt.ext), []byte(tt.src))
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if !reflect.DeepEqual(c, tt.want) {
				t.Errorf("Parse() = %+v, want %+v", c, tt.want)
			}
		})
	}
}
