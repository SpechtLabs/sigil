package check_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/parser"
)

// checkBundle checks each source in order against the test kind, each
// one able to import the ones before it, and returns the last checker.
func checkBundle(t *testing.T, srcs ...string) *check.Checker {
	t.Helper()
	k := loadKind(t)
	exports := map[string]*check.Exported{}
	resolve := func(name string) (*check.Exported, bool) {
		e, ok := exports[name]
		return e, ok
	}
	var c *check.Checker
	for i, src := range srcs {
		f, perrs := parser.ParseFile("p.sigil", []byte(src))
		if perrs != nil {
			t.Fatalf("parse: %v", perrs)
		}
		c = check.New("p.sigil")
		c.Resolver = resolve
		switch d := f.Docs[0].(type) {
		case *ast.PolicyDoc:
			c.Policy(d, k)
		case *ast.ModuleDoc:
			c.Module(d, k)
		}
		if i < len(srcs)-1 {
			if errs := c.Errors(); errs != nil {
				t.Fatalf("document %d: %v", i, errs)
			}
			exports[c.Exported().Name] = c.Exported()
		}
	}
	return c
}

const (
	commonModule = `module deploy.common: Test@1
pub let owns_service = actor.teams any in service.owners
pub let cleared = split(service.labels["regions"], ",") all in actor.regions
let restricted = service.labels has "restricted"
pub let open = not restricted
pub let names = ["a"]`
	guardrailsPolicy = `policy deploy.guardrails: Test@1
use deploy.common.{cleared}
param min_soak: duration = 24h, min: 1h, max: 48h
pub let soaked = release.soak >= 1h
let short = release.soak < min_soak
when short and cleared { deny(reason: soak_too_short) }`
	productionPolicy = `policy deploy.production: Test@1
use deploy.common.{cleared, owns_service}
param approvers: list<string>
param tiers: list<Tier> = [standard, internal]
when cleared and service.tier in tiers and owns_service {
  review(reason: service_owner, approvers: approvers)
}`
)

// TestImports covers `use` in every form, reading imported lets, and
// invoking imported policies, with the platform documents checked first.
func TestImports(t *testing.T) {
	tests := []struct {
		name string
		src  string
		errs []string
		help string
	}{
		{name: "selective import", src: "policy p: Test@1\nuse deploy.common.{cleared, owns_service as owner}\nwhen cleared and owner { deny(reason: a) }"},
		{name: "whole module import", src: "policy p: Test@1\nuse deploy.common\nwhen common.cleared and common.open { deny(reason: a) }"},
		{name: "aliased whole import", src: "policy p: Test@1\nuse deploy.common as c\nwhen c.cleared { deny(reason: a) }"},
		{name: "invocation at the top level", src: "policy p: Test@1\nuse deploy.guardrails\nguardrails(min_soak: 4h)"},
		{name: "invocation with defaults only", src: "policy p: Test@1\nuse deploy.guardrails\nguardrails()"},
		{name: "invocation in a when with a param argument", src: "policy p: Test@1\nuse deploy.production\nparam mine: list<string> = [\"a\"]\nwhen release.hotfix { production(approvers: mine, tiers: [\"x\"]) }", errs: []string{"4:59: expected Tier, found string"}, help: "an enum value is a bare name; Tier declares: critical, standard, internal"},
		{name: "invocation binds an enum param", src: "policy p: Test@1\nuse deploy.production\nparam mine: list<string> = [\"a\"]\nwhen release.hotfix { production(approvers: mine, tiers: [critical, standard]) }"},
		{name: "invocation binds an enum param from a param", src: "policy p: Test@1\nuse deploy.production\nparam mine: list<Tier> = [internal]\nproduction(approvers: [\"a\"], tiers: mine)"},
		{name: "invocation binds a qualified enum value", src: "policy p: Test@1\nuse deploy.production\nproduction(approvers: [\"a\"], tiers: [Tier.critical, standard])"},
		{name: "invocation enum argument misspelled", src: "policy p: Test@1\nuse deploy.production\nproduction(approvers: [\"a\"], tiers: [critcal])", errs: []string{"3:38: Tier has no value `critcal`"}, help: "did you mean `critical`? Tier declares: critical, standard, internal"},
		{name: "invocation aliased", src: "policy p: Test@1\nuse deploy.production as approvals\napprovals(approvers: [\"a\"])"},
		{name: "invocation argument from arithmetic on a param", src: "policy p: Test@1\nuse deploy.guardrails\nparam extra: duration = 1h\nguardrails(min_soak: extra + 2h)"},
		{name: "pub let of a policy", src: "policy p: Test@1\nuse deploy.guardrails.{soaked}\nwhen soaked { deny(reason: a) }"},
		{name: "pub let of a policy through a whole import", src: "policy p: Test@1\nuse deploy.guardrails\nwhen guardrails.soaked { deny(reason: a) }"},
		{name: "module importing a module", src: "module m: Test@1\nuse deploy.common.{cleared}\npub let both = cleared and release.hotfix"},

		{name: "unknown document", src: "policy p: Test@1\nuse deploy.commn\nwhen true { deny(reason: a) }", errs: []string{"2:5: unknown document `deploy.commn`"}},
		{name: "private let", src: "policy p: Test@1\nuse deploy.common.{restricted}\nwhen restricted { deny(reason: a) }", errs: []string{"2:20: let `restricted` of deploy.common is private", "3:6: unknown name `restricted`"}, help: "only `pub let`s can be imported; mark it `pub let restricted = ...` in deploy.common"},
		{name: "unknown let with suggestion", src: "policy p: Test@1\nuse deploy.common.{clearedd}\nwhen true { deny(reason: a) }", errs: []string{"2:20: deploy.common has no pub let `clearedd`"}, help: "did you mean `cleared`? deploy.common exports: cleared, names, open, owns_service"},
		{name: "unknown qualified let", src: "policy p: Test@1\nuse deploy.common\nwhen common.nope { deny(reason: a) }", errs: []string{"3:13: deploy.common has no pub let `nope`"}},
		{name: "import collides with an input", src: "policy p: Test@1\nuse deploy.common.{cleared as actor}\nwhen true { deny(reason: a) }", errs: []string{"2:31: `actor` is already the name of an input"}},
		{name: "import collides with a let", src: "policy p: Test@1\nuse deploy.common.{cleared}\nlet cleared = true\nwhen true { deny(reason: a) }", errs: []string{"3:5: `cleared` is already the name of a let"}},
		{name: "module used as a value", src: "policy p: Test@1\nuse deploy.common\nwhen common { deny(reason: a) }", errs: []string{"3:6: `common` is a module, not a value"}, help: "read one of its pub lets as `common.<let>`"},
		{name: "module invoked", src: "policy p: Test@1\nuse deploy.common\ncommon()", errs: []string{"3:1: `common` is a module and can't be invoked"}},
		{name: "policy used as a value", src: "policy p: Test@1\nuse deploy.guardrails\nwhen guardrails { deny(reason: a) }", errs: []string{"3:6: `guardrails` is an imported policy, not a value"}},
		{name: "optional chaining on an import", src: "policy p: Test@1\nuse deploy.common\nwhen common?.cleared { deny(reason: a) }", errs: []string{"3:14: `common` isn't optional"}},

		{name: "invocation with a positional argument", src: "policy p: Test@1\nuse deploy.guardrails\nguardrails(4h)", errs: []string{"3:12: invocation arguments are named"}},
		{name: "invocation with an unknown param", src: "policy p: Test@1\nuse deploy.guardrails\nguardrails(min_soke: 4h)", errs: []string{"3:12: policy deploy.guardrails has no param `min_soke`"}, help: "did you mean `min_soak`? deploy.guardrails declares: min_soak"},
		{name: "invocation with the wrong type", src: "policy p: Test@1\nuse deploy.guardrails\nguardrails(min_soak: 4)", errs: []string{"3:22: expected duration, found int"}},
		{name: "invocation gives a param twice", src: "policy p: Test@1\nuse deploy.guardrails\nguardrails(min_soak: 4h, min_soak: 5h)", errs: []string{"3:26: param `min_soak` is given twice"}},
		{name: "invocation misses a required param", src: "policy p: Test@1\nuse deploy.production\nproduction()", errs: []string{"3:1: invocation of deploy.production doesn't bind param `approvers`"}, help: "deploy.production declares: approvers, tiers"},
		{name: "invocation argument reads an input", src: "policy p: Test@1\nuse deploy.production\nproduction(approvers: service.owners)", errs: []string{"3:23: invocation argument reads input `service`"}},
		{name: "invocation argument reads a let", src: "policy p: Test@1\nuse deploy.production\nlet mine = [\"a\"]\nproduction(approvers: mine)", errs: []string{"4:23: invocation argument reads let `mine`"}},
		{name: "invocation argument reads an imported let", src: "policy p: Test@1\nuse deploy.production\nuse deploy.common\nproduction(approvers: common.names)", errs: []string{"4:23: invocation argument reads imported let `common.names`"}},
		{name: "invocation argument reads a selectively imported let", src: "policy p: Test@1\nuse deploy.production\nuse deploy.common.{names}\nproduction(approvers: names)", errs: []string{"4:23: invocation argument reads imported let `names`"}},
		{name: "invocation argument calls a function", src: "policy p: Test@1\nuse deploy.production\nproduction(approvers: split(\"a,b\", \",\"))", errs: []string{"3:23: invocation argument calls a host function"}},
		{name: "invocation argument filters", src: "policy p: Test@1\nuse deploy.production\nproduction(approvers: filter a in [\"x\"]: true)", errs: []string{"3:23: invocation argument filters a list"}},
		{name: "invocation in a module is a parse error", src: "module m: Test@1\nuse deploy.guardrails\nguardrails()", errs: []string{"3:1: a module can't contain an invocation"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, perrs := parser.ParseFile("p.sigil", []byte(tt.src))
			if perrs != nil {
				if tt.errs == nil {
					t.Fatalf("parse: %v", perrs)
				}
				got := make([]string, len(perrs))
				for i, e := range perrs {
					got[i] = e.Pos.String() + ": " + e.Msg
				}
				if g, w := strings.Join(got, "\n"), strings.Join(tt.errs, "\n"); g != w {
					t.Errorf("errors:\n%s\nwant:\n%s", g, w)
				}
				return
			}
			c := checkBundle(t, commonModule, guardrailsPolicy, productionPolicy, tt.src)
			errs := c.Errors()
			got := make([]string, len(errs))
			for i, e := range errs {
				got[i] = e.Pos.String() + ": " + e.Msg
			}
			if g, w := strings.Join(got, "\n"), strings.Join(tt.errs, "\n"); g != w {
				t.Errorf("errors:\n%s\nwant:\n%s", g, w)
			}
			if tt.help != "" && (len(errs) == 0 || errs[0].Help != tt.help) {
				var h string
				if len(errs) > 0 {
					h = errs[0].Help
				}
				t.Errorf("help = %q\nwant   %q", h, tt.help)
			}
		})
	}
}

// TestExported checks what a document offers its importers.
func TestExported(t *testing.T) {
	c := checkBundle(t, commonModule, guardrailsPolicy)
	e := c.Exported()
	if e.Name != "deploy.guardrails" || e.Module {
		t.Errorf("Exported = %+v", e)
	}
	if got := strings.Join(e.LetNames(), " "); got != "soaked" {
		t.Errorf("LetNames = %q", got)
	}
	if got := strings.Join(e.Private, " "); got != "short" {
		t.Errorf("Private = %q", got)
	}
	if p := e.Param("min_soak"); p == nil || p.Required || p.Type.String() != "duration" {
		t.Errorf("Param(min_soak) = %+v", p)
	}
	c = checkBundle(t, commonModule)
	if e := c.Exported(); !e.Module || len(e.Lets) != 4 || len(e.Params) != 0 {
		t.Errorf("module Exported = %+v", e)
	}
}
