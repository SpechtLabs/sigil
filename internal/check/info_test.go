package check_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

// scoped is a policy with a scope of every sort: the document's, a
// `when` body with a let, an assert, a quantifier and a filter.
const scoped = `policy p: Test@1

param min_soak: duration = 1h

let cleared = true

assert("ok", all d in outcome: d != deny)

when cleared {
  let owners = filter o in service.owners: o != ""
  review(reason: r, approvers: owners)
}

when any c in actor.teams: c == "x" {
  deny(reason: x)
}
`

// TestScopeAt looks up names in the scope at a position: at, the text the
// position follows, which scoped holds once. Each name in visible must be
// in scope, declared where decl says (the text its declaration starts
// with, or "" for a name the document doesn't declare), and each in
// hidden must not be.
func TestScopeAt(t *testing.T) {
	tests := []struct {
		at      string
		visible map[string]string // name to the text its declaration starts with
		hidden  []string
		assert  bool
	}{
		{at: "policy p: Test@1\n", visible: map[string]string{"cleared": "cleared = true", "min_soak": "min_soak: duration", "service": ""}, hidden: []string{"owners", "o", "c", "d"}},
		{at: "all d in outcome: d", visible: map[string]string{"d": "d in outcome", "cleared": "cleared = true"}, assert: true},
		{at: "when cleared {\n", visible: map[string]string{"owners": "owners = filter"}, hidden: []string{"o", "c"}},
		{at: "o != ", visible: map[string]string{"o": "o in service", "owners": "owners = filter"}},
		{at: "any c in actor.teams: c", visible: map[string]string{"c": "c in actor"}, hidden: []string{"owners"}},
		{at: "deny(reason: x)\n}\n", visible: map[string]string{"cleared": "cleared = true"}, hidden: []string{"c", "owners"}},
	}
	c := withScopes(t, scoped)
	src := scoped
	for _, tt := range tests {
		t.Run(tt.at, func(t *testing.T) {
			at := tt.at
			if strings.Count(src, at) != 1 {
				t.Fatalf("the policy holds %q %d times", at, strings.Count(src, at))
			}
			env := c.Info().ScopeAt(strings.Index(src, at) + len(at))
			if env == nil {
				t.Fatal("no scope")
			}
			if env.InAssert != tt.assert {
				t.Errorf("InAssert = %v, want %v", env.InAssert, tt.assert)
			}
			for name, decl := range tt.visible {
				b, ok := env.Lookup(name)
				if !ok {
					t.Errorf("%s isn't in scope", name)
					continue
				}
				switch {
				case decl == "" && b.Decl != nil:
					t.Errorf("%s is declared at %s, want nowhere", name, b.Decl.Pos())
				case decl != "" && (b.Decl == nil || b.Decl.Pos().Offset != strings.Index(src, decl)):
					t.Errorf("%s is declared at %v, want at %q", name, b.Decl, decl)
				}
			}
			for _, name := range tt.hidden {
				if _, ok := env.Lookup(name); ok {
					t.Errorf("%s is in scope", name)
				}
			}
		})
	}
}

// TestScopeAtEdges looks up the scope where no document is, past every
// scope, and in an Info that isn't there.
func TestScopeAtEdges(t *testing.T) {
	src := "policy p: Test@1\n\nlet a = 1\n"
	c := withScopes(t, src)
	if env := c.Info().ScopeAt(len(src) + 10); env == nil {
		t.Error("past the end, there's no scope; want the document's")
	} else if _, ok := env.Lookup("a"); !ok {
		t.Error("past the end, a isn't in scope")
	}
	if env := (&check.Info{Scopes: []check.Scope{{From: c.Info().Scopes[0].To}}}).ScopeAt(0); env != nil {
		t.Error("before every scope, there's one")
	}
	var none *check.Info
	if env := none.ScopeAt(0); env != nil {
		t.Error("a nil Info has a scope")
	}
}

// TestImportDecls checks where imported names are declared: a whole
// import at its alias or last segment, and a selective one at its alias
// or name.
func TestImportDecls(t *testing.T) {
	src := "policy p: Test@1\n\nuse deploy.common as shared\nuse deploy.lets.{cleared as ok, owners}\nuse deploy.guard\n"
	c := check.New("p.sigil")
	c.Scopes = true
	c.Resolver = func(name string) (*check.Exported, bool) {
		switch name {
		case "deploy.common":
			return &check.Exported{Name: name, Module: true}, true
		case "deploy.lets":
			return &check.Exported{Name: name, Module: true, Lets: map[string]types.Type{"cleared": types.Bool, "owners": types.Bool}}, true
		case "deploy.guard":
			return &check.Exported{Name: name}, true
		}
		return nil, false
	}
	f, errs := parser.ParseFile("p.sigil", []byte(src))
	if errs != nil {
		t.Fatal(errs)
	}
	c.Policy(f.Docs[0].(*ast.PolicyDoc), loadKind(t))
	env := c.Info().ScopeAt(len(src))
	for name, decl := range map[string]string{"shared": "shared", "ok": "ok,", "owners": "owners}", "guard": "guard\n"} {
		b, ok := env.Lookup(name)
		if !ok || b.Decl == nil || b.Decl.Pos().Offset != strings.Index(src, decl) {
			t.Errorf("%s is declared at %v, want at %q", name, b.Decl, decl)
		}
	}
}

// TestScopesAreOptIn checks that a checker records scopes only when
// asked, so a compile keeps no scope alive.
func TestScopesAreOptIn(t *testing.T) {
	c, _ := checkDoc(t, scoped)
	if len(c.Info().Scopes) != 0 || c.Info().ScopeAt(len(scoped)) != nil {
		t.Errorf("a checker without Scopes recorded %d scopes", len(c.Info().Scopes))
	}
}

// withScopes checks src, one policy of the test kind, with scopes
// recorded.
func withScopes(t *testing.T, src string) *check.Checker {
	t.Helper()
	f, errs := parser.ParseFile("p.sigil", []byte(src))
	if errs != nil {
		t.Fatal(errs)
	}
	c := check.New("p.sigil")
	c.Scopes = true
	c.Policy(f.Docs[0].(*ast.PolicyDoc), loadKind(t))
	return c
}
