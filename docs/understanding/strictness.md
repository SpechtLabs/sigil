---
title: Strict schema, forgiving data
icon: mdi:shield-check
createTime: 2026/09/24 22:30:00
permalink: /understanding/strictness/
---

Sigil draws a hard line between two kinds of "missing". A field the schema doesn't know about is a bug in the policy, so it fails at compile time. A value the input doesn't contain is a normal fact about the world, so it gets a well-defined zero value at runtime, the way Go does it.

## The typo that turns a deny rule off

filt-rs resolves unknown properties to `null`. For a filter that's pragmatic: a typo means the filter matches nothing, and someone notices the empty list. Carry the same rule into a policy language and it becomes a security bug.

Imagine this rule, a variant of the deploy gate used throughout these docs, in a language with filt-rs semantics:

```sigil
when service.teir == "critical" and not release.hotfix {
  deny("critical_needs_hotfix_flag")
}
```

`service.teir` doesn't exist, so it's `null`. `null == "critical"` is false. The deny never fires, every other rule still runs, and a critical service ships on whatever `approve` some other block produced. Nothing errors, nothing logs, and the policy passes every test that didn't happen to deploy a critical service.

That's the failure mode strictness is designed to kill. Deny rules fail open when their conditions break silently, and the rules that matter most tend to cover the rare cases nobody tests.

## Schema problems fail at compile time

Because every policy declares its kind, the compiler knows every input, every field, every function signature and every decision payload. It rejects:

- an unknown field or input, like `service.teir`
- a type mismatch, like comparing a `duration` to a `string` or an `int` to a `float`
- a payload key the decision doesn't declare, or a value of the wrong type
- a missing required payload field or a missing required `param`

The error names the location and suggests a fix:

```text
deploy/production.sigil:27:16: error: unknown field "teir" on type Service
   |
27 |   when service.teir == "critical"
   |                ^^^^
   = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

The policy never loads, so it never gets the chance to be wrong in production. When the error is a type or payload mismatch, the message quotes the relevant signature from the kind so the author doesn't have to go looking for it.

## Absent data follows Go

Strictness about the schema doesn't mean strictness about data. Real inputs have missing map keys all the time: a service without an `owner` label is normal, not an error. So data absence follows Go's rules.

A missing map key yields the zero value of the map's value type. `service.labels["owner"]` on a service without that label is `""`, and `service.labels["owner"] == "payments"` is false, which is almost always what the author meant. If you need to tell "absent" apart from "empty", ask directly with `service.labels has "owner"`.

This is the choice with the sharpest edge in the design, and it's worth being honest about. A zero value can still flow somewhere surprising. The `deploy.production` policy splits a label into a list:

```sigil
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
```

If the `regions` label is missing, the label value is `""`, and Go's `strings.Split("", ",")` returns `[""]`, not an empty list. `[""] all in actor.regions` is false, so the policy fails closed. That's the right outcome here, but it's right by accident of how `split` behaves. The interaction with vacuous `all in` is an [open question](/project/open-questions/).

## Optionals must be unwrapped

Where a Go host uses a pointer field, the kind exposes `?T`, an optional. The compiler won't let you compare or pass a `?T` where a `T` is expected; you have to unwrap it with `??` and say what absence means. Suppose the `Release` type had a `ticket: ?string` field (the `DeployApproval` kind doesn't; this is only an illustration):

```sigil
when (release.ticket ?? "") == "" {
  deny("no_ticket")
}
```

This is the one place Sigil makes the author think about absence explicitly. It's there because a Go pointer is the host saying "nil is meaningful for this field", and the language shouldn't paper over that.

It doesn't yet work for optional structs, since there's no struct literal to put on the right of `??`. That gap is in the [open questions](/project/open-questions/).

## Runtime errors fail closed

Static typing leaves very few ways for evaluation to go wrong: a list index out of range, integer overflow, or an error returned by a host function. When one happens, `Eval` returns the error together with a result that holds the kind's `default` decision.

A host that fails closed can use that result directly without writing its own fallback. A host that wants to surface the error can do that too. What it can't get is a half-evaluated result where some rules ran and others didn't, because that's exactly the silent partial failure the rest of this page is trying to avoid.

## Why not be strict about data too

A language that errors on every missing map key would force `has` checks in front of every label lookup, and policies would drown in them. Authors would start wrapping everything defensively, and the defensive wrappers would be where the bugs hide. Go's zero-value rule is predictable, familiar to every host author, and fails closed for the common "label equals value" check. The schema is the part where silence is dangerous, so that's where Sigil is loud.
