---
title: Common patterns
icon: mdi:puzzle
createTime: 2026/09/24 22:30:00
permalink: /guides/patterns/
---

Short recipes for things policy authors do all the time. Most use the `DeployApproval` kind from the [tour](/getting-started/tour/). When a recipe needs something that kind doesn't declare, it shows the kind lines to add, because in Sigil nothing exists in a policy unless the kind says so.

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
  approve("not_payments")
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

Watch what happens when the label is missing. The lookup yields `""`, and `split` is Go's `strings.Split`, which turns `""` into `[""]`, a list with one empty string, not an empty list. `[""] all in actor.regions` is false unless some region is literally named `""`, so a missing label fails closed here. That's the behaviour you want, but it rests on a detail of `strings.Split`. An empty list on the left of `all in` would be vacuously true, and whether Sigil keeps that rule is an [open question](/project/open-questions/).

Don't rely on the accident. Say what you mean:

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
  deny("hotfix_without_ticket")
}
```

`??` binds tighter than `==`, so the parentheses aren't required, but they make the intent obvious. Writing `release.ticket == "CHG-1234"` without `??` is a compile error that tells you to unwrap it.

::: info Optional structs
This works for scalar optionals. A pointer to a struct, say `?Release`, can't be unwrapped yet, because there's no struct literal to use as a fallback. How optional structs should work is an [open question](/project/open-questions/).
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
let a = any r in actor.roles: r like "sre-*" or release.hotfix

// The quantifier ends at the closing parenthesis.
let b = (any r in actor.roles: r like "sre-*") or release.hotfix
```

Both compile, and they disagree for an actor with no roles shipping a hotfix: `a` has no elements to test and is false, while `b` is true. Put quantifiers in their own `let` or in parentheses and the question never comes up.

`all` over an empty list is true. `all r in actor.regions: r like "eu-*"` holds for an actor with no regions at all. In a rule that grants something, pair it with a check that the list isn't empty.

When you only need membership, prefer the operators: `"deployer" in actor.roles` reads better than `any r in actor.roles: r == "deployer"`.

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
  deny("stale_build")
}
```

Adding a duration to a timestamp gives a timestamp: `release.built_at + 1d` is exactly 24 hours later. There are no calendar functions. If a rule needs "business hours" or "no deploys on Friday", the host declares a function such as `fn hour_of_day(t: timestamp) -> int` in the kind and implements it in Go, time zone and all.

Replaying a decision later is then just evaluating the same input again, `now` included.

## Fail closed

A policy fails closed when the absence of information leads to a deny. The pieces:

- Make the kind's `default` a deny. Anything no rule covers gets refused.
- Write explicit denies for things that must never be approved. Deny outranks every other decision, and no composed policy can remove a deny.
- Write grants as positive matches. A grant that fires on `!=` or `not` fires on missing data too (see the missing-keys warning above).
- Let runtime errors fall back. When a host function fails or an index is out of range, `Eval` returns the error together with the kind's default decision, so a host that just uses the result stays closed.

The eligibility check in `deploy.production` shows the shape:

```sigil
when not eligible {
  deny("not_eligible")
}
```

`eligible` is a positive match, and the deny fires on its absence.

## Share matchers across policies

Define a matcher once as a `let` in the base policy and reach it from other policies through a `use` alias:

```sigil
use deploy.production(approvers: ["payments-leads"]) as base

when base.eligible and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

`use` always brings the used policy's rules along with its lets, so here the team gets `deploy.production`'s denies too. That's usually what you want. If you only want the matchers, put them in a policy with lets and no rules; there's no separate "library" file type, and such a policy works as one as long as it implements the same kind. [Per-team policies](/guides/team-policies/) covers aliases in more detail.

## Compare strings case-sensitively, or not

String comparison is case-sensitive: `"Production" == "production"` is false. Kubernetes labels and most identifiers in this space are case-sensitive, so that's the default. When the data really is inconsistent, use a regex with RE2's case-insensitive flag:

```sigil
when service.tier matches `(?i)^critical$` {
  review("critical_any_case", approvers: approvers)
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
