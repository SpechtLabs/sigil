package config_test

import (
	"reflect"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
)

func FuzzConfig(f *testing.F) {
	for _, src := range []string{"", "{}", "lints:\n  unused-let: error\n", "lints: {unknown: warn}", "---\n{}\n---\n{}", "&a [*a]", "kinds: [a.sigil]\nrequire:\n  - policy: p\n    trusted: t\n    roots: [\"x.*\"]\n", "require: [&e {policy: p}, *e]"} {
		f.Add([]byte(src))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		a, err := config.Parse("sigil.yaml", src)
		b, again := config.Parse("sigil.yaml", src)
		if (err == nil) != (again == nil) || err != nil && err.Error() != again.Error() || !reflect.DeepEqual(a, b) {
			t.Fatal("configuration parsing is not deterministic")
		}
	})
}
