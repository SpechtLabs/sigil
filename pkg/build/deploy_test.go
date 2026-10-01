package build_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/parser"

	"github.com/spechtlabs/sigil/pkg/build"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// The DeployApproval kind of examples/deploy-gates, copied so the tests
// can rebuild its platform documents without importing the example's
// module.

type Tier string

type Input struct {
	Release     Release `policy:"release"`
	Service     Service `policy:"service"`
	Actor       Actor   `policy:"actor"`
	Environment string  `policy:"environment"`
}

type Release struct {
	Soak   time.Duration `policy:"soak"`
	Hotfix bool          `policy:"hotfix"`
}

type Service struct {
	Name   string            `policy:"name"`
	Tier   Tier              `policy:"tier"`
	Owners []string          `policy:"owners"`
	Labels map[string]string `policy:"labels"`
}

type Actor struct {
	Name    string   `policy:"name"`
	Teams   []string `policy:"teams"`
	Roles   []string `policy:"roles"`
	Regions []string `policy:"regions"`
}

type ReviewData struct {
	Approvers []string `policy:"approvers"`
}

type ApproveData struct {
	Bake time.Duration `policy:"bake,default=1h"`
}

var (
	Deny    = policy.NewDecision[policy.None]("deny", "not_eligible", "soak_too_short", "no_rule_matched")
	Review  = policy.NewDecision[ReviewData]("review", "service_owner")
	Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")

	Deploy = policy.NewKind[Input]("DeployApproval",
		policy.WithVersion(1),
		policy.WithEnum[Tier]("critical", "standard", "internal"),
		policy.WithDecisions(Deny, Review, Approve),
		policy.WithReasonPrecedence(Deny.Reason("not_eligible"), Deny.Reason("soak_too_short"), Deny.Reason("no_rule_matched")),
		policy.WithReasonPrecedence(Approve.Reason("release_manager"), Approve.Reason("payments_sre")),
		policy.WithDefault(Deny.Reason("no_rule_matched")),
		policy.WithFunc("split", strings.Split),
	)
)

// common is examples/deploy-gates/policies/platform/deploy/common.sigil.
func common() *build.ModuleDoc[Input] {
	split := build.Func2[string, string, []string]("split")
	return build.Module("deploy.common", Deploy, func(m *build.ModuleDoc[Input], in *Input) {
		labels := build.Field(&in.Service.Labels)
		build.Pub(m, "owns_service", build.AnyIn(build.Field(&in.Actor.Teams), build.Field(&in.Service.Owners)))
		build.Pub(m, "cleared", build.AllIn(split(build.Get(labels, build.Lit("regions")), build.Lit(",")), build.Field(&in.Actor.Regions)))
		build.Pub(m, "eligible", build.And(
			build.In(build.Lit("deployer"), build.Field(&in.Actor.Roles)),
			build.Field(&in.Environment).Eq(build.Lit("production")),
			build.HasAll(labels, build.Lit(map[string]string{
				"app.kubernetes.io/managed-by":   "argocd",
				"platform.example.com/lifecycle": "ga",
			})),
		))
	})
}

// guardrails is examples/deploy-gates/policies/platform/deploy/guardrails.sigil.
func guardrails(common build.Importable) *build.PolicyDoc[Input] {
	return build.Policy("deploy.guardrails", Deploy, func(p *build.PolicyDoc[Input], in *Input) {
		minSoak := build.Param(p, "min_soak", build.Default(24*time.Hour), build.Min(time.Hour), build.Max(48*time.Hour))
		p.When(build.Not(build.Ref[bool](common, "eligible")), func(b *build.Block) {
			b.Decide(Deny.Reason("not_eligible"))
		})
		p.When(build.And(build.Field(&in.Release.Soak).Lt(minSoak), build.Not(build.Field(&in.Release.Hotfix))), func(b *build.Block) {
			b.Decide(Deny.Reason("soak_too_short"))
		})
	})
}

// production is examples/deploy-gates/policies/platform/deploy/production.sigil.
func production(common build.Importable) *build.PolicyDoc[Input] {
	return build.Policy("deploy.production", Deploy, func(p *build.PolicyDoc[Input], in *Input) {
		approvers := build.Param[[]string](p, "approvers")
		tiers := build.Param(p, "tiers", build.Default([]Tier{"standard", "internal"}))
		p.When(build.Ref[bool](common, "cleared"), func(b *build.Block) {
			b.When(build.And(build.Field(&in.Service.Tier).Eq(build.Lit[Tier]("critical")), build.In(build.Lit("release_manager"), build.Field(&in.Actor.Roles))), func(b *build.Block) {
				b.Decide(Approve.Reason("release_manager"))
			})
			b.When(build.And(build.In(build.Field(&in.Service.Tier), tiers), build.Ref[bool](common, "owns_service")), func(b *build.Block) {
				b.Decide(Review.Reason("service_owner"), build.Arg("approvers", approvers))
			})
		})
	})
}

// TestDeployGates rebuilds the platform documents of the deploy-gates
// example in Go. testdata/deploygates holds copies of them as written by
// hand; the rendered documents must parse to the same trees, and declare
// the same params with the same bounds. Line breaks and the generated
// header may differ.
func TestDeployGates(t *testing.T) {
	c := common()
	for _, d := range []build.Doc{c, guardrails(c), production(c)} {
		t.Run(d.Name(), func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join("testdata", "deploygates", filepath.Base(d.Path())))
			if err != nil {
				t.Fatal(err)
			}
			got, err := d.Source()
			if err != nil {
				t.Fatal(err)
			}
			if g, w := shape(t, got), shape(t, want); g != w {
				t.Errorf("rendered\n%s\nwant\n%s", g, w)
			}
		})
	}
}

// spans matches the source range ast.Dump prints after each node.
var spans = regexp.MustCompile(` \[\d+:\d+-\d+:\d+\]`)

// shape is what a document says, without where it says it: its tree
// without source ranges, and its param lines, whose bounds the tree dump
// leaves out.
func shape(t *testing.T, src []byte) string {
	t.Helper()
	f, errs := parser.ParseFile("", src)
	if errs != nil {
		t.Fatal(errs)
	}
	var params []string
	for line := range strings.SplitSeq(string(src), "\n") {
		if strings.HasPrefix(line, "param ") {
			params = append(params, line)
		}
	}
	return spans.ReplaceAllString(ast.Dump(f), "") + strings.Join(params, "\n")
}
