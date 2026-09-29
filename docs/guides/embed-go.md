---
title: Embed Sigil in a Go service
icon: mdi:language-go
createTime: 2026/09/29 12:00:00
permalink: /guides/embed-go/
---

By the end of this guide your Go service defines the `DeployApproval` kind from the [tour](/getting-started/tour/) in Go, loads the team policies with the platform's guardrails required, and acts on each decision through typed payloads. Everything lives in package `policy`, import path `github.com/spechtlabs/sigil/pkg/policy`; the [Go API](/reference/go-api/) lists every symbol, and [the example service](/guides/example-service/) is a complete host built this way.

## Define the kind

The kind is the contract between your service and the policies. You write it as Go types and declare it once, at package level. Why the contract lives in Go: [Kinds as contracts](/understanding/kinds/).

### Describe the input

Write the input as ordinary structs. Every field tagged `policy:"..."` becomes an `input` of the kind, and every nested struct becomes a `type`. Untagged fields are invisible to policies.

```go
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
	Tier   string            `policy:"tier"`
	Owners []string          `policy:"owners"`
	Labels map[string]string `policy:"labels"`
}

type Actor struct {
	Name    string   `policy:"name"`
	Teams   []string `policy:"teams"`
	Roles   []string `policy:"roles"`
	Regions []string `policy:"regions"`
}
```

[Go type mapping](/reference/go-api/#go-type-mapping) lists which Go types map to which Sigil types.

### Give each decision a payload

Write one struct per decision that carries data. Leave the reason out: every decision has one implicitly. A payload field can take a default after its name, as a Sigil constant of the field's type:

```go
type ReviewData struct {
	Approvers []string `policy:"approvers"`
}

type ApproveData struct {
	Bake time.Duration `policy:"bake,default=1h"`
}
```

A decision that carries only a reason uses `policy.None` as its payload.

### Declare the decisions and their reasons

Declare each decision as a typed handle with `policy.NewDecision`, listing every reason a rule may give for it:

```go
var (
	Deny    = policy.NewDecision[policy.None]("deny", "not_eligible", "soak_too_short", "no_rule_matched")
	Review  = policy.NewDecision[ReviewData]("review", "service_owner")
	Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")
)
```

Why reasons are declared names rather than free text: [Decisions and reasons](/understanding/decisions/).

### Name the reasons your Go code uses

Wherever Go code names a reason, to rank it, make it the default or compare a result with it, take a reason handle from the decision:

```go
var (
	NotEligible    = Deny.Reason("not_eligible")
	SoakTooShort   = Deny.Reason("soak_too_short")
	NoRuleMatched  = Deny.Reason("no_rule_matched")
	ReleaseManager = Approve.Reason("release_manager")
	PaymentsSRE    = Approve.Reason("payments_sre")
)
```

`Reason` panics on a name the decision doesn't declare, so a typo stops the program at init instead of compiling into a comparison that never matches:

```text
policy: decision deny has no reason "no_rule_mached" (did you mean "no_rule_matched"? deny declares: not_eligible, soak_too_short, no_rule_matched)
```

### Build the kind

Tie it together with `policy.NewKind`. The options are the kind file's declarations, written in Go:

```go
var Deploy = policy.NewKind[Input]("DeployApproval",
	policy.WithVersion(1),
	policy.WithDecisions(Deny, Review, Approve), // order = precedence
	policy.WithReasonPrecedence(NotEligible, SoakTooShort, NoRuleMatched),
	policy.WithReasonPrecedence(ReleaseManager, PaymentsSRE),
	policy.WithDefault(NoRuleMatched),
	policy.WithFunc("split", strings.Split),
)
```

- `WithDecisions` takes the decisions highest precedence first, so a deny beats a review beats an approval.
- `WithReasonPrecedence` ranks one decision's reasons, so two denies, or two approvals, never conflict.
- `WithDefault` is the result when no rule fires, and also what a failed evaluation returns. To return a reason of its own after a conflict, add `WithConflict`, as [Name conflicts in the result](/guides/handle-errors/#name-conflicts-in-the-result) shows.
- `WithFunc` binds a host function a policy can call, under the name you give it. The function must be pure, terminate and not panic. To keep a panicking function from taking the service down, see [Recover host panics](/guides/handle-errors/#recover-host-panics).

For a kind where every decision that fires applies, such as the example service's `AccessGrant`, pass `policy.WithCollect` instead of `policy.WithDecisions`; it may leave out the default. Every option, with its kind file equivalent, is in [Kind options](/reference/go-api/#kind-options).

`NewKind` panics at init on a Go type it can't map or on a kind that breaks a validity rule, and lists every problem at once. Leaving out both `WithVersion` and `WithDefault` gives:

```text
policy.NewKind(DeployApproval): invalid kind:
  invalid kind version 0 (the version is a positive integer that changes when the contract does)
  kind DeployApproval has no default decision (declare `default <decision>(<reason>)` for the case where no rule fires)
```

The tooling reads the kind as a kind file that your service exports; [Build a host binary](/guides/host-binary/) sets that up.

## Load the policies

Compile the policies once, at startup, and keep the compiled `*policy.Policy`. It's immutable and safe to evaluate from any number of goroutines.

Embed the policy tree in the binary and load it with the kind's `Load`, naming the root policy and the policies the root must invoke:

```go
//go:embed policies
var policies embed.FS

func load() *policy.Policy[Input] {
	p, err := Deploy.Load(policies, "payments.production",
		policy.Require("deploy.guardrails"))

	if err != nil {
		log.Fatal(err) // every diagnostic, with file:line:col and a fix hint
	}

	return p
}
```

`Load` reads every `.sigil` file in the `fs.FS` into one bundle and resolves imports by the names in the documents' headers, so it doesn't matter how the documents are split into files. `policy.Require("deploy.guardrails")` fails the load unless `payments.production` invokes `deploy.guardrails` unconditionally. Put the requirement wherever the host loads team policies, and name the policies that hold the denies no team may switch off. The rules are in [Bundles](/reference/bundles/) and [Required policies](/reference/evaluation/#required-policies).

A failed load returns a `*policy.CompileError` whose message quotes the offending lines the way the CLI does:

```text
3:14 (deploy.gate): error: unknown field "teir" on type Service
  |
3 | when service.teir == "critical" {
  |              ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

Without `From`, the required policy is looked up in the bundle like any other document, which suits an `embed.FS` built from a reviewed repository, as above. Whenever someone other than the platform team can write to the bundle, for example a ConfigMap, pass the platform's documents with `policy.From`, so the guardrails can only come from them:

```go
//go:embed platform
var platformFS embed.FS

func loadTeams() (*policy.Policy[Input], error) {
	return Deploy.Load(os.DirFS("/etc/sigil"), "payments.production",
		policy.Require("deploy.guardrails", policy.From(platformFS)))
}
```

[Policies in a ConfigMap](/guides/configmaps/) walks through that setup, and [Trusted sources](/reference/bundles/#trusted-sources) has the rules. Why it's needed: [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source).

To bind the root's params from Go instead of from a team file, pass `policy.Params`; see [Bind params from Go instead](/guides/team-policies/#bind-params-from-go-instead). To compile a single source string, for example in a test, use `Deploy.Compile(src, "payments.production", opts...)`, which takes the same options.

## Evaluate and act on the result

Call `Eval` with the request's context and the input. Check the error first, then match the result against the decision handles:

```go
func decide(ctx context.Context, p *policy.Policy[Input], in Input) error {
	res, err := p.Eval(ctx, in)
	if err != nil {
		return reject(res, err) // res holds deny(no_rule_matched); see Handle failed evaluations
	}

	if r, ok := Review.Match(res); ok {
		return requestReview(r.Approvers, res.Reason) // r is a typed ReviewData
	}

	if a, ok := Approve.Match(res); ok {
		return startRollout(a.Bake)
	}

	if NoRuleMatched.Is(res) {
		flagUncovered(p.Name()) // the default: no rule covers this deploy
	}

	return reject(res, nil)
}
```

- Check `err` before you match. A failed evaluation still returns a result, holding the kind's default, so `Deny.Match` reports `true` on it. [Handle failed evaluations](/guides/handle-errors/) shows what to do with the error.
- `Match` returns the payload as its Go struct when the outcome is exactly one entry of that decision.
- A reason handle's `Is` does the same and compares the reason too. Use it instead of comparing `res.Reason` with a string, which compiles with a typo in it and never matches.

A collecting kind can grant a decision more than once, so read it with `MatchAll`, which returns every entry of that decision in outcome order, each with its typed payload. For the example service's `AccessGrant` kind, where `Admin` is `policy.NewDecision[AdminData]("admin", "clearance", "break_glass")`:

```go
for _, g := range Admin.MatchAll(res) {
	grantAdmin(g.Payload.TTL, g.Reason) // g.Payload is a typed AdminData
}
```

`Match` and `Is` panic on a `collect all` kind without precedence, since there's no single decision to match.

To show why a request got its decision, log or return the trace. Every candidate the rules produced is in `res.Trace.Candidates`, and `Candidate.Location()` renders where it came from, through every invocation:

```text
payments/production.sigil:10:3 → deploy/production.sigil:16:5
```

The fields of `Result`, `Trace` and `Candidate` are in [Result](/reference/go-api/#result), and the matching rules in [Typed matching](/reference/go-api/#typed-matching).

## Next steps

- [Handle failed evaluations](/guides/handle-errors/): fail closed, tell the errors apart, count them and bound evaluation time.
- [Policies in a ConfigMap](/guides/configmaps/): ship team policies to the service and reload them without an outage.
- [Build a host binary](/guides/host-binary/): give the `sigil` CLI your host functions and export the kind file.
- [Test your policies](/guides/test-policies/): run policy test cases from `go test`.
