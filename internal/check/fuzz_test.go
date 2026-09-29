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
	for _, src := range []string{
		deploy, "",
		"kind K version 1\ndecision allow { reason: ok }\ncollect all",
		"kind K version 1\ntype A { a: A }",
		"kind K version 2, accepts: 1\ndecision d { reason: r  n: int = 1 + 2 }\ncollect all",
		"kind K version 1\nenum Tier: a | b\nenum Plan: b\n  | c\ninput t: map<Tier, list<?Plan>>\ndecision d { reason: a | Tier  t: Tier = b }\ncollect all\ndefault d(t: a, reason: a)",
		"kind K version 1\nenum Reason: a\ndecision d { reason: Reason }\ncollect all",
		"kind K version 1\nenum Tier: a | b\ndecision d { reason: x  t: Tier = Tier.a }\ncollect all\ndefault d(reason: d.x, t: Tier.c)",
		"kind K version 1\ndecision d(n: int = 1) { a b }\ncollect one\nprecedence d\ndefault d(a, n: 2)",
	} {
		f.Add(src)
	}
	f.Add("kind K version 1\ndecision d { reason: r  f: float = " + strings.Repeat("9", 308) + ".0 + " + strings.Repeat("9", 308) + ".0 }\ncollect all")
	f.Add("kind K version 1\ndecision conflict { reason: conflict | r  n: int = 1 }\ncollect one\nprecedence conflict\ndefault conflict(reason: r)\nconflict conflict(reason: conflict, n: 2)")
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
	for _, src := range []string{"true", "[]", "{}", "[[], [1]]", "service.labels[\"x\"]", "release.soak >= 1h", "all x in actor.roles: x != \"admin\"", "filter x in actor.roles: x != \"admin\"", "split(service.name, \"-\")", "present service", "unknown.field", "all r in outcome.review: r.reason == review.service_owner", "filter r in outcome.review: \"a\" in r.approvers", "outcome.review[0]",
		"service.tier == critical", "standard in service.plans", "[standard, service.tier]", "service.by_tier has {critical: 1}", "service.backup ?? internal", "tier_of(\"x\") != standard", "{standard: [free]}", "service.tier in [\"critical\"]", "critcal == service.tier", "Tier.standard == service.tier", "[Plan.standard, free]", "Tier", "Tier.nope", "Tier?.critical", "Tier(1)", "service.plan == Tier.standard",
	} {
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
			if !reflect.DeepEqual(c.Info(), other.Info()) || !reflect.DeepEqual(c.Errors(), other.Errors()) { //nolint:govet // deepequalerrors: diagnostics compare field by field, and a check error has no Cause
				t.Fatal("checking is not deterministic")
			}
		}
	})
}
