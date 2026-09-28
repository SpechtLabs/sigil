package eval_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
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
	for _, src := range []string{"count + 1", "ratio - 0.5", "release.soak >= 1h", "release.parent?.author.name ?? \"nobody\"", "[1, 2][count]", "all x in service.counts: x > count", "actor.roles any in [\"admin\"]", `service.labels has "team"`, `service.name matches "a.*"`, `service.name like "*api"`, "[[]] in [[[]]]", "true or [1][9] == 0"} {
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
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("runtime errors changed: %v != %v", first, second)
		}
		if first == nil && !reflect.DeepEqual(b.Canonical(typ, v), b.Canonical(typ, w)) {
			t.Fatal("evaluation is not repeatable")
		}
	})
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
