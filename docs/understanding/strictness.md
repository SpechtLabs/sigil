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
  deny(critical_needs_hotfix_flag)
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
deploy/production.sigil:9:16: error: unknown field "teir" on type Service
  |
9 |   when service.teir == "critical"
  |                ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

The policy never loads, so it never gets the chance to be wrong in production. When a constructor names a payload field the decision doesn't have, or leaves out one it needs, the hint quotes the decision's declaration from the kind, so the author doesn't have to go looking for it:

```text
deploy/production.sigil:11:30: error: decision approve has no payload field "bakes"
   |
11 |     approve(release_manager, bakes: 1h)
   |                              ^^^^^
   = help: did you mean "bake"? approve is declared as: decision approve(bake: duration = 1h) { release_manager, payments_sre }
```

The same check runs in CI. `sigil check` needs only the exported kind file, so a policy repository catches these errors in a pull request, before any host tries to load the policy.

## Inputs are checked too

The CLI applies the same rule to the inputs it reads. `sigil eval` and `sigil test` decode JSON and YAML inputs strictly against the kind, so a fixture with a misspelled key fails instead of quietly testing something else:

```text
Error: testdata/typo.json: service.teir: unknown field "teir" on type Service

What you can do
  • did you mean "tier"? declared: name, tier, owners, labels
  • the input is a JSON object with one key per input the kind declares
```

A key the fixture leaves out is the zero value, as the next section describes. A Go host decodes its requests into its own input struct, with whatever strictness it chooses; by the time Sigil sees the input, it's a typed Go value.

## Absent data follows Go

Strictness about the schema doesn't mean strictness about data. Real inputs have missing map keys all the time: a service without an `owner` label is normal, not an error. So data absence follows Go's rules.

A missing map key yields the zero value of the map's value type. `service.labels["owner"]` on a service without that label is `""`, and `service.labels["owner"] == "payments"` is false, which is almost always what the author meant. If you need to tell "absent" apart from "empty", ask directly with `service.labels has "owner"`.

This is the choice with the sharpest edge in the design. A zero value can still flow somewhere surprising. The `deploy.production` policy splits a label into a list:

```sigil
let cleared = split(service.labels["regions"], ",") all in actor.regions
```

If the `regions` label is missing, the label value is `""`, and Go's `strings.Split("", ",")` returns `[""]`, not an empty list. `[""] all in actor.regions` is false, so the policy fails closed. That's the right outcome here, but it's right by accident of how `split` behaves. The interaction with vacuous `all in` is an [open question](/project/open-questions/#vacuous-all-in).

## Optionals must be unwrapped

Where a Go host uses a pointer field, the kind exposes `?T`, an optional. The compiler won't let you compare or pass a `?T` where a `T` is expected; you have to unwrap it with `??` and say what absence means. Suppose the `Release` type had a `ticket: ?string` field (the `DeployApproval` kind doesn't; this is only an illustration):

```sigil
when (release.ticket ?? "") == "" {
  deny(no_ticket)
}
```

This is the one place Sigil makes the author think about absence explicitly. It's there because a Go pointer is the host saying "nil is meaningful for this field", and the language shouldn't paper over that.

An optional struct has no literal to put on the right of `??`, so its fields are read with [optional chaining](/reference/expressions/#optional-chaining): `release?.soak ?? 0s`. The absence is still explicit, in the `?.` and in the `??` that ends it.

## Why not be strict about data too

A language that errors on every missing map key would force `has` checks in front of every label lookup, and policies would drown in them. Authors would start wrapping everything defensively, and the defensive wrappers would be where the bugs hide. Go's zero-value rule is predictable, familiar to every host author, and fails closed for the common "label equals value" check. The schema is the part where silence is dangerous, so that's where Sigil is loud.

## Every failure fails closed

Static typing leaves very few ways for evaluation to go wrong at run time: a list index out of range, integer overflow, or an error returned by a host function. Add a failed assert, a conflict between candidates and a context that's done, and that's every way an evaluation can fail. Each one returns an error together with a result that holds the kind's `default` decision, or, after a conflict, the conflict outcome a kind may name, as the end of this section explains. For a [collecting kind](/reference/kind-files/#collecting-kinds) the result's outcome is empty instead, even when the kind declares a default, because a default grant on an error would fail open. The [failed evaluations](/reference/evaluation/#failed-evaluations) table lists what each failure returns.

A fallback for every failure means a host that fails closed can use the result directly, without writing a fallback of its own for each error type. A host that wants to surface the error can do that too. What it can't get is a half-evaluated result where some rules ran and others didn't, because that's exactly the silent partial failure the rest of this page is trying to avoid. A runtime error in one rule fails the whole evaluation, not just that rule. And because every block is evaluated, the failure doesn't depend on block order: an input that triggers a runtime error always does.

The trace follows the same rule. After a runtime error, or once the context is done, the trace is empty, even when rules had fired before the failure. The result can't depend on how far the evaluation got before a deadline cut it off, or which rule happened to run first. A conflict or a failed outcome assert is different: every rule has run by then, so the trace lists every candidate, and the host can see which rules claimed what.

A usable fallback has one cost: it looks like a real decision. For `DeployApproval`, a failed evaluation comes back as `deny(no_rule_matched)`, so a metric labelled from the result would count a policy defect as an input no rule matched, and `Deny.Match` reports `true` on it. A host has to check the error before it reads the result. [Handle failed evaluations](/guides/handle-errors/) shows how, and how to count failures apart.

After a conflict, that fallback says something false as well. `no_rule_matched` reports that no rule matched an evaluation where several rules fired and contradicted each other, so a log line or a dashboard that only sees the result points whoever reads it at the wrong problem. A `collect one` kind can therefore name the result of a conflict next to its default:

```sigil
default deny(no_rule_matched)
conflict deny(conflicting_rules)
```

A conflict then comes back as `deny(conflicting_rules)`: still closed, and now honest about what happened. The error and the trace don't change, and the error is still what tells a host a conflict apart from a deny. Only conflicts get it: a runtime error, a failed assert or a done context still falls back to the default.

A collecting kind can't name one. A conflict is a failed evaluation like any other, and a collecting kind returns an empty outcome from every one of them, because granting anything on a defect in the policy would fail open. The declaration is under [`conflict`](/reference/kind-files/#conflict).

## Related

- [Kinds as contracts](/understanding/kinds/) covers where the schema comes from.
- [Asserts and decisions](/understanding/asserts/) covers failing an evaluation on purpose.
- [Halting by construction](/understanding/halting/) covers deadlines.
