package build_test

import (
	"math"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/build"
)

type unmapped struct{ X int }

func TestLit(t *testing.T) {
	runModules(t, []moduleCase{
		{name: "string", want: `let x = "a \"quoted\"\n line"`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit("a \"quoted\"\n line"))
			return 0
		}},
		{name: "bool", want: "let x = false", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(false))
			return 0
		}},
		{name: "int", want: "let x = 42", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(42))
			return 0
		}},
		{name: "negative int", want: "let x = -42", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(int64(-42)))
			return 0
		}},
		{name: "minimum int", want: "let x = -9223372036854775807 - 1", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(int64(math.MinInt64)))
			return 0
		}},
		{name: "minimum int as an operand", want: "let x = count == -9223372036854775807 - 1", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Field(&in.Count).Eq(build.Lit(int64(math.MinInt64))))
			return 0
		}},
		{name: "float", want: "let x = 2.0", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(2.0))
			return 0
		}},
		{name: "negative float", want: "let x = -0.25", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(-0.25))
			return 0
		}},
		{name: "negative zero", want: "let x = 0.0", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(math.Copysign(0, -1)))
			return 0
		}},
		{name: "duration", want: "let x = 1h30m5s250ms", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(time.Hour+30*time.Minute+5*time.Second+250*time.Millisecond))
			return 0
		}},
		{name: "a day", want: "let x = 24h", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(24*time.Hour))
			return 0
		}},
		{name: "negative duration", want: "let x = -1m", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(-time.Minute))
			return 0
		}},
		{name: "zero duration", want: "let x = 0s", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(time.Duration(0)))
			return 0
		}},
		{name: "minus a negative literal", want: "let x = - -1m", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Neg(build.Lit(-time.Minute)))
			return 0
		}},
		{name: "enum", want: "let x = admin", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(LevelAdmin))
			return 0
		}},
		{name: "enum value two enums declare", want: "let x = Class.standard", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(ClassStandard))
			return 0
		}},
		{name: "list", want: `let x = ["a", "b"]`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit([]string{"a", "b"}))
			return 0
		}},
		{name: "nil list", want: `let x = [] all in actor.roles`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.AllIn(build.Lit[[]string](nil), build.Field(&in.Actor.Roles)))
			return 0
		}},
		{name: "map sorted by key", want: `let x = {critical: 2, Class.standard: 1}`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(map[Class]int{ClassStandard: 1, ClassCritical: 2}))
			return 0
		}},
		{name: "map of lists", want: `let x = {"a": [1, 2]}`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(map[string][]int{"a": {1, 2}}))
			return 0
		}},
		{name: "long map", want: "let x = {\n  \"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\": 1,\n  \"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\": 2,\n}", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Lit(map[string]int{"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": 1, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb": 2}))
			return 0
		}},
		{name: "raw", want: "let x = actor.name != \"\" and admin == actor.level", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.And(build.Raw[bool](`actor.name != ""`), build.Raw[Level]("admin").Eq(build.Field(&in.Actor.Level))))
			return 0
		}},
		{name: "raw gets parentheses", want: "let x = (1 + 2).author", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			build.Let(m, "x", build.Sel(build.Raw[Commit]("1 + 2"), func(c *Commit) *string { return &c.Author }))
			return 0
		}},

		{name: "Go type without a Sigil type", err: "Go type build_test.unmapped has no Sigil type in kind Access", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(unmapped{})))
		}},
		{name: "timestamp", err: "timestamp has no literal: a timestamp comes from input only", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(time.Time{})))
		}},
		{name: "optional", err: "?string has no literal", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit[*string](nil)))
		}},
		{name: "struct", err: "Commit has no literal: a struct value comes from input only", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(Commit{})))
		}},
		{name: "list of structs", err: "list<Commit> has no literal", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit([]Commit{})))
		}},
		{name: "map keyed by timestamps", err: "map<timestamp, int> has no literal", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(map[time.Time]int{})))
		}},
		{name: "map of structs", err: "map<string, Person> has no literal", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(map[string]Person{})))
		}},
		{name: "not a number", err: "NaN has no literal; a float literal is finite", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(math.NaN())))
		}},
		{name: "infinity", err: "+Inf has no literal", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(math.Inf(1))))
		}},
		{name: "duration below a millisecond", err: "duration 1.5ms has no literal; the smallest unit is a millisecond", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(1500*time.Microsecond)))
		}},
		{name: "enum value the enum lacks", err: `Level has no value "root"; Level declares: read, write, admin`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(Level("root"))))
		}},
		{name: "enum value the enum lacks in a list", err: `Level has no value "root"`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit([]Level{"root"})))
		}},
		{name: "enum value the enum lacks as a key", err: `Class has no value "x"`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(map[Class]int{"x": 1})))
		}},
		{name: "enum value the enum lacks as a value", err: `Level has no value "x"`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Lit(map[string][]Level{"a": {"x"}})))
		}},
		{name: "raw that doesn't parse", err: `"a ==" doesn't parse`, build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Raw[bool]("a ==")))
		}},
	})
}
