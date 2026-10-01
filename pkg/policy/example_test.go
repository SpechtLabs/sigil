package policy_test

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"
)

// A deploy gate: the host defines the kind in Go, compiles a policy
// against it and evaluates requests to typed decisions.
func Example() {
	type Service struct {
		Name string `policy:"name"`
		Tier string `policy:"tier"`
	}
	type Input struct {
		Service Service  `policy:"service"`
		Teams   []string `policy:"teams"`
	}
	type ApproveData struct {
		Bake time.Duration `policy:"bake,default=1h"`
	}

	deny := policy.NewDecision[policy.None]("deny", "not_owner", "no_rule_matched")
	approve := policy.NewDecision[ApproveData]("approve", "owner")

	deploy := policy.NewKind[Input]("Deploy",
		policy.WithVersion(1),
		policy.WithDecisions(deny, approve), // deny outranks approve
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	p, err := deploy.Compile(`
policy deploy.gate: Deploy@1

when service.name in teams {
  approve(reason: owner)
}

when service.tier == "critical" {
  approve(reason: owner, bake: 4h)
}

when service.name not in teams {
  deny(reason: not_owner)
}
`, "deploy.gate")
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, in := range []Input{
		{Service: Service{Name: "payments", Tier: "standard"}, Teams: []string{"payments"}},
		{Service: Service{Name: "payments", Tier: "standard"}, Teams: []string{"search"}},
	} {
		res, err := p.Eval(context.Background(), in)
		if err != nil {
			fmt.Println(err)
			return
		}
		fmt.Println(res.Decision, res.Reason)
		if a, ok := approve.Match(res); ok {
			fmt.Println("  bake for", a.Bake)
		}
	}
	// Output:
	// approve owner
	//   bake for 1h0m0s
	// deny not_owner
}

// NewKind derives the contract from Go types; Schema writes it out as the
// kind file a policy repository checks in.
func ExampleNewKind() {
	type Actor struct {
		Name  string   `policy:"name"`
		Roles []string `policy:"roles"`
	}
	type Input struct {
		Actor    Actor          `policy:"actor"`
		Resource string         `policy:"resource"`
		Age      time.Duration  `policy:"age"`
		Labels   map[string]int `policy:"labels"`
	}
	type AllowData struct {
		TTL time.Duration `policy:"ttl,default=1h"`
	}

	deny := policy.NewDecision[policy.None]("deny", "banned", "no_rule_matched")
	allow := policy.NewDecision[AllowData]("allow", "admin", "owner")

	access := policy.NewKind[Input]("Access",
		policy.WithVersion(2),
		policy.WithDecisions(deny, allow),
		policy.WithReasonPrecedence(allow.Reason("admin"), allow.Reason("owner")),
		policy.WithDefault(deny.Reason("no_rule_matched")),
		policy.WithFunc("split", strings.Split),
	)
	fmt.Print(access.Schema())
	// Output:
	// kind Access version 2
	//
	// type Actor {
	//   name: string
	//   roles: list<string>
	// }
	//
	// input actor: Actor
	// input resource: string
	// input age: duration
	// input labels: map<string, int>
	//
	// fn split(string, string) -> list<string>
	//
	// decision deny {
	//   reason: banned | no_rule_matched
	// }
	//
	// decision allow {
	//   reason: admin | owner
	//   ttl: duration = 1h
	// }
	//
	// collect one
	// precedence deny > allow
	// precedence allow: admin > owner
	//
	// default deny(reason: no_rule_matched)
}

// Load reads every .sigil file of an fs.FS into one bundle. Documents
// resolve each other by name, so how they are split into files doesn't
// matter.
func ExampleKind_Load() {
	type Input struct {
		User  string   `policy:"user"`
		Teams []string `policy:"teams"`
	}
	deny := policy.NewDecision[policy.None]("deny", "banned", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "member")
	access := policy.NewKind[Input]("Access",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	fsys := policy.MapFS(map[string]string{
		"common.sigil": `
module access.common: Access@1

pub let banned = user in ["mallory"]
`,
		"teams/search.sigil": `
policy access.search: Access@1

use access.common.{banned}

when banned {
  deny(reason: banned)
}

when "search" in teams {
  allow(reason: member)
}
`,
	})

	p, err := access.Load(fsys, "access.search")
	if err != nil {
		fmt.Println(err)
		return
	}
	res, _ := p.Eval(context.Background(), Input{User: "mallory", Teams: []string{"search"}})
	fmt.Println(p.Name(), "->", res.Decision, res.Reason, "at", res.Trace.Candidates[0].Position)
	// Output:
	// access.search -> deny banned at teams/search.sigil:7:3 (access.search)
}

// A failed compile returns a *CompileError. Its message quotes the source
// the way the CLI does, and Diagnostics has every problem as data.
func ExampleCompileError() {
	type Input struct {
		Tier string `policy:"tier"`
	}
	deny := policy.NewDecision[policy.None]("deny", "critical", "no_rule_matched")
	k := policy.NewKind[Input]("Gate",
		policy.WithVersion(1),
		policy.WithDecisions(deny),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	_, err := k.Compile(`policy gate: Gate@1

when teir == "critical" {
  deny(reason: critical)
}
`, "gate")

	if ce, ok := errors.AsType[*policy.CompileError](err); ok {
		fmt.Println(err)
		for _, d := range ce.Diagnostics {
			fmt.Printf("%s: %s\n", d.Position, d.Message)
		}
	}
	// Output:
	// 3:6 (gate): error: unknown name `teir`
	//   |
	// 3 | when teir == "critical" {
	//   |      ^^^^
	//   = help: did you mean `tier`?
	// 3:6 (gate): unknown name `teir`
}

// Params binds the root policy's params from Go, for example from a
// per-team configuration. Values are checked against the param's type and
// bounds when the policy compiles.
func ExampleParams() {
	type Input struct {
		Soak time.Duration `policy:"soak"`
	}
	deny := policy.NewDecision[policy.None]("deny", "soak_too_short", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "soaked")
	k := policy.NewKind[Input]("Soak",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	const src = `policy soak: Soak@1

param min_soak: duration = 24h, min: 1h, max: 48h

when soak >= min_soak {
  allow(reason: soaked)
}

when soak < min_soak {
  deny(reason: soak_too_short)
}
`
	p, err := k.Compile(src, "soak", policy.Params{"min_soak": 4 * time.Hour})
	if err != nil {
		fmt.Println(err)
		return
	}
	res, _ := p.Eval(context.Background(), Input{Soak: 6 * time.Hour})
	fmt.Println(res.Decision, res.Reason)

	_, err = k.Compile(src, "soak", policy.Params{"min_soak": time.Minute})
	if ce, ok := errors.AsType[*policy.CompileError](err); ok {
		fmt.Println(ce.Diagnostics[0].Message)
	}
	// Output:
	// allow soaked
	// param min_soak: 1m is below the minimum 1h
}

// Require makes the root invoke the platform's guardrails unconditionally,
// and From takes them from a source the host trusts, so a team bundle can
// neither wrap them in a `when` nor ship its own copy.
func ExampleRequire() {
	type Input struct {
		Hotfix bool `policy:"hotfix"`
	}
	deny := policy.NewDecision[policy.None]("deny", "hotfix_frozen", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "team")
	k := policy.NewKind[Input]("Deploy",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	platform := policy.MapFS(map[string]string{"guardrails.sigil": `
policy deploy.guardrails: Deploy@1

when hotfix {
  deny(reason: hotfix_frozen)
}
`})

	// The team wraps the guardrails in a condition that never holds.
	team := policy.MapFS(map[string]string{"team.sigil": `
policy team.search: Deploy@1

use deploy.guardrails

when false {
  guardrails()
}

when true {
  allow(reason: team)
}
`})

	_, err := k.Load(team, "team.search",
		policy.Require("deploy.guardrails", policy.From(platform)))
	if ce, ok := errors.AsType[*policy.CompileError](err); ok {
		d := ce.Diagnostics[0]
		fmt.Println(d.Position, d.Message)
		fmt.Println(d.Help)
	}

	// The team ships its own, empty deploy.guardrails and invokes that.
	forged := policy.MapFS(map[string]string{"team.sigil": `
policy deploy.guardrails: Deploy@1
---
policy team.search: Deploy@1

use deploy.guardrails

guardrails()
`})

	_, err = k.Load(forged, "team.search",
		policy.Require("deploy.guardrails", policy.From(platform)))
	if ce, ok := errors.AsType[*policy.CompileError](err); ok {
		fmt.Println(ce.Diagnostics[0].Message)
	}
	// Output:
	// team.sigil:7:3 (team.search) deploy.guardrails must be invoked unconditionally
	// the host requires deploy.guardrails for every Deploy policy; move the call to the top level
	// policy deploy.guardrails is defined twice
}

// Trusted serves the platform's vocabulary to team policies without
// requiring a policy from it: the team imports deploy.freeze, and can't
// ship a deploy.freeze of its own.
func ExampleTrusted() {
	type Input struct {
		Environment string   `policy:"environment"`
		Frozen      []string `policy:"frozen"`
	}
	deny := policy.NewDecision[policy.None]("deny", "change_freeze", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "team")
	k := policy.NewKind[Input]("Deploy",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	vocabulary := policy.MapFS(map[string]string{"deploy/freeze.sigil": `
module deploy.freeze: Deploy@1

pub let is_frozen = environment in frozen
`})

	team := policy.MapFS(map[string]string{"team.sigil": `
policy team.search: Deploy@1

use deploy.freeze.{is_frozen}

when is_frozen {
  deny(reason: change_freeze)
}

when true {
  allow(reason: team)
}
`})

	p, err := k.Load(team, "team.search", policy.Trusted(vocabulary))
	if err != nil {
		fmt.Println(err)
		return
	}
	res, err := p.Eval(context.Background(), Input{Environment: "production", Frozen: []string{"production"}})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println(res.Decision, res.Reason)

	// The team ships its own deploy.freeze, which never freezes.
	forged := policy.MapFS(map[string]string{"team.sigil": `
module deploy.freeze: Deploy@1

pub let is_frozen = false
---
policy team.search: Deploy@1

use deploy.freeze.{is_frozen}

when not is_frozen {
  allow(reason: team)
}
`})

	_, err = k.Load(forged, "team.search", policy.Trusted(vocabulary))
	if ce, ok := errors.AsType[*policy.CompileError](err); ok {
		d := ce.Diagnostics[0]
		fmt.Println(d.Position, d.Message)
		fmt.Println(d.Help)
	}
	// Output:
	// deny change_freeze
	// team.sigil:2:8 module deploy.freeze is defined twice
	// the name belongs to the trusted source, defined at deploy/freeze.sigil:2:1; documents resolve by name, so each name has one definition
}

// Eval never returns a nil result. On an error the result holds the kind's
// default, so a host that fails closed can act on it and handle the error
// apart.
func ExamplePolicy_Eval() {
	type Input struct {
		Roles []string `policy:"roles"`
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "second_role")
	k := policy.NewKind[Input]("Roles",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)
	p, err := k.Compile(`policy roles: Roles@1

when roles[1] == "admin" {
  allow(reason: second_role)
}
`, "roles")
	if err != nil {
		fmt.Println(err)
		return
	}

	res, err := p.Eval(context.Background(), Input{Roles: []string{"viewer"}})
	if re, ok := errors.AsType[*policy.RuntimeError](err); ok {
		fmt.Println("runtime error:", re)
	}
	fmt.Println("fall back to:", res.Decision, res.Reason)
	// Output:
	// runtime error: 3:6 (roles): index 1 out of range for a list of 1
	// fall back to: deny no_rule_matched
}

// Match returns the winning decision's payload as its Go struct.
func ExampleDecision_Match() {
	type Input struct {
		Team string `policy:"team"`
	}
	type ReviewData struct {
		Approvers []string `policy:"approvers"`
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	review := policy.NewDecision[ReviewData]("review", "service_owner")
	k := policy.NewKind[Input]("Review",
		policy.WithVersion(1),
		policy.WithDecisions(deny, review),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)
	p, err := k.Compile(`policy review: Review@1

when team == "payments" {
  review(reason: service_owner, approvers: ["payments-leads", "security-leads"])
}
`, "review")
	if err != nil {
		fmt.Println(err)
		return
	}

	res, err := p.Eval(context.Background(), Input{Team: "payments"})
	if err != nil {
		fmt.Println(err)
		return
	}
	if r, ok := review.Match(res); ok {
		fmt.Println(res.Reason, r.Approvers)
	}
	if _, ok := deny.Match(res); !ok {
		fmt.Println("not denied")
	}
	// Output:
	// service_owner [payments-leads security-leads]
	// not denied
}

// A reason handle names a reason once, where it's declared; the options
// and the host's checks refer to the Go identifier, so a typo is a
// compile error or, in the string passed to Reason, a panic at init.
func ExampleOutcome_Is() {
	type Input struct {
		Frozen bool `policy:"frozen"`
		Owner  bool `policy:"owner"`
	}
	deny := policy.NewDecision[policy.None]("deny", "change_freeze", "not_owner", "no_rule_matched")
	var (
		changeFreeze  = deny.Reason("change_freeze")
		notOwner      = deny.Reason("not_owner")
		noRuleMatched = deny.Reason("no_rule_matched")
	)
	k := policy.NewKind[Input]("Freeze",
		policy.WithVersion(1),
		policy.WithDecisions(deny),
		policy.WithReasonPrecedence(changeFreeze, notOwner, noRuleMatched),
		policy.WithDefault(noRuleMatched),
	)
	p, err := k.Compile(`policy freeze: Freeze@1

when frozen {
  deny(reason: change_freeze)
}

when not owner {
  deny(reason: not_owner)
}
`, "freeze")
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, in := range []Input{{Frozen: true}, {Owner: false}, {Owner: true}} {
		res, err := p.Eval(context.Background(), in)
		if err != nil {
			fmt.Println(err)
			return
		}
		switch {
		case changeFreeze.Is(res):
			fmt.Println("frozen: retry after the freeze")
		case notOwner.Is(res):
			fmt.Println("not an owner: ask the owning team")
		case noRuleMatched.Is(res):
			fmt.Println("no rule matched:", res.Decision, res.Reason)
		}
	}
	// Output:
	// frozen: retry after the freeze
	// not an owner: ask the owning team
	// no rule matched: deny no_rule_matched
}

// A collecting kind applies every decision that fires, so it is read with
// MatchAll, which can return a decision more than once.
func ExampleDecision_MatchAll() {
	type Input struct {
		Teams []string `policy:"teams"`
	}
	type GrantData struct {
		TTL time.Duration `policy:"ttl,default=8h"`
	}
	read := policy.NewDecision[GrantData]("read", "member")
	admin := policy.NewDecision[GrantData]("admin", "platform", "oncall")
	k := policy.NewKind[Input]("Grants",
		policy.WithVersion(1),
		policy.WithCollect(read, admin),
	)
	p, err := k.Compile(`policy grants: Grants@1

when "engineering" in teams {
  read(reason: member)
}

when "platform" in teams {
  admin(reason: platform, ttl: 1h)
}

when "oncall" in teams {
  admin(reason: oncall)
}
`, "grants")
	if err != nil {
		fmt.Println(err)
		return
	}

	res, err := p.Eval(context.Background(), Input{Teams: []string{"engineering", "platform", "oncall"}})
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, g := range admin.MatchAll(res) {
		fmt.Println("admin", g.Reason, "for", g.Payload.TTL)
	}
	fmt.Println(len(res.Outcome), "grants in total")
	// Output:
	// admin platform for 1h0m0s
	// admin oncall for 8h0m0s
	// 3 grants in total
}

// An assert that doesn't hold fails the evaluation with an
// *AssertionError. It is a defect in the policy, the host or the input,
// not a decision, so count it apart.
func ExampleAssertionError() {
	type Input struct {
		Soak time.Duration `policy:"soak"`
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "soaked")
	k := policy.NewKind[Input]("Soak",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)
	p, err := k.Compile(`policy soak: Soak@1

assert("negative_soak", soak >= 0s)

when soak > 1h {
  allow(reason: soaked)
}
`, "soak")
	if err != nil {
		fmt.Println(err)
		return
	}

	_, err = p.Eval(context.Background(), Input{Soak: -time.Hour})
	if ae, ok := errors.AsType[*policy.AssertionError](err); ok {
		for _, f := range ae.Failures {
			fmt.Println(f.Reason, "failed at", f.Location())
		}
	}
	// Output:
	// negative_soak failed at 3:1 (soak)
}

// Candidates of two exclusive decisions in one evaluation are a
// *ConflictError, a defect in the policy rather than in the input.
func ExampleWithExclusive() {
	type Input struct {
		Teams []string `policy:"teams"`
	}
	customerData := policy.NewDecision[policy.None]("customer_data", "support")
	devEnv := policy.NewDecision[policy.None]("dev_env", "engineering")
	k := policy.NewKind[Input]("Grants",
		policy.WithVersion(1),
		policy.WithCollect(customerData, devEnv),
		policy.WithExclusive(customerData, devEnv),
	)
	p, err := k.Compile(`policy grants: Grants@1

when "support" in teams {
  customer_data(reason: support)
}

when "engineering" in teams {
  dev_env(reason: engineering)
}
`, "grants")
	if err != nil {
		fmt.Println(err)
		return
	}

	_, err = p.Eval(context.Background(), Input{Teams: []string{"support", "engineering"}})
	if _, ok := errors.AsType[*policy.ConflictError](err); ok {
		fmt.Println(err)
	}
	// Output:
	// conflict: exclusive customer_data, dev_env: more than one fired
	//   customer_data support at 4:3 (grants)
	//   dev_env engineering at 8:3 (grants)
}

// A conflict still fails the evaluation, but the result the host falls
// back to says so, instead of claiming that no rule matched. Here two
// approve reasons the kind doesn't rank fire together.
func ExampleWithConflict() {
	type Input struct {
		Roles []string `policy:"roles"`
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched", "conflicting_rules")
	approve := policy.NewDecision[policy.None]("approve", "release_manager", "service_owner")
	k := policy.NewKind[Input]("Deploy",
		policy.WithVersion(1),
		policy.WithDecisions(deny, approve),
		policy.WithDefault(deny.Reason("no_rule_matched")),
		policy.WithConflict(deny.Reason("conflicting_rules")),
	)
	p, err := k.Compile(`policy deploy: Deploy@1

when "release_manager" in roles {
  approve(reason: release_manager)
}

when "owner" in roles {
  approve(reason: service_owner)
}
`, "deploy")
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, roles := range [][]string{{"release_manager", "owner"}, {"viewer"}} {
		res, err := p.Eval(context.Background(), Input{Roles: roles})
		if _, ok := errors.AsType[*policy.ConflictError](err); ok {
			fmt.Println("conflict, fall back to:", res.Decision, res.Reason)
			continue
		}
		fmt.Println("decided:", res.Decision, res.Reason)
	}
	// Output:
	// conflict, fall back to: deny conflicting_rules
	// decided: deny no_rule_matched
}

// A host function receives its arguments as Go values; an error it returns
// becomes a *RuntimeError.
func ExampleWithFunc() {
	type Input struct {
		Listen string `policy:"listen"`
	}
	deny := policy.NewDecision[policy.None]("deny", "privileged", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "unprivileged")
	k := policy.NewKind[Input]("Ports",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
		policy.WithFunc("port", strconv.Atoi),
	)
	p, err := k.Compile(`policy ports: Ports@1

when port(listen) < 1024 {
  deny(reason: privileged)
}

when port(listen) >= 1024 {
  allow(reason: unprivileged)
}
`, "ports")
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, port := range []string{"8080", "22", "http"} {
		res, err := p.Eval(context.Background(), Input{Listen: port})
		fmt.Println(port, "->", res.Decision, res.Reason, err != nil)
	}
	// Output:
	// 8080 -> allow unprivileged false
	// 22 -> deny privileged false
	// http -> deny no_rule_matched true
}

// The trace explains a result: every candidate, where it came from, and
// the conditions that held for the winning decision.
func ExampleTrace() {
	type Input struct {
		Tier   string `policy:"tier"`
		Hotfix bool   `policy:"hotfix"`
	}
	type ApproveData struct {
		Bake time.Duration `policy:"bake,default=1h"`
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	approve := policy.NewDecision[ApproveData]("approve", "hotfix", "standard")
	k := policy.NewKind[Input]("Deploy",
		policy.WithVersion(1),
		policy.WithDecisions(deny, approve),
		policy.WithReasonPrecedence(approve.Reason("hotfix"), approve.Reason("standard")),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)
	p, err := k.Compile(`policy deploy: Deploy@1

when tier != "critical" {
  approve(reason: standard)

  when hotfix {
    approve(reason: hotfix, bake: 10m)
  }
}
`, "deploy")
	if err != nil {
		fmt.Println(err)
		return
	}

	res, err := p.Eval(context.Background(), Input{Tier: "standard", Hotfix: true})
	if err != nil {
		fmt.Println(err)
		return
	}
	fmt.Println("winner:", res.Decision, res.Reason)
	for _, c := range res.Trace.Candidates {
		fmt.Println(c)
		for _, cond := range c.Conditions {
			fmt.Println("  when", cond.Text)
		}
	}
	// Output:
	// winner: approve hotfix
	// approve hotfix at 7:5 (deploy) {bake: 10m0s}
	//   when tier != "critical"
	//   when hotfix
	// approve standard at 4:3 (deploy) {bake: 1h0m0s}
	//   when tier != "critical"
}

// Compiled policies are immutable, so a reload is a pointer swap. Compile
// the new source first and keep the last good policy when that fails.
func Example_reload() {
	type Input struct {
		User string `policy:"user"`
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	allow := policy.NewDecision[policy.None]("allow", "listed")
	k := policy.NewKind[Input]("Users",
		policy.WithVersion(1),
		policy.WithDecisions(deny, allow),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)

	var current atomic.Pointer[policy.Policy[Input]]
	reload := func(src string) {
		p, err := k.Compile(src, "users")
		if err != nil {
			fmt.Println("reload failed, keeping the last good policy")
			return
		}
		current.Store(p)
	}
	decide := func(user string) {
		res, _ := current.Load().Eval(context.Background(), Input{User: user})
		fmt.Println(user, "->", res.Decision)
	}

	reload(`policy users: Users@1
when user in ["ada"] { allow(reason: listed) }
`)
	decide("grace")

	reload(`policy users: Users@1
when user in ["ada", "grace"] { allow(reason: listd) }
`)
	decide("grace")

	reload(`policy users: Users@1
when user in ["ada", "grace"] { allow(reason: listed) }
`)
	decide("grace")
	// Output:
	// grace -> deny
	// reload failed, keeping the last good policy
	// grace -> deny
	// grace -> allow
}

// A worker that consumes a queue has nothing above it to recover a panic,
// so its kind recovers host function panics: a bad input fails closed
// with a *RuntimeError instead of killing the worker, and the stack stays
// available for the log.
func ExampleWithRecoverHostPanics() {
	type Input struct {
		Service string `policy:"service"`
	}
	owners := map[string]*struct{ Team string }{"payments-api": {Team: "payments"}}
	grant := policy.NewDecision[policy.None]("grant", "owner")
	k := policy.NewKind[Input]("Access",
		policy.WithVersion(1),
		policy.WithCollect(grant),
		policy.WithFunc("owner", func(service string) string {
			return owners[service].Team // panics on an unknown service
		}),
		policy.WithRecoverHostPanics(),
	)
	p, err := k.Compile(`policy access: Access@1
when owner(service) == "payments" { grant(reason: owner) }
`, "access")
	if err != nil {
		fmt.Println(err)
		return
	}

	for _, service := range []string{"payments-api", "search-api"} {
		res, err := p.Eval(context.Background(), Input{Service: service})
		if hp, ok := errors.AsType[*policy.HostPanicError](err); ok {
			fmt.Println(service, "->", err, "| stack captured:", len(hp.Stack) > 0)
			continue
		}
		fmt.Println(service, "->", len(res.Outcome), "grant")
	}
	// Output:
	// payments-api -> 1 grant
	// search-api -> 2:6 (access): host function owner panicked: runtime error: invalid memory address or nil pointer dereference | stack captured: true
}

// An enum gives a named string type a fixed set of values. Policies
// write the values bare, and a Go value outside the set fails the
// evaluation closed when a rule reads it.
func ExampleWithEnum() {
	type Tier string
	const (
		Critical Tier = "critical"
		Standard Tier = "standard"
		Internal Tier = "internal"
	)
	type Input struct {
		Tier Tier `policy:"tier"`
	}
	type ReviewData struct {
		Tier Tier `policy:"tier"`
	}
	deny := policy.NewDecision[policy.None]("deny", "no_rule_matched")
	review := policy.NewDecision[ReviewData]("review", "sensitive")
	k := policy.NewKind[Input]("Deploy",
		policy.WithVersion(1),
		policy.WithEnum(Critical, Standard, Internal),
		policy.WithDecisions(deny, review),
		policy.WithDefault(deny.Reason("no_rule_matched")),
	)
	fmt.Println(strings.Split(k.Schema(), "\n")[2])

	p, err := k.Compile(`policy deploy: Deploy@1

when tier in [critical, standard] {
  review(reason: sensitive, tier: tier)
}
`, "deploy")
	if err != nil {
		fmt.Println(err)
		return
	}
	for _, tier := range []Tier{Critical, Internal, ""} {
		res, err := p.Eval(context.Background(), Input{Tier: tier})
		if err != nil {
			fmt.Printf("%q -> %s(reason: %s): %v\n", tier, res.Decision, res.Reason, err)
			continue
		}
		if r, ok := review.Match(res); ok {
			fmt.Printf("%q -> review of a %s service\n", tier, r.Tier)
			continue
		}
		fmt.Printf("%q -> %s\n", tier, res.Decision)
	}
	// Output:
	// enum Tier: critical | standard | internal
	// "critical" -> review of a critical service
	// "internal" -> deny
	// "" -> deny(reason: no_rule_matched): 3:6 (deploy): tier: "" is not a value of Tier
}
