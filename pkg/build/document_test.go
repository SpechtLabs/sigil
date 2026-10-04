package build_test

import (
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/pkg/build"
	"github.com/spechtlabs/sigil/pkg/policy"
)

func TestStatements(t *testing.T) {
	other := build.Module("other.m", Access, func(m *build.ModuleDoc[Request], in *Request) {
		build.Pub(m, "fine", build.Lit(true))
		build.Let(m, "hidden", build.Lit(true))
	})
	empty := build.Module("empty.m", Access, nil)
	otherPolicy := build.Policy("other.p", Access, func(p *build.PolicyDoc[Request], in *Request) {
		build.Param[int](p, "n")
		build.Pub(p, "fine", build.Lit(true))
	})
	deploy := build.Module("deploy.m", Deploy, func(m *build.ModuleDoc[Input], in *Input) {
		build.Pub(m, "fine", build.Lit(true))
	})
	var scoped build.Expr[bool]
	var foreignParam build.Expr[int]
	build.Policy("foreign.p", Access, func(p *build.PolicyDoc[Request], in *Request) {
		foreignParam = build.Param[int](p, "n")
	})
	var foreignLet build.Expr[bool]
	build.Module("foreign.m", Access, func(m *build.ModuleDoc[Request], in *Request) {
		foreignLet = build.Let(m, "y", build.Lit(true))
	})
	badExtern := build.Extern("bad name")

	runPolicies(t, []policyCase{
		{name: "params", want: "param a: int\nparam b: list<Level> = [read], min: [read]\nparam c: float, max: 2.5", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Param[int](p, "a")
			build.Param(p, "b", build.Default([]Level{LevelRead}), build.Min([]Level{LevelRead}))
			build.Param(p, "c", build.Max(2.5))
			return 0
		}},
		{name: "rules", want: strings.Join([]string{
			`assert("named", actor.name != "")`,
			"",
			"when true {",
			"  let soon = now - now < 1h",
			"",
			"  when soon {",
			"    refuse(reason: frozen)",
			"  }",
			"",
			`  assert("soon", soon)`,
			"",
			"  // why",
			"  when not soon {}",
			"",
			"  grant(reason: owner, level: read)",
			"}",
		}, "\n"), build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Assert("named", build.Field(&in.Actor.Name).NotEq(build.Lit("")))
			p.When(build.Lit(true), func(b *build.Block) {
				soon := build.Let(b, "soon", build.TimeDiff(build.Field(&in.Now), build.Field(&in.Now)).Lt(build.Lit(time.Hour)))
				b.When(soon, func(b *build.Block) { b.Decide(Refuse.Reason("frozen")) })
				b.Assert("soon", soon)
				b.Comment("why")
				b.When(build.Not(soon), nil)
				b.Decide(Grant.Reason("owner"), build.Arg("level", build.Lit(LevelRead)))
			})
			return 0
		}},
		{name: "long assert and arguments", want: strings.Join([]string{
			`assert("long",`,
			`  actor.name != "` + strings.Repeat("a", 76) + `")`,
			"",
			"grant(",
			"  reason: oncall,",
			"  level: read,",
			`  ttl: 1h2m3s4ms,`,
			"  note: \"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb\",",
			")",
		}, "\n"), build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Assert("long", build.Field(&in.Actor.Name).NotEq(build.Lit(strings.Repeat("a", 76))))
			p.Decide(Grant.Reason("oncall"), build.Arg("level", build.Lit(LevelRead)), build.Arg("ttl", build.Lit(time.Hour+2*time.Minute+3*time.Second+4*time.Millisecond)), build.Arg("note", build.Lit(strings.Repeat("b", 68))))
			return 0
		}},
		{name: "comment with carriage returns", want: "// a\n// pub let injected = true\n// b\nparam n: int", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Comment("a\rpub let injected = true\r\nb")
			build.Param[int](p, "n")
			return 0
		}},
		{name: "comments", want: "// one\n//\n// two\nparam n: int\n\n// last", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Comment("one\n\ntwo  ")
			build.Param[int](p, "n")
			p.Comment("last")
			return 0
		}},
		{name: "selective import", want: "use other.m.{fine}\n\nlet x = fine and fine", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", build.And(build.Ref[bool](other, "fine"), build.Ref[bool](other, "fine")))
			return 0
		}},
		{name: "whole import of an invoked policy", want: "use other.p\n\np(n: 1)\n\nwhen p.fine {\n  p(n: 2)\n}", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Invoke(otherPolicy, build.Arg("n", build.Lit(1)))
			p.When(build.Ref[bool](otherPolicy, "fine"), func(b *build.Block) { b.Invoke(otherPolicy, build.Arg("n", build.Lit(2))) })
			return 0
		}},
		{name: "invoke a handwritten policy", want: "use x.y\n\ny()", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Invoke(build.Extern("x.y"))
			return 0
		}},

		{name: "param on a nil policy", err: "the policy is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Param[int](nil, "n").Eq(build.Lit(1))))
		}},
		{name: "param named like a keyword", err: `"when" is a keyword`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Param[int](p, "when"))
		}},
		{name: "two defaults", err: "param n has a default already", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Param(p, "n", build.Default(1), build.Default(2)))
		}},
		{name: "two minimums", err: "param n has a minimum already", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Param(p, "n", build.Min(1), build.Min(2)))
		}},
		{name: "two maximums", err: "param n has a maximum already", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Param(p, "n", build.Max(1), build.Max(2)))
		}},
		{name: "optional param", err: "param n can't be optional (Go type *int)", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Param[*int](p, "n"))
		}},
		{name: "param of an unmapped type", err: "Go type build_test.unmapped has no Sigil type in kind Access", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Param[unmapped](p, "n"))
		}},
		{name: "param of another policy", err: "param n belongs to foreign.p, not test.p", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", foreignParam.Eq(build.Lit(1)))
			return 0
		}},
		{name: "let of another document", err: "let y belongs to foreign.m, not test.p", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", foreignLet)
			return 0
		}},
		{name: "scoped let outside its body", err: "let s is read outside the `when` body it's declared in", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.When(build.Lit(true), func(b *build.Block) {
				scoped = build.Let(b, "s", build.Lit(true))
			})
			p.When(scoped, nil)
			return 0
		}},
		{name: "let in a nil scope", err: "the scope is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "y", build.Let[bool](nil, "x", build.Lit(true))))
		}},
		{name: "let in a nil block", err: "the scope is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			var b *build.Block
			return line(build.Let(p, "y", build.Let(b, "x", build.Lit(true))))
		}},
		{name: "let in a nil module", err: "the scope is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			var nilModule *build.ModuleDoc[Request]
			return line(build.Let(p, "y", build.Let(nilModule, "x", build.Lit(true))))
		}},
		{name: "param on a nil policy pointer", err: "the policy is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			var nilPolicy *build.PolicyDoc[Request]
			return line(build.Let(p, "x", build.Param[int](nilPolicy, "n").Eq(build.Lit(1))))
		}},
		{name: "pub let in a nil policy", err: "the scope is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			var nilPolicy *build.PolicyDoc[Request]
			return line(build.Let(p, "y", build.Pub(nilPolicy, "x", build.Lit(true))))
		}},
		{name: "let named like a keyword", err: `"filter" is a keyword`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "filter", build.Lit(true)))
		}},
		{name: "let name", err: `"1x" isn't an identifier`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "1x", build.Lit(true)))
		}},
		{name: "empty let name", err: `"" isn't an identifier`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "", build.Lit(true)))
		}},
		{name: "name declared twice", err: "x is already declared at document_test.go:", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", build.Lit(true))
			return line(build.Param[int](p, "x"))
		}},
		{name: "zero outcome", err: "the outcome is the zero policy.Outcome", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Decide(policy.Outcome{})
			return line() - 1
		}},
		{name: "reason as an argument", err: "the reason comes from the policy.Outcome", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Decide(Refuse.Reason("frozen"), build.Arg("reason", build.Lit("x")))
			return 0
		}},
		{name: "argument given twice", err: "argument level is given twice", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Decide(Grant.Reason("owner"), build.Arg("level", build.Lit(LevelRead)), build.Arg("level", build.Lit(LevelRead)))
			return 0
		}},
		{name: "argument name", err: `argument name "a-b" isn't an identifier`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Invoke(build.Extern("x.y"), build.Arg("a-b", build.Lit(1)))
			return 0
		}},
		{name: "invoke nil", err: "the policy to invoke is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			var nilPolicy *build.PolicyDoc[Request]
			p.Invoke(nilPolicy)
			return 0
		}},
		{name: "invoke a nil interface", err: "the policy to invoke is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Invoke(nil)
			return line() - 1
		}},
		{name: "invoke itself", err: "test.p can't import itself", build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Invoke(p)
			return 0
		}},
		{name: "invoke a misnamed handwritten policy", err: `document name bad name: "bad name" isn't an identifier`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Invoke(badExtern)
			return 0
		}},
		{name: "ref to nil", err: "the document is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Ref[bool](nil, "y")))
		}},
		{name: "ref to a nil document", err: "the document is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			var nilModule *build.ModuleDoc[Request]
			return line(build.Let(p, "x", build.Ref[bool](nilModule, "y")))
		}},
		{name: "ref to a misnamed handwritten module", err: `document name bad name`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", build.Ref[bool](badExtern, "y"))
			return 0
		}},
		{name: "ref to another kind", err: "deploy.m is built for kind DeployApproval, and test.p for kind Access", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Ref[bool](deploy, "fine")))
		}},
		{name: "ref to a private let", err: "other.m has no pub let hidden; it exports: fine", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Ref[bool](other, "hidden")))
		}},
		{name: "ref to a document without pub lets", err: "empty.m has no pub let x; it exports no pub lets", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Ref[bool](empty, "x")))
		}},
		{name: "ref name", err: `pub let "a.b" isn't an identifier`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Ref[bool](build.Extern("x.y"), "a.b")))
		}},
		{name: "imports that collide", err: "importing fine from other.p collides with fine imported from other.m", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", build.Ref[bool](other, "fine"))
			return line(build.Let(p, "y", build.Ref[bool](otherPolicy, "fine")))
		}},
		{name: "import that takes a let's name", err: "importing fine from other.m collides with the let or param fine declared at document_test.go", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "fine", build.Lit(true))
			return line(build.Let(p, "y", build.Ref[bool](other, "fine")))
		}},
	})
}

func TestDocuments(t *testing.T) {
	tests := []struct {
		doc  func() build.Doc
		name string
		path string
		want string // the rendered source, or part of the error
		err  bool
	}{
		{
			name: "default header and path",
			doc:  func() build.Doc { return build.Module("a.b.c", Access, nil) },
			path: "a/b/c.sigil",
			want: "// Code generated by pkg/build from document_test.go. DO NOT EDIT.\n\nmodule a.b.c: Access@2\n",
		},
		{
			name: "header and path set",
			doc: func() build.Doc {
				return build.Policy("a.b", Access, nil, build.WithPath("teams/a/b.sigil"), build.WithHeader("Generated.\n\nEdit vocabulary.go."), nil)
			},
			path: "teams/a/b.sigil",
			want: "// Generated.\n//\n// Edit vocabulary.go.\n\npolicy a.b: Access@2\n",
		},
		{
			name: "no header",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithHeader("")) },
			path: "a.sigil",
			want: "module a: Access@2\n",
		},
		{
			name: "trailing comment",
			doc: func() build.Doc {
				return build.Module("a", Access, func(m *build.ModuleDoc[Request], in *Request) {
					build.Let(m, "x", build.Lit(1))
					m.Comment("done")
				}, build.WithHeader(""))
			},
			path: "a.sigil",
			want: "module a: Access@2\n\nlet x = 1\n\n// done\n",
		},
		{
			name: "path that isn't relative",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithPath("/abs.sigil")) },
			path: "a.sigil",
			want: `"/abs.sigil" isn't a path for a policy file`,
			err:  true,
		},
		{
			name: "path without the extension",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithPath("a.txt")) },
			path: "a.sigil",
			want: `"a.txt" isn't a path for a policy file`,
			err:  true,
		},
		{
			name: "nil kind",
			doc:  func() build.Doc { return build.Module[Request]("a", nil, nil) },
			path: "a.sigil",
			want: "build.Module: the kind is nil",
			err:  true,
		},
		{
			name: "empty name",
			doc:  func() build.Doc { return build.Module("", Access, nil) },
			path: ".sigil",
			want: "build.Module: the document name is empty",
			err:  true,
		},
		{
			name: "name part that's a keyword",
			doc:  func() build.Doc { return build.Policy("a.when", Access, nil) },
			path: "a/when.sigil",
			want: `build.Policy: document name a.when: "when" is a keyword`,
			err:  true,
		},
		{
			name: "path in a directory Load skips",
			doc: func() build.Doc {
				return build.Module("a", Access, nil, build.WithPath(".platform/deploy/freeze.sigil"))
			},
			path: "a.sigil",
			want: `".platform/deploy/freeze.sigil" has an element that starts with ` + "`.`" + `, which policy.Kind.Load skips`,
			err:  true,
		},
		{
			name: "file Load skips",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithPath("deploy/..data.sigil")) },
			path: "a.sigil",
			want: "which policy.Kind.Load skips",
			err:  true,
		},
		{
			name: "header with carriage returns",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithHeader("x\rpub let y = true\r\nz")) },
			path: "a.sigil",
			want: "// x\n// pub let y = true\n// z\n\nmodule a: Access@2\n",
		},
		{
			name: "older pin",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithHeader(""), build.WithPin(1)) },
			path: "a.sigil",
			want: "module a: Access@1\n",
		},
		{
			name: "pin above the current version",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithPin(3)) },
			path: "a.sigil",
			want: "build.WithPin: kind Access accepts pins from version 1 to 2, not 3",
			err:  true,
		},
		{
			name: "pin below the oldest accepted version",
			doc:  func() build.Doc { return build.Module("a", Access, nil, build.WithPin(0)) },
			path: "a.sigil",
			want: "accepts pins from version 1 to 2, not 0",
			err:  true,
		},
		{
			name: "pin without a kind",
			doc:  func() build.Doc { return build.Module[Request]("a", nil, nil, build.WithPin(1)) },
			path: "a.sigil",
			want: "build.Module: the kind is nil",
			err:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := tt.doc()
			if d.Path() != tt.path {
				t.Errorf("Path() = %q, want %q", d.Path(), tt.path)
			}
			src, err := d.Source()
			switch {
			case tt.err && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Errorf("error %v, want one containing %q", err, tt.want)
			case !tt.err && err != nil:
				t.Errorf("unexpected error: %v", err)
			case !tt.err && string(src) != tt.want:
				t.Errorf("got\n%s\nwant\n%s", src, tt.want)
			}
		})
	}
}

// TestErrorOrder checks that a document's errors are sorted by Go file
// and line, whether a builder call found them or rendering did.
func TestErrorOrder(t *testing.T) {
	var lines []int
	m := build.Module("a", Access, func(m *build.ModuleDoc[Request], in *Request) {
		lines = append(lines, line(build.Let(m, "x", build.Lit(time.Time{}))))
		lines = append(lines, line(build.Let(m, "when", build.Lit(1))))
	})
	_, err := m.Source()
	errs, ok := err.(build.Errors)
	if !ok || len(errs) != 2 {
		t.Fatalf("got %v, want two build.Errors", err)
	}
	for i, e := range errs {
		if e.Line != lines[i] {
			t.Errorf("error %d is at line %d, want %d: %v", i, e.Line, lines[i], e)
		}
	}
	if want := "document_test.go:"; !strings.HasPrefix(errs.Error(), want) || strings.Count(errs.Error(), "\n") != 1 {
		t.Errorf("Errors.Error() = %q, want two lines starting with %q", errs.Error(), want)
	}
}

// TestCallNames checks that an error names the builder call the way Go
// names it: a function by its package, a method by its receiver.
func TestCallNames(t *testing.T) {
	var zero build.Expr[bool]
	tests := []struct {
		build func(p *build.PolicyDoc[Request], in *Request)
		call  string
	}{
		{call: "build.Field", build: func(p *build.PolicyDoc[Request], in *Request) { build.Let(p, "x", build.Field(&in.Note)) }},
		{call: "Expr.Matches", build: func(p *build.PolicyDoc[Request], in *Request) {
			build.Let(p, "x", build.Field(&in.Actor.Name).Matches("("))
		}},
		{call: "(*PolicyDoc).When", build: func(p *build.PolicyDoc[Request], in *Request) { p.When(zero, nil) }},
		{call: "(*PolicyDoc).Assert", build: func(p *build.PolicyDoc[Request], in *Request) { p.Assert("a", zero) }},
		{call: "(*PolicyDoc).Decide", build: func(p *build.PolicyDoc[Request], in *Request) { p.Decide(policy.Outcome{}) }},
		{call: "(*PolicyDoc).Invoke", build: func(p *build.PolicyDoc[Request], in *Request) { p.Invoke(nil) }},
		{call: "(*Block).When", build: func(p *build.PolicyDoc[Request], in *Request) {
			p.When(build.Lit(true), func(b *build.Block) { b.When(zero, nil) })
		}},
		{call: "(*Block).Assert", build: func(p *build.PolicyDoc[Request], in *Request) {
			p.When(build.Lit(true), func(b *build.Block) { b.Assert("a", zero) })
		}},
		{call: "(*Block).Decide", build: func(p *build.PolicyDoc[Request], in *Request) {
			p.When(build.Lit(true), func(b *build.Block) { b.Decide(policy.Outcome{}) })
		}},
		{call: "(*Block).Invoke", build: func(p *build.PolicyDoc[Request], in *Request) {
			p.When(build.Lit(true), func(b *build.Block) { b.Invoke(nil) })
		}},
	}
	for _, tt := range tests {
		t.Run(tt.call, func(t *testing.T) {
			_, err := build.Policy("test.p", Access, tt.build).Source()
			errs, ok := err.(build.Errors)
			if !ok || len(errs) != 1 {
				t.Fatalf("got %v, want one error", err)
			}
			if errs[0].Call != tt.call {
				t.Errorf("Call = %q, want %q", errs[0].Call, tt.call)
			}
		})
	}
}

func TestAs(t *testing.T) {
	platform := build.Policy("platform.guardrails", Access, func(p *build.PolicyDoc[Request], in *Request) {
		build.Pub(p, "fine", build.Lit(true))
	})
	payments := build.Extern("payments.guardrails")
	common := build.Module("access.vocab", Access, func(m *build.ModuleDoc[Request], in *Request) {
		build.Pub(m, "fine", build.Lit(true))
	})
	runPolicies(t, []policyCase{
		{name: "two policies named alike", want: strings.Join([]string{
			"use payments.guardrails as payments_guardrails",
			"use platform.guardrails",
			"",
			"guardrails()",
			"",
			"payments_guardrails(min: 1)",
			"",
			"when guardrails.fine and payments_guardrails.fine {}",
		}, "\n"), build: func(p *build.PolicyDoc[Request], in *Request) int {
			pay := build.As(payments, "payments_guardrails")
			p.Invoke(platform)
			p.Invoke(pay, build.Arg("min", build.Lit(1)))
			p.When(build.And(build.Ref[bool](platform, "fine"), build.Ref[bool](pay, "fine")), nil)
			return 0
		}},
		{name: "module under another name", want: "use access.vocab as v\n\nlet x = v.fine", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", build.Ref[bool](build.As(common, "v"), "fine"))
			return 0
		}},
		{name: "an alias of an alias", want: "use access.vocab as w\n\nlet x = w.fine", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", build.Ref[bool](build.As(build.As(common, "v"), "w"), "fine"))
			return 0
		}},

		{name: "imported under two names", err: "access.vocab is imported as vocab here and as v at document_test.go", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "x", build.Ref[bool](build.As(common, "v"), "fine"))
			return line(build.Let(p, "y", build.Ref[bool](common, "fine")))
		}},
		{name: "alias that isn't a name", err: `alias "a.b" isn't an identifier`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Ref[bool](build.As(common, "a.b"), "fine")))
		}},
		{name: "alias of nothing", err: "the document is nil", build: func(p *build.PolicyDoc[Request], in *Request) int {
			return line(build.Let(p, "x", build.Ref[bool](build.As(nil, "v"), "fine")))
		}},
		{name: "alias of a misnamed document", err: `document name a b`, build: func(p *build.PolicyDoc[Request], in *Request) int {
			p.Invoke(build.As(build.Extern("a b"), "v"))
			return 0
		}},
		{name: "alias that collides", err: "importing fine from access.vocab collides with the let or param fine", build: func(p *build.PolicyDoc[Request], in *Request) int {
			build.Let(p, "fine", build.Lit(true))
			return line(build.Let(p, "x", build.Ref[bool](build.As(common, "fine"), "fine")))
		}},
	})
}
