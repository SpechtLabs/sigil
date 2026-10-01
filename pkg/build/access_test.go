package build_test

import (
	"strings"
	"time"

	"github.com/spechtlabs/sigil/pkg/build"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// The Access kind exercises what the deploy kind doesn't: enums that
// share a value, optional structs and scalars, lists of structs, maps of
// structs and keyed by an enum, timestamps, floats, and host functions
// of one to three arguments.

type Level string

type Plan string

type Class string

const (
	LevelRead  Level = "read"
	LevelWrite Level = "write"
	LevelAdmin Level = "admin"

	PlanStandard Plan = "standard"
	PlanPremium  Plan = "premium"

	ClassStandard Class = "standard"
	ClassCritical Class = "critical"
)

type Request struct {
	Actor   Person            `policy:"actor"`
	Service Svc               `policy:"service"`
	Change  *Change           `policy:"change"`
	Ticket  *string           `policy:"ticket"`
	Commits []Commit          `policy:"commits"`
	Reviews []*Commit         `policy:"reviews"`
	Quotas  map[Class]int     `policy:"quotas"`
	Owners  map[string]Person `policy:"owners"`
	Now     time.Time         `policy:"now"`
	Score   float64           `policy:"score"`
	Count   int64             `policy:"count"`
	Note    string            // not readable by policies
	Meta    Meta              // not readable either, nor anything below it
	_       struct{}
}

type Person struct {
	Name  string   `policy:"name"`
	Roles []string `policy:"roles"`
	Level Level    `policy:"level"`
	Nick  string
}

type Svc struct {
	Name   string            `policy:"name"`
	Class  Class             `policy:"class"`
	Plan   Plan              `policy:"plan"`
	Labels map[string]string `policy:"labels"`
}

type Change struct {
	ID       string        `policy:"id"`
	Window   time.Duration `policy:"window"`
	Approver *Person       `policy:"approver"`
	Base     Commit        `policy:"base"`
	Note     string
}

type Commit struct {
	Author string    `policy:"author"`
	Signed bool      `policy:"signed"`
	At     time.Time `policy:"at"`
}

type Meta struct {
	Source string `policy:"source"`
}

type GrantData struct {
	Level Level         `policy:"level"`
	TTL   time.Duration `policy:"ttl,default=1h"`
}

var (
	Refuse = policy.NewDecision[policy.None]("refuse", "unsigned", "frozen", "no_rule_matched")
	Grant  = policy.NewDecision[GrantData]("grant", "owner", "oncall")

	Access = policy.NewKind[Request]("Access",
		policy.WithVersion(2),
		policy.WithEnum(LevelRead, LevelWrite, LevelAdmin),
		policy.WithEnum(PlanStandard, PlanPremium),
		policy.WithEnum(ClassStandard, ClassCritical),
		policy.WithDecisions(Refuse, Grant),
		policy.WithDefault(Refuse.Reason("no_rule_matched")),
		policy.WithFunc("lower", strings.ToLower),
		policy.WithFunc("size", func(xs []string) int { return len(xs) }),
		policy.WithFunc("between", func(t, from, to time.Time) bool { return !t.Before(from) && t.Before(to) }),
	)
)

// accessCommon is a module of every expression form the builder has.
func accessCommon() *build.ModuleDoc[Request] {
	lower := build.Func1[string, string]("lower")
	between := build.Func3[time.Time, time.Time, time.Time, bool]("between")
	return build.Module("access.common", Access, func(m *build.ModuleDoc[Request], in *Request) {
		roles := build.Field(&in.Actor.Roles)
		m.Comment("Who the actor is.")
		owner := build.Let(m, "owner_name", build.Sel(build.Get(build.Field(&in.Owners), build.Field(&in.Service.Name)), func(p *Person) *string { return &p.Name }))
		build.Pub(m, "owner", owner.Eq(build.Field(&in.Actor.Name)))
		build.Pub(m, "is_admin", build.Field(&in.Actor.Level).Eq(build.Lit(LevelAdmin)))
		build.Pub(m, "senior", build.And(
			build.Call[int]("size", roles).Ge(build.Lit(2)),
			build.NotIn(build.Lit("guest"), roles),
			build.Index(roles, build.Lit(0)).NotEq(build.Lit("intern")),
		))
		build.Pub(m, "sre", build.Filter("r", roles, func(r *string) build.Expr[bool] { return build.Field(r).Like("sre-*") }))
		build.Pub(m, "leads", build.OneIn(build.Lit([]string{"lead", "staff"}), roles))
		build.Pub(m, "duties_split", build.ExclusiveIn(build.List(build.Lit("approver"), build.Field(&in.Actor.Name)), roles))

		m.Comment("The service.\nIts plan and class share the value standard.")
		build.Pub(m, "standard_plan", build.Field(&in.Service.Plan).Eq(build.Lit(PlanStandard)))
		build.Pub(m, "important", build.In(build.Field(&in.Service.Class), build.Lit([]Class{ClassCritical, ClassStandard})))
		build.Pub(m, "managed", build.HasAll(build.Field(&in.Service.Labels), build.Lit(map[string]string{"managed-by": "argocd"})))
		build.Pub(m, "payments", build.Lit("payments").Within(lower(build.Field(&in.Service.Name))))
		build.Pub(m, "has_quota", build.Or(
			build.HasKey(build.Field(&in.Quotas), build.Lit(ClassCritical)),
			build.Get(build.Field(&in.Quotas), build.Field(&in.Service.Class)).Gt(build.Lit(0)),
		))

		m.Comment("The change.")
		build.Pub(m, "signed", build.All("c", build.Field(&in.Commits), func(c *Commit) build.Expr[bool] {
			return build.And(build.Field(&c.Signed), build.Field(&c.Author).NotEq(build.Lit("")))
		}))
		build.Pub(m, "fresh", build.Any("c", build.Field(&in.Commits), func(c *Commit) build.Expr[bool] {
			return build.TimeDiff(build.Field(&in.Now), build.Field(&c.At)).Lt(build.Lit(2 * time.Hour))
		}))
		build.Pub(m, "reviewed", build.Any("r", build.Field(&in.Reviews), func(r **Commit) build.Expr[bool] {
			return build.And(build.Present(build.Field(r)), build.Coalesce(build.Opt(&(*r).Signed), build.Lit(false)))
		}))
		build.Pub(m, "in_window", between(
			build.Field(&in.Now),
			build.Sel(build.Index(build.Field(&in.Commits), build.Lit(int64(0))), func(c *Commit) *time.Time { return &c.At }),
			build.TimeAdd(build.Field(&in.Now), build.Coalesce(build.Opt(&in.Change.Window), build.Lit(30*time.Minute))),
		))
		build.Pub(m, "ticketed", build.And(
			build.Present(build.Field(&in.Ticket)),
			build.Coalesce(build.Field(&in.Ticket), build.Lit("")).Matches(`^CHG-\d+$`),
		))
		build.Pub(m, "approved", build.And(
			build.Present(build.OptPtr(&in.Change.Approver)),
			build.Coalesce(build.Opt(&in.Change.Approver.Name), build.Lit("")).NotEq(build.Field(&in.Actor.Name)),
		))
		build.Pub(m, "base_signed", build.Coalesce(build.Opt(&in.Change.Base.Signed), build.Lit(false)))
		build.Pub(m, "scored", build.Xor(
			build.Field(&in.Score).Add(build.Lit(0.5)).Gt(build.Lit(1.0)),
			build.Neg(build.Field(&in.Score)).Lt(build.Lit(-2.5)),
		))
		build.Pub(m, "counted", build.Field(&in.Count).Sub(build.Lit(int64(1))).Ge(build.Lit(int64(0))))
		build.Pub(m, "named", build.Raw[bool](`actor.name != ""`))
	})
}

// accessGuard is a policy with params, nested rules, scoped lets,
// asserts and decisions with payloads.
func accessGuard(common build.Importable) *build.PolicyDoc[Request] {
	return build.Policy("access.guard", Access, func(p *build.PolicyDoc[Request], in *Request) {
		maxTTL := build.Param(p, "max_ttl", build.Default(8*time.Hour), build.Min(time.Hour), build.Max(24*time.Hour))
		levels := build.Param(p, "levels", build.Default([]Level{LevelRead, LevelWrite}))
		minRoles := build.Param(p, "min_roles", build.Min(1))
		unsigned := build.Pub(p, "unsigned", build.Not(build.Ref[bool](common, "signed")))
		p.Assert("score_range", build.Field(&in.Score).Ge(build.Lit(-10.0)))

		p.When(unsigned, func(b *build.Block) {
			b.Decide(Refuse.Reason("unsigned"))
		})
		p.When(build.And(build.Ref[bool](common, "owner"), build.Ref[bool](common, "signed")), func(b *build.Block) {
			oncall := build.Let(b, "oncall", build.Any("r", build.Field(&in.Actor.Roles), func(r *string) build.Expr[bool] {
				return build.Field(r).Like("oncall-*")
			}))
			b.Comment("Owners get the level they hold.")
			b.When(build.In(build.Field(&in.Actor.Level), levels), func(b *build.Block) {
				b.Decide(Grant.Reason("owner"), build.Arg("level", build.Field(&in.Actor.Level)), build.Arg("ttl", maxTTL))
			})
			b.When(build.And(oncall, build.Call[int]("size", build.Field(&in.Actor.Roles)).Ge(minRoles)), func(b *build.Block) {
				b.Assert("oncall_is_senior", build.Ref[bool](common, "senior"))
				b.Decide(Grant.Reason("oncall"), build.Arg("level", build.Lit(LevelRead)))
			})
		})
		p.Assert("owner_needs_ticket", build.Raw[bool]("grant.owner not in outcome or present ticket"))
	})
}

// accessTeam invokes the guard, reads one of its pub lets through the
// whole import, and uses documents written by hand.
func accessTeam(guard build.Invocable) *build.PolicyDoc[Request] {
	handwritten := build.Extern("access.handwritten")
	return build.Policy("access.team", Access, func(p *build.PolicyDoc[Request], in *Request) {
		ttl := build.Param(p, "ttl", build.Default(4*time.Hour))
		p.Invoke(guard, build.Arg("max_ttl", ttl), build.Arg("min_roles", build.Lit(2)))
		p.When(build.Ref[bool](guard, "unsigned"), func(b *build.Block) {
			b.Decide(Refuse.Reason("frozen"))
		})
		p.When(build.Ref[bool](handwritten, "weekend"), func(b *build.Block) {
			b.Invoke(build.Extern("access.baseline"))
		})
	})
}
