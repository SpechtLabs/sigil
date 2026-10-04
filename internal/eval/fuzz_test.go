package eval_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

func FuzzEvalExpr(f *testing.F) {
	k, b, errs := gokind.Build(gokind.Options{
		Name: "Test", Version: 1, Input: reflect.TypeFor[Input](), Collect: true,
		Decisions: []gokind.Decision{{Name: "allow", Payload: reflect.TypeFor[None](), Reasons: []string{"ok"}}},
	})
	if errs != nil {
		f.Fatal(errs)
	}
	for _, src := range []string{"count + 1", "ratio - 0.5", "release.soak >= 1h", "release.parent?.author.name ?? \"nobody\"", "[1, 2][count]", "all x in service.counts: x > count", "filter x in service.counts: x > count", "actor.roles any in [\"admin\"]", `service.labels has "team"`, `service.name matches "a.*"`, `service.name like "*api"`, "[[]] in [[[]]]", "true or [1][9] == 0",
		"-(1h - 90m) + 1ms", "0.1 + 0.2 - -0.0", "[-9223372036854775807 - 1]", `{"a": [1.5 - 0.25, -2.5]}`, "{1: 2h, -3: 0ms}", `[["x"], []]`} {
		f.Add(src, int64(0))
	}
	f.Fuzz(func(t *testing.T, src string, n int64) {
		x, errs := parser.ParseExpr("fuzz.sigil", []byte(src))
		if errs != nil {
			return
		}
		c := check.New("fuzz.sigil")
		typ := c.Expr(x, check.NewEnv(k))
		if c.Errors() != nil {
			return
		}
		scope := eval.NewScope(b)
		prog, err := eval.Compile(x, c.Info(), scope)
		if err != nil {
			t.Fatalf("checked expression failed compilation: %v", err)
		}
		in := input
		in.Count = int(n)
		v, first := eval.Run(prog, eval.NewFrame(&in, scope))
		w, second := eval.Run(prog, eval.NewFrame(&in, scope))
		if !reflect.DeepEqual(first, second) { //nolint:govet // deepequalerrors: a runtime error compares field by field, its Cause included
			t.Fatalf("runtime errors changed: %v != %v", first, second)
		}
		if first == nil && !reflect.DeepEqual(b.Canonical(typ, v), b.Canonical(typ, w)) {
			t.Fatal("evaluation is not repeatable")
		}
		sameAsConstant(t, x, typ, b.Canonical(typ, v), first)
	})
}

// sameAsConstant checks that an expression constant folding accepts, as
// for a kind's defaults, evaluates at run time to the same value.
func sameAsConstant(t *testing.T, x ast.Expr, typ types.Type, got any, err *diag.Error) { //nolint:emptyinterface // canonical values are dynamically typed
	t.Helper()
	if want, cerr := constant.Eval(x, typ); cerr == nil && (err != nil || !reflect.DeepEqual(got, want)) {
		t.Fatalf("%s: runtime = %#v, %v; constant = %#v", ast.Sprint(x), got, err, want)
	}
}

// Both evaluation paths must implement the same checked integer arithmetic.
// Each mutation produces valid source so this reaches the evaluator every time.
func FuzzEvalArithmetic(f *testing.F) {
	f.Add(int64(1), int64(2), false)
	f.Add(int64(9223372036854775807), int64(1), false)
	f.Add(int64(-9223372036854775808), int64(1), true)
	f.Fuzz(func(t *testing.T, a, b int64, sub bool) {
		op := "+"
		if sub {
			op = "-"
		}
		src := fmt.Sprintf("(%s) %s (%s)", constant.Format(a), op, constant.Format(b))
		x, errs := parser.ParseExpr("fuzz.sigil", []byte(src))
		if errs != nil {
			t.Fatal(errs)
		}
		c := check.New("fuzz.sigil")
		c.ExprAs(x, check.NewEnv(nil), types.Int)
		if c.Errors() != nil {
			t.Fatal(c.Errors())
		}
		scope := eval.NewScope(nil)
		prog, err := eval.Compile(x, c.Info(), scope)
		if err != nil {
			t.Fatal(err)
		}
		got, runErr := eval.Run(prog, eval.NewFrame(struct{}{}, scope))
		want, constErr := constant.Eval(x, types.Int)
		if (runErr == nil) != (constErr == nil) || runErr == nil && got.Int() != want.(int64) {
			t.Fatalf("%s: runtime = %v, %v; constant = %v, %v", src, got, runErr, want, constErr)
		}
	})
}

type fuzzLists struct {
	XS []int64 `policy:"xs"`
	YS []int64 `policy:"ys"`
}

func FuzzEvalCollections(f *testing.F) {
	k, b, errs := gokind.Build(gokind.Options{
		Name: "Lists", Version: 1, Input: reflect.TypeFor[fuzzLists](), Collect: true,
		Decisions: []gokind.Decision{{Name: "allow", Payload: reflect.TypeFor[None](), Reasons: []string{"ok"}}},
	})
	if errs != nil {
		f.Fatal(errs)
	}
	x, errs := parser.ParseExpr("lists.sigil", []byte(`[
  xs all in ys, xs any in ys, xs one in ys, xs exclusive in ys,
  (all a in xs: any b in ys: a == b), (any a in xs: a in ys)
]`))
	if errs != nil {
		f.Fatal(errs)
	}
	c := check.New("lists.sigil")
	c.Expr(x, check.NewEnv(k))
	if c.Errors() != nil {
		f.Fatal(c.Errors())
	}
	scope := eval.NewScope(b)
	prog, err := eval.Compile(x, c.Info(), scope)
	if err != nil {
		f.Fatal(err)
	}
	f.Add([]byte{}, []byte{})
	f.Add([]byte{1, 1, 2}, []byte{1, 1})
	f.Add([]byte{1, 2}, []byte{2, 1})
	f.Fuzz(func(t *testing.T, left, right []byte) {
		// Bound generated collection sizes so nested quantifiers remain a
		// useful semantic check rather than spending each run on huge lists.
		in := fuzzLists{}
		set, found := map[int64]bool{}, map[int64]bool{}
		for _, v := range right[:min(len(right), 32)] {
			in.YS = append(in.YS, int64(v))
			set[int64(v)] = true
		}
		all := true
		for _, v := range left[:min(len(left), 32)] {
			in.XS = append(in.XS, int64(v))
			if set[int64(v)] {
				found[int64(v)] = true
			} else {
				all = false
			}
		}
		want := []bool{all, len(found) > 0, len(found) == 1, len(found) <= 1, all, len(found) > 0}
		got, err := eval.Run(prog, eval.NewFrame(&in, scope))
		if err != nil {
			t.Fatal(err)
		}
		for i, expected := range want {
			if actual := eval.Bool(got.Index(i)); actual != expected {
				t.Fatalf("operator %d: got %v, want %v, xs=%v ys=%v", i, actual, expected, in.XS, in.YS)
			}
		}
	})
}

// FuzzEvalEnum evaluates enum expressions over host values that may lie
// outside their enum: evaluation never panics, it fails or succeeds the
// same way twice, and a constant, such as `Tier.critical`, evaluates to
// the value constant folding gives it.
func FuzzEvalEnum(f *testing.F) {
	k, b := accountsKind(f)
	for _, src := range []string{"account.tier == critical", "critical in account.tiers", "account.limits has standard", "next(account.tier) == standard",
		"{critical: 1}[account.tier] == 1", "(account.backup ?? internal) != standard", "any x in account.tiers: {internal: true} has x", "account.plan == basic",
		"Tier.internal", "{critical: [Plan.basic, Plan.standard]}", "[critical, (Tier.standard)]"} {
		f.Add(src, "critical")
		f.Add(src, "")
	}
	f.Fuzz(func(t *testing.T, src, value string) {
		x, errs := parser.ParseExpr("fuzz.sigil", []byte(src))
		if errs != nil {
			return
		}
		c := check.New("fuzz.sigil")
		typ := c.Expr(x, check.NewEnv(k))
		if c.Errors() != nil {
			return
		}
		scope := eval.NewScope(b)
		prog, err := eval.Compile(x, c.Info(), scope)
		if err != nil {
			t.Fatalf("checked expression failed compilation: %v", err)
		}
		in := account()
		in.Account.Tier, in.Account.Plan = Tier(value), Plan(value)
		in.Account.Tiers = append(in.Account.Tiers, Tier(value))
		in.Account.Limits[Tier(value)] = 7
		v, first := eval.Run(prog, eval.NewFrame(&in, scope))
		w, second := eval.Run(prog, eval.NewFrame(&in, scope))
		if !reflect.DeepEqual(first, second) { //nolint:govet // deepequalerrors: a runtime error compares field by field
			t.Fatalf("runtime errors changed: %v != %v", first, second)
		}
		if first == nil && !reflect.DeepEqual(b.Canonical(typ, v), b.Canonical(typ, w)) {
			t.Fatal("evaluation is not repeatable")
		}
		sameAsConstant(t, x, typ, b.Canonical(typ, v), first)
	})
}
