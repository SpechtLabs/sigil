package build_test

import (
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/build"
)

func TestOperators(t *testing.T) {
	runModules(t, []moduleCase{
		{name: "comparisons", want: "let x =\n  score == 1.0\n  and score != 2.0\n  and score < 3.0\n  and score <= 4.0\n  and score > 5.0\n  and score >= 6.0", build: func(m *build.ModuleDoc[Request], in *Request) int {
			s := build.Field(&in.Score)
			build.Let(m, "x", build.And(s.Eq(build.Lit(1.0)), s.NotEq(build.Lit(2.0)), s.Lt(build.Lit(3.0)), s.Le(build.Lit(4.0)), s.Gt(build.Lit(5.0)), s.Ge(build.Lit(6.0))))
			return 0
		}},
		{name: "membership", want: "let x =\n  \"a\" in actor.roles\n  or \"b\" not in actor.roles\n  or \"c\" in actor.name", build: func(m *build.ModuleDoc[Request], in *Request) int {
			roles := build.Field(&in.Actor.Roles)
			build.Let(m, "x", build.Or(build.In(build.Lit("a"), roles), build.NotIn(build.Lit("b"), roles), build.Lit("c").Within(build.Field(&in.Actor.Name))))
			return 0
		}},
		{name: "list operators", want: "let x =\n  actor.roles all in [\"a\"]\n  and actor.roles any in [\"b\"]\n  and actor.roles one in [\"c\"]\n  and actor.roles exclusive in [\"d\"]", build: func(m *build.ModuleDoc[Request], in *Request) int {
			roles := build.Field(&in.Actor.Roles)
			build.Let(m, "x", build.And(
				build.AllIn(roles, build.Lit([]string{"a"})),
				build.AnyIn(roles, build.Lit([]string{"b"})),
				build.OneIn(roles, build.Lit([]string{"c"})),
				build.ExclusiveIn(roles, build.Lit([]string{"d"})),
			))
			return 0
		}},
		{name: "patterns", want: "let x = actor.name like \"bot-*\" xor actor.name matches `^\\w+$`", build: func(m *build.ModuleDoc[Request], in *Request) int {
			name := build.Field(&in.Actor.Name)
			build.Let(m, "x", build.Xor(name.Like("bot-*"), name.Matches(`^\w+$`)))
			return 0
		}},
		{name: "pattern with a backslash and a backtick", want: "let x = actor.name matches \"`\\\\d\"", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Field(&in.Actor.Name).Matches("`\\d"))
			return 0
		}},
		{name: "pattern with a backslash and a line ending", want: `let x = actor.name matches "\\d\r"`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Field(&in.Actor.Name).Matches("\\d\r"))
			return 0
		}},
		{name: "pattern with a backslash and invalid UTF-8", want: `let x = actor.name like "a\\\xff*"`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Field(&in.Actor.Name).Like("a\\\xff*"))
			return 0
		}},
		{name: "maps", want: "let x =\n  quotas has critical\n  and quotas[service.class] > 0\n  and service.labels has {\"a\": \"b\"}", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.And(
				build.HasKey(build.Field(&in.Quotas), build.Lit(ClassCritical)),
				build.Get(build.Field(&in.Quotas), build.Field(&in.Service.Class)).Gt(build.Lit(0)),
				build.HasAll(build.Field(&in.Service.Labels), build.Lit(map[string]string{"a": "b"})),
			))
			return 0
		}},
		{name: "one operand", want: "let x = not actor.name == \"\"", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Not(build.Or(build.Field(&in.Actor.Name).Eq(build.Lit("")))))
			return 0
		}},
		{name: "arithmetic", want: "let x = count + 1 - -count", build: func(m *build.ModuleDoc[Request], in *Request) int {
			c := build.Field(&in.Count)
			build.Let(m, "x", c.Add(build.Lit(int64(1))).Sub(build.Neg(c)))
			return 0
		}},
		{name: "timestamps", want: "let x = now + 1h - 30m - now", build: func(m *build.ModuleDoc[Request], in *Request) int {
			now := build.Field(&in.Now)
			build.Let(m, "x", build.TimeDiff(build.TimeSub(build.TimeAdd(now, build.Lit(time.Hour)), build.Lit(30*time.Minute)), now))
			return 0
		}},
		{name: "host functions", want: `let x = lower(actor.name) == "a" and size(actor.roles) == 1`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			lower := build.Func1[string, string]("lower")
			size := build.Func1[[]string, int]("size")
			build.Let(m, "x", build.And(lower(build.Field(&in.Actor.Name)).Eq(build.Lit("a")), size(build.Field(&in.Actor.Roles)).Eq(build.Lit(1))))
			return 0
		}},
		{name: "untyped host function call", want: `let x = between(now, now, now)`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Call[bool]("between", build.Field(&in.Now), build.Field(&in.Now), build.Field(&in.Now)))
			return 0
		}},
		{name: "list of expressions", want: `let x = [actor.name, "root"] any in actor.roles`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.AnyIn(build.List(build.Field(&in.Actor.Name), build.Lit("root")), build.Field(&in.Actor.Roles)))
			return 0
		}},

		{name: "zero Expr", err: "an operand is the zero build.Expr", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Field(&in.Actor.Name).Eq(build.Expr[string]{})))
		}},
		{name: "zero Expr as a let", err: "an operand is the zero build.Expr", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Expr[bool]{}))
		}},
		{name: "zero Expr alone in a chain", err: "an operand is the zero build.Expr", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.And(build.Expr[bool]{})))
		}},
		{name: "zero Expr first in a chain", err: "an operand is the zero build.Expr", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Or(build.Expr[bool]{}, build.Lit(true))))
		}},
		{name: "no operands", err: "needs at least one operand", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.And()))
		}},
		{name: "host function name", err: `host function "lower case" isn't an identifier`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Call[bool]("lower case")))
		}},
		{name: "host function named like a keyword", err: `host function "when" is a keyword`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Call[bool]("when")))
		}},
		{name: "nil argument", err: "an operand is the zero build.Expr", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Call[bool]("lower", nil)))
		}},
		{name: "typed caller with a zero argument", err: "an operand is the zero build.Expr", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			f := build.Func3[string, string, string, bool]("between")
			return line(build.Let(m, "x", f(build.Lit("a"), build.Lit("b"), build.Expr[string]{})))
		}},
		{name: "two-argument caller", err: "an operand is the zero build.Expr", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			f := build.Func2[string, string, bool]("between")
			return line(build.Let(m, "x", f(build.Lit("a"), build.Expr[string]{})))
		}},
		{name: "invalid regular expression", err: "invalid regular expression", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Field(&in.Actor.Name).Matches("(")))
		}},
	})
}
