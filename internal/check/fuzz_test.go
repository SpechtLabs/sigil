package check_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

func FuzzLoadKind(f *testing.F) {
	for _, src := range []string{deploy, "", "kind K version 1\ndecision allow { ok }\ncollect all", "kind K version 1\ntype A { a: A }", "kind K version 2, accepts: 1\ndecision d(n: int = 1 + 2) { r }\ncollect all"} {
		f.Add(src)
	}
	f.Add("kind K version 1\ndecision d(f: float = " + strings.Repeat("9", 308) + ".0 + " + strings.Repeat("9", 308) + ".0) { r }\ncollect all")
	f.Fuzz(func(t *testing.T, src string) {
		k, errs := check.LoadKind("fuzz.sigil", []byte(src))
		if errs != nil {
			if k != nil {
				t.Fatal("invalid kind returned a model")
			}
			return
		}
		out := k.Source()
		again, errs := check.LoadKind("export.sigil", []byte(out))
		if errs != nil {
			t.Fatalf("export does not load: %v\n%s", errs, out)
		}
		if !reflect.DeepEqual(k, again) || out != again.Source() {
			t.Fatalf("kind changed on export/import:\n%s\nthen:\n%s", out, again.Source())
		}
	})
}

func FuzzCheckExpr(f *testing.F) {
	k, errs := check.LoadKind("kind.sigil", []byte(deploy))
	if errs != nil {
		f.Fatal(errs)
	}
	for _, src := range []string{"true", "[]", "{}", "[[], [1]]", "service.labels[\"x\"]", "release.soak >= 1h", "all x in actor.roles: x != \"admin\"", "filter x in actor.roles: x != \"admin\"", "split(service.name, \"-\")", "present service", "unknown.field", "all r in outcome.review: r.reason == review.service_owner", "filter r in outcome.review: \"a\" in r.approvers", "outcome.review[0]"} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		x, errs := parser.ParseExpr("fuzz.sigil", []byte(src))
		if errs != nil {
			return
		}
		// Outside an assert and inside one, where `outcome` and its
		// candidates are values too.
		for _, inAssert := range []bool{false, true} {
			env := func() *check.Env {
				e := check.NewEnv(k)
				e.InAssert = inAssert
				return e
			}
			c := check.New("fuzz.sigil")
			got := c.Expr(x, env())
			if c.Errors() == nil && (got == nil || got == types.Invalid) {
				t.Fatal("checker accepted an expression without a valid type")
			}
			other := check.New("fuzz.sigil")
			other.Expr(x, env())
			if !reflect.DeepEqual(c.Info(), other.Info()) || !reflect.DeepEqual(c.Errors(), other.Errors()) {
				t.Fatal("checking is not deterministic")
			}
		}
	})
}
