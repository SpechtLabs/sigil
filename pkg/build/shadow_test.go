package build_test

import (
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/build"
)

func TestField(t *testing.T) {
	var outside Commit
	var leaked *string
	runModules(t, []moduleCase{
		{name: "input", want: "let x = now", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Field(&in.Now))
			return 0
		}},
		{name: "nested field", want: "let x = actor.name", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Field(&in.Actor.Name))
			return 0
		}},
		{name: "struct and its first field", want: "let x = service.name\nlet y = service.labels", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Field(&in.Service.Name))
			build.Let(m, "y", build.Field(&in.Service.Labels))
			return 0
		}},
		{name: "optional struct itself", want: "let x = present change", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Present(build.Field(&in.Change)))
			return 0
		}},
		{name: "optional scalar", want: `let x = ticket ?? "none"`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Coalesce(build.Field(&in.Ticket), build.Lit("none")))
			return 0
		}},
		{name: "field of an optional struct", want: "let x = change?.window ?? 1h", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Coalesce(build.Opt(&in.Change.Window), build.Lit(time.Hour)))
			return 0
		}},
		{name: "chain after an optional", want: "let x = change?.base.author ?? \"\"", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Coalesce(build.Opt(&in.Change.Base.Author), build.Lit("")))
			return 0
		}},
		{name: "two optionals", want: "let x = present change?.approver?.level", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Present(build.Opt(&in.Change.Approver.Level)))
			return 0
		}},
		{name: "optional pointer field", want: "let x = present change?.approver", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Present(build.OptPtr(&in.Change.Approver)))
			return 0
		}},
		{name: "struct through an optional", want: "let x = (change?.base ?? commits[0]).signed", build: func(m *build.ModuleDoc[Request], in *Request) int {
			base := build.Coalesce(build.Opt(&in.Change.Base), build.Index(build.Field(&in.Commits), build.Lit(0)))
			build.Let(m, "x", build.Sel(base, func(c *Commit) *bool { return &c.Signed }))
			return 0
		}},
		{name: "struct value of a map", want: "let x = owners[\"a\"].roles", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Sel(build.Get(build.Field(&in.Owners), build.Lit("a")), func(p *Person) *[]string { return &p.Roles }))
			return 0
		}},
		{name: "Sel of the argument itself", want: "let x = commits[0]", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Sel(build.Index(build.Field(&in.Commits), build.Lit(0)), func(c *Commit) *Commit { return c }))
			return 0
		}},
		{name: "input in a quantifier", want: `let x = any r in actor.roles: r == actor.name`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Any("r", build.Field(&in.Actor.Roles), func(r *string) build.Expr[bool] { return build.Field(r).Eq(build.Field(&in.Actor.Name)) }))
			return 0
		}},
		{name: "quantifier variable", want: `let x = any r in actor.roles: r == "a"`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Any("r", build.Field(&in.Actor.Roles), func(r *string) build.Expr[bool] { return build.Field(r).Eq(build.Lit("a")) }))
			return 0
		}},
		{name: "nested variables", want: "let x = all c in commits: ((any d in commits: d.at < c.at) or c.author == actor.name)", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.All("c", build.Field(&in.Commits), func(c *Commit) build.Expr[bool] {
				return build.Or(
					build.Any("d", build.Field(&in.Commits), func(d *Commit) build.Expr[bool] { return build.Field(&d.At).Lt(build.Field(&c.At)) }),
					build.Field(&c.Author).Eq(build.Field(&in.Actor.Name)),
				)
			}))
			return 0
		}},
		{name: "filter", want: "let x = (filter c in commits: c.signed) all in commits", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.AllIn(build.Filter("c", build.Field(&in.Commits), func(c *Commit) build.Expr[bool] { return build.Field(&c.Signed) }), build.Field(&in.Commits)))
			return 0
		}},
		{name: "variable over optional structs", want: "let x = any r in reviews: r?.signed ?? false", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "x", build.Any("r", build.Field(&in.Reviews), func(r **Commit) build.Expr[bool] {
				return build.Coalesce(build.Opt(&(*r).Signed), build.Lit(false))
			}))
			return 0
		}},

		{name: "nil pointer", err: "the pointer is nil", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Field[string](nil)))
		}},
		{name: "not a field", err: "the *build_test.Commit isn't a field of the input", build: func(m *build.ModuleDoc[Request], _ *Request) int {
			return line(build.Let(m, "x", build.Field(&outside)))
		}},
		{name: "untagged field", err: "field Request.Note has no `policy` tag", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Field(&in.Note)))
		}},
		{name: "field below an untagged field", err: "field Request.Meta has no `policy` tag", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Field(&in.Meta.Source)))
		}},
		{name: "untagged field of a tagged one", err: "field Person.Nick has no `policy` tag", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Field(&in.Actor.Nick)))
		}},
		{name: "untagged field of an optional", err: "field Change.Note has no `policy` tag", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Opt(&in.Change.Note)))
		}},
		{name: "not a field, in a quantifier", err: "the *build_test.Commit isn't a field of the input", build: func(m *build.ModuleDoc[Request], in *Request) int {
			var at int
			build.Let(m, "x", build.Any("r", build.Field(&in.Actor.Roles), func(*string) build.Expr[bool] {
				x := build.Field(&outside)
				at = line() - 1
				return x.Eq(x)
			}))
			return at
		}},
		{name: "whole input", err: "the input as a whole isn't a value", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Field(in)))
		}},
		{name: "Field through an optional", err: "change?.window reaches through an optional struct; read it with build.Opt", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Field(&in.Change.Window)))
		}},
		{name: "Opt without an optional", err: "actor.name reaches through no optional struct; read it with build.Field", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Opt(&in.Actor.Name)))
		}},
		{name: "OptPtr without an optional", err: "change reaches through no optional struct", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.OptPtr(&in.Change)))
		}},
		{name: "variable outside its body", err: "isn't a field of the input or of a variable in scope", build: func(m *build.ModuleDoc[Request], in *Request) int {
			build.Let(m, "y", build.Any("r", build.Field(&in.Actor.Roles), func(r *string) build.Expr[bool] {
				leaked = r
				return build.Lit(true)
			}))
			return line(build.Let(m, "x", build.Field(leaked)))
		}},
		{name: "Sel with a nil function", err: "the field function is nil", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Sel[Person, string](build.Get(build.Field(&in.Owners), build.Lit("a")), nil)))
		}},
		{name: "Sel returning nil", err: "the field function returned nil", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Sel(build.Get(build.Field(&in.Owners), build.Lit("a")), func(*Person) *string { return nil })))
		}},
		{name: "Sel returning another field", err: "the field function must return the address of a tagged field of its argument, of type *string", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Sel(build.Get(build.Field(&in.Owners), build.Lit("a")), func(*Person) *string { return &in.Actor.Name })))
		}},
		{name: "Sel through an optional", err: "the field reaches through an optional struct, which build.Sel can't read", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Sel(build.Field(&in.Change), func(c **Change) *string { return &(*c).ID })))
		}},
		{name: "variable name", err: `variable "a b" isn't an identifier`, build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.Any("a b", build.Field(&in.Actor.Roles), func(*string) build.Expr[bool] { return build.Lit(true) })))
		}},
		{name: "nil body", err: "the body function is nil", build: func(m *build.ModuleDoc[Request], in *Request) int {
			return line(build.Let(m, "x", build.All[string]("r", build.Field(&in.Actor.Roles), nil)))
		}},
	})
}
