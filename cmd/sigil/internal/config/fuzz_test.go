package config_test

import (
	"reflect"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
)

func FuzzConfig(f *testing.F) {
	fuzz(f, "sigil.yaml", "", "{}", "lints:\n  unused-let: error\n", "lints: {unknown: warn}", "---\n{}\n---\n{}", "&a [*a]", "kinds: [a.sigil]\nrequire:\n  - policy: p\n    trusted: t\n    roots: [\"x.*\"]\n", "require: [&e {policy: p}, *e]")
}

func FuzzConfigJSON(f *testing.F) {
	fuzz(f, "sigil.json", "", "{}", "null", `{"lints": {"unused-let": "error"}}`, `{"kinds": ["a.sigil"], "kinds": []}`, `{"require": [{"policy": "p", "trusted": "t", "roots": ["x.*"]}]}`, `[1, 2.5, true, null]`, `{"a": `, `{} {}`)
}

func FuzzConfigTOML(f *testing.F) {
	fuzz(f, "sigil.toml", "", "# empty\n", "[lints]\nunused-let = \"error\"\n", "kinds = [\"a.sigil\"]\n[[require]]\npolicy = \"p\"\ntrusted = \"t\"\nroots = [\"x.*\"]\n", "lints.a = 1\nlints = 2\n", "require = [{policy = \"p\"}]\n[[require]]\n[require.x]\n", "[a.b.c]\n[[a.b]]\n", "kinds = [1979-05-27, 2.5e3, inf]\n")
}

// fuzz checks that parsing a configuration named file doesn't panic and
// gives the same result twice, starting from seeds.
func fuzz(f *testing.F, file string, seeds ...string) {
	for _, src := range seeds {
		f.Add([]byte(src))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		a, err := config.Parse(file, src)
		b, again := config.Parse(file, src)
		if (err == nil) != (again == nil) || err != nil && err.Error() != again.Error() || !reflect.DeepEqual(a, b) {
			t.Fatal("configuration parsing is not deterministic")
		}
	})
}
