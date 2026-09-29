---
title: Common patterns
icon: mdi:puzzle
createTime: 2026/09/24 22:30:00
permalink: /guides/patterns/
---

Short recipes for things policy authors do all the time. Most use the `DeployApproval` kind from the [tour](/getting-started/tour/). When a recipe needs something that kind doesn't declare, it shows the kind lines to add, because in Sigil nothing exists in a policy unless the kind says so. That includes reasons: every reason a recipe constructs is assumed to be declared on its decision in the kind.

## Match a set of labels

To require several labels at once, use `has` with a map literal. It's true when the map contains every listed key with exactly that value; extra labels on the service don't matter.

```sigil
let managed = service.labels has {
  "app.kubernetes.io/managed-by": "argocd",
  "platform.example.com/lifecycle": "ga",
}
```

To require that a key exists, whatever its value, pass a single string:

```sigil
let has_owner = service.labels has "platform.example.com/owner"
```

Keys with dots or slashes always go through a map literal or `[...]`. `service.labels.regions` doesn't work either, because `labels` is a map, not a struct.

::: warning Missing keys read as empty strings
`service.labels["team"]` on a service without a `team` label yields `""`, the zero value, as it would in Go. That makes `!=` risky in rules that grant something:

```sigil
// Approves services with no team label at all.
when service.labels["team"] != "payments" {
  approve(not_payments)
}
```

Match positively in grants (`== "payments"`, `has {...}`), or check for the key first with `has "team"`.
:::

## Check a comma-separated label against a list

Labels are flat strings, so lists get packed into them. Split with the host's `split` function and compare with `all in` (every element on the left appears on the right) or `any in` (at least one does):

```sigil
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
```

A missing label reads as `""`, and `split` is Go's `strings.Split`, which returns `[""]` for it, so the rule fails closed only by accident of `split`. A function that returned `[]` would make it vacuously true ([why](/understanding/strictness/#absent-data-follows-go)). Test for the label instead:

```sigil
let cleared =
  service.labels has "regions"
  and split(service.labels["regions"], ",") all in actor.regions
```

## Handle optional fields

A Go pointer field becomes an optional type `?T` in the kind. Suppose the host adds an optional change ticket to `Release`:

```go
type Release struct {
	Soak   time.Duration `policy:"soak"`
	Hotfix bool          `policy:"hotfix"`
	Ticket *string       `policy:"ticket"`
}
```

```sigil
type Release {
  soak: duration
  hotfix: bool
  ticket: ?string
}
```

You can't compare an optional directly. Unwrap it with `??` and a fallback:

```sigil
when release.hotfix and (release.ticket ?? "") == "" {
  deny(hotfix_without_ticket)
}
```

`??` binds tighter than `==`, so the parentheses aren't required, but they make the intent obvious. Writing `release.ticket == "CHG-1234"` without `??` is a compile error that tells you to unwrap it.

The rule above treats a missing ticket and an empty one the same way. When the difference matters, test for the value with [`present`](/reference/expressions/#presence-present):

```sigil
when release.hotfix and not present release.ticket {
  deny(hotfix_without_ticket)
}
```

::: info Optional structs
A pointer to a struct, say `?Release`, has no literal to use as a fallback. Read its fields with [optional chaining](/reference/expressions/#optional-chaining) instead: `release?.ticket ?? ""` is absent-safe whether the release or the ticket is missing.
:::

## Test every element, or any element

Quantifiers apply a condition to each element of a list. `any` needs one match, `all` needs every element to match:

```sigil
let is_sre = any r in actor.roles: r like "sre-*"
let eu_only = all r in actor.regions: r like "eu-*"
```

The quantifier body runs as far to the right as it can, so combining a quantifier with something else needs parentheses:

```sigil
// The body is `r like "sre-*" or release.hotfix`, checked per role.
let a = any r in actor.roles: (r like "sre-*" or release.hotfix)

// The quantifier ends at the closing parenthesis.
let b = (any r in actor.roles: r like "sre-*") or release.hotfix
```

Both compile, and they disagree for an actor with no roles shipping a hotfix: `a` has no elements to test and is false, while `b` is true. The parentheses inside `a` don't change how it parses. `sigil fmt` adds them to any body whose top level is `and`, `or` or `xor`, so you can see where the body ends. Put quantifiers in their own `let` or in parentheses and the question never comes up.

`all` over an empty list is true. `all r in actor.regions: r like "eu-*"` holds for an actor with no regions at all. In a rule that grants something, pair it with an `any` over the same list, which is false when the list is empty:

```sigil
let eu_only =
  (any r in actor.regions: r like "eu-*")
  and (all r in actor.regions: r like "eu-*")
```

The same `any` is how you ask whether a list has elements at all; see [Test whether a list is empty](#test-whether-a-list-is-empty).

When you only need membership, prefer the operators: `"deployer" in actor.roles` reads better than `any r in actor.roles: r == "deployer"`.

## Test whether a list is empty

Lists have no `==` or `!=`, so `actor.regions != []` is a compile error: the `[]` takes its type from the other side, and `!=` isn't defined for `list<string>`. Ask with a quantifier whose body is `true` instead, which is what the error suggests:

```sigil
let has_regions = any r in actor.regions: true
let lacks_regions = not (any r in actor.regions: true)
```

`any` over an empty list is false, so `has_regions` holds exactly when the list has an element. Name the test in a `let`, as here, and rules read `when lacks_regions { ... }`.

If your kind declares a length function, compare its result instead:

```sigil
// In the kind; the host implements it in Go.
fn len(list<string>) -> int
```

```sigil
let has_regions = len(actor.regions) > 0

when len(actor.regions) == 0 {
  deny(no_regions)
}
```

A map can't be quantified over, so testing a map for emptiness always needs a host function like this one, declared for the map's type. To test for one key, use `has`: `service.labels has "team"`.

## Keep the requestor off the approvers

Four-eyes review means nobody approves their own request, even when they're on the list that normally approves. Take them off the list where it's built, with a [filter](/reference/expressions/#filters):

```sigil
param approvers: list<string>

let reviewers = filter a in approvers: a != actor.name
let has_reviewers = any r in reviewers: true

when has_reviewers {
  review(service_owner, approvers: reviewers)
}

when not has_reviewers {
  deny(not_eligible)
}
```

The second rule matters. When the requestor is the only approver, the filter leaves nobody, and a review that nobody can approve just waits forever. Decide what happens instead: deny, as here, or send the review to a fallback list.

The policy only proposes who may approve. The approval itself happens in the host, after evaluation, so the host still has to reject a self-approval. The policy's filter is what keeps the requestor from being asked in the first place.

To hold every team's policy to the rule, not just this one, put an assert on the reviews in `outcome` into a policy the host [requires](/reference/evaluation/#required-policies):

```sigil
assert("no_self_review",
  all r in outcome.review: actor.name not in r.approvers)
```

`outcome.review` is every review the host gets back, with its payload (see [Candidates](/reference/expressions/#candidates)). Write `all`, not `any`: in a collecting kind that returns several reviews, `any` would let one clean review hide a self-review next to it. A team policy that forgets the filter then fails the evaluation with the offending approvers in the error, instead of quietly asking the requestor to approve their own change.

## Add text computed from the input

A reason is a name the kind declares, so it can't carry a service name or anything else from the input. When the person reading one result needs that text, declare an optional `detail: string` payload field on the decision, and fill it from any `string` expression:

```sigil
// kind: decision deny(detail: string = "") { not_eligible soak_too_short no_rule_matched }
when release.soak < min_soak and not release.hotfix {
  deny(soak_too_short, detail: service.name)
}
```

In Go, the field goes on the decision's payload struct:

```go
type DenyData struct {
	Detail string `policy:"detail,default=\"\""`
}
```

Give it a default. Constructors that don't pass it keep compiling, and the kind's `default deny(no_rule_matched)` passes no payload, so every field of its decision needs one.

`detail` is an ordinary payload field; only the convention is special. The reason is for machines and dashboards, so count and alert by it. The detail is for a human reading one specific result, so show it in the approval UI or the log line, and keep it out of metric labels. There's no `+` on strings, so the value is a single string expression, such as an input field or a label. [Decisions and reasons](/understanding/decisions/) explains why the reason itself is never computed.

## Write time-based rules

Sigil has no clock. Evaluation is deterministic, so a policy that needs the current time gets it from the host as an input. Add it to the kind, along with whatever timestamps the rule compares against:

```go
type Input struct {
	// ...
	Now time.Time `policy:"now"`
}

type Release struct {
	// ...
	BuiltAt time.Time `policy:"built_at"`
}
```

```sigil
input now: timestamp

type Release {
  soak: duration
  hotfix: bool
  built_at: timestamp
}
```

Subtracting two timestamps gives a duration, which compares with duration literals:

```sigil
when now - release.built_at > 30d {
  deny(stale_build)
}
```

Adding a duration to a timestamp gives a timestamp: `release.built_at + 1d` is exactly 24 hours later. There are no calendar functions. If a rule needs "business hours" or "no deploys on Friday", the host declares a function such as `fn hour_of_day(timestamp) -> int` in the kind and implements it in Go, time zone and all.

Replaying a decision later is then just evaluating the same input again, `now` included.

## Reject input that can't be right

A deny is an answer: the deploy was looked at and refused. When the input itself is broken, say a request with no actor name, there's nothing to decide, and an `assert` says so instead:

```sigil
assert("named_actor", actor.name != "")

when service.tier == "critical" {
  assert("critical_needs_team_label", service.labels has "team")
}
```

A failed assert fails the evaluation. The host gets an assertion error naming `named_actor`, alongside the kind's default decision, and logs it as an error rather than counting it as a deny. An assert that doesn't read `outcome` runs before any rule, so it works as a precondition, and one inside a `when` is only checked where the condition holds. A test case pins it with `asserts: [named_actor]` (see [Test your policies](/guides/test-policies/#write-test-cases)). The reason is a string literal the policy picks, not a name from the kind. [`assert`](/reference/policy-files/#assert) has the full rules.

## Fail closed

A policy fails closed when the absence of information leads to a deny. The pieces:

- Make the kind's `default` a deny. Anything no rule covers gets refused.
- Write explicit denies for things that must never be approved, in a policy the host requires with `policy.Require`. In `DeployApproval`, deny outranks every other decision, no composed policy can remove a deny, and a required policy can't be gated behind a `when`.
- Write grants as positive matches. A grant that fires on `!=` or `not` fires on missing data too (see the missing-keys warning above).
- Let failures fall back. When a host function fails, an index is out of range, an `assert` fails or two `exclusive` outcomes conflict, `Eval` returns the error together with the kind's default decision, so a host that just uses the result stays closed. [Handle failed evaluations](/guides/handle-errors/#fail-closed) shows the host's side.

The eligibility check in `deploy.guardrails` shows the shape:

```sigil
when not eligible {
  deny(not_eligible)
}
```

`eligible` is a positive match, and the deny fires on its absence.

## Share matchers across policies

Define a matcher once as a `let` in a module and import it wherever it's needed:

```sigil
module deploy.common: DeployApproval@1

pub let cleared =
  split(service.labels["regions"], ",") all in actor.regions
```

```sigil
use deploy.common.{cleared}

when cleared and "payments-sre" in actor.teams {
  approve(payments_sre, bake: 15m)
}
```

A module holds `use` and `let` statements and nothing else, so importing from it never brings rules along. `use deploy.common` without braces works too, and then the matcher reads `common.cleared`. Only `pub let`s can be imported, from a module or a policy, and a policy's `pub let` can't read a param, because a param has no value outside an invocation. [Per-team policies](/guides/team-policies/) covers imports in more detail.

## Add conditions to a shared policy

Invoke a shared policy inside a `when` block to apply its rules only where the block's condition holds. The condition is added to every rule the call brings in:

```sigil
use deploy.production

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}

when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}
```

Two gated calls with complementary conditions are the idiom for "this policy, with different params depending on the input". Invocation arguments can't read inputs, so the input-dependent choice goes into the `when`, and `sigil explain` can still print every rule with concrete values.

Don't gate a policy that holds denies unless you mean to switch them off where the condition is false. The `gated-deny` lint warns about it, and a host that requires the policy rejects it outright.

## Compare versions

Strings aren't ordered, so `service.labels["version"] < "v2.0.0"` is a compile error. Byte-wise order would make `"v10" < "v9"` true, which is exactly the result that gets a version rule wrong. Sigil ships no version comparison; declare a host function in the kind that compares versions the way yours are written:

```sigil
// In the kind; the name and the Go function behind it are the host's choice.
fn version_below(string, string) -> bool
```

The host implements it and registers it with `policy.WithFunc`, here with `golang.org/x/mod/semver`:

```go
var Deploy = policy.NewKind[Input]("DeployApproval",
	// ...
	policy.WithFunc("version_below", versionBelow),
)

func versionBelow(a, b string) (bool, error) {
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return false, fmt.Errorf("not a semantic version: %q or %q", a, b)
	}
	return semver.Compare(a, b) < 0, nil
}
```

A policy calls it like any other host function:

```sigil
when version_below(service.labels["version"], "v2.0.0") {
  deny(version_too_old)
}
```

The error matters. A missing label reads as `""`, which isn't a version, so the call fails and the evaluation returns the kind's default, a deny, instead of the rule quietly not firing. [Handle failed evaluations](/guides/handle-errors/) covers the host's side.

::: warning Planned
[Host-ordered types](/project/planned/#host-ordered-types) would let `<` compare versions directly.
:::

## Compare strings case-sensitively, or not

String comparison is case-sensitive: `"Production" == "production"` is false. Kubernetes labels and most identifiers in this space are case-sensitive, so that's the default. When the data really is inconsistent, use a regex with RE2's case-insensitive flag:

```sigil
when service.tier matches `(?i)^critical$` {
  review(critical_any_case, approvers: ["sre-leads"])
}
```

## Choose between globs and regexes

`like` matches a glob, `matches` an RE2 regex. Both patterns must be literals, so they compile once, with the policy.

```sigil
let is_sre = any r in actor.roles: r like "sre-*"
let platform_team = any t in actor.teams: t matches `^platform-[a-z]+$`
```

Use `like` when a `*` is all you need; it's easier to read. Reach for `matches` when you need anchors, character classes or alternation. Write regexes as backtick raw strings so backslashes don't need escaping: `` `^v\d+$` `` rather than `"^v\\d+$"`.

RE2 runs in linear time, so no pattern can make evaluation hang.
