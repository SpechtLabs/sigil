---
title: Decisions
icon: mdi:directions-fork
createTime: 2026/09/24 22:30:00
permalink: /reference/decisions/
---

The rules for writing decision constructors in a policy: where they may appear, the reason and the payload.

```sigil
review(reason: service_owner, approvers: approvers)
//     ^ reason, declared on review in the kind
//                            ^ named payload fields, typed by the kind
```

Every argument is named, the reason included, and named arguments come in any order.

The kind declares each decision, its payload fields and its reasons; see [Kind files](/reference/kind-files/#decision). How candidates become the outcome is on [Evaluation semantics](/reference/evaluation/#resolution), and what the host reads back is [Result](/reference/go-api/#result) in the Go API. Why decisions work this way: [Decisions and reasons](/understanding/decisions/).

## Constructors, not calls

A constructor builds a value. Nothing runs, nothing returns early, and later rules are still evaluated.

Constructors may only appear:

- directly inside a `when` body, as a statement, and
- in the kind's [`default`](#the-default-decision) and [`conflict`](#the-conflict-outcome) declarations.

| Rule                                                                         | Otherwise                           |
| ---------------------------------------------------------------------------- | ----------------------------------- |
| The name is a decision the kind declares                                      | Compile error                       |
| A constructor isn't an expression: it can't be bound with `let`, passed to a function or compared | Compile error   |
| Each constructor that evaluation reaches becomes a candidate                  |                                     |
| A `when` body may hold several constructors; each becomes its own candidate   |                                     |

The bare name without parentheses builds nothing: `approve` on its own is a [`decision` value](/reference/types/#decision), which `assert` conditions compare against [`outcome`](/reference/expressions/#decision-values-and-outcome).

```sigil
when "platform" in actor.groups {
  write(reason: platform_member)
  development_environment_writer(reason: platform_member)
}
```

## The reason

Every constructor names one reason, with `reason:`.

| Rule                                                                                          | Otherwise                                                             |
| --------------------------------------------------------------------------------------------- | --------------------------------------------------------------------- |
| `reason:` is passed exactly once                                                              | Compile error that lists the decision's reasons                       |
| The value is a bare name the kind declares on that decision, never a string or computed       | Compile error with a did-you-mean hint                                |
| The reason is labeled. A positional first argument, `deny(soak_too_short)`, isn't accepted    | Compile error whose help is the labeled call; `sigil fmt` rewrites it |
| `reason:` may come anywhere among the arguments: `approve(bake: 2h, reason: release_manager)` |                                                                       |

- The name resolves against the decision's reasons only, never against the policy's names or the kind's enum values. It takes no qualified form: `reason: approve.release_manager` is a compile error whose help is `reason: release_manager`. `let release_manager = ...` next to `approve(reason: release_manager)` is fine.
- How reasons are declared and scoped: [Kind files](/reference/kind-files/#decision).
- A decision and reason may appear in more than one branch. The trace records each candidate by source position with the conditions that held for it, so the branches stay distinguishable.
- Two candidates with the same decision, reason and payload fold into one outcome. The same decision and reason with different payloads contradict each other; see [Resolution](/reference/evaluation/#resolution).

```sigil
param hotfix_min_soak: duration = 1h

when release.soak < min_soak and not release.hotfix {
  deny(reason: soak_too_short)
}

when release.hotfix and release.soak < hotfix_min_soak {
  deny(reason: soak_too_short)
}
```

```text
deploy/guardrails.sigil:12:16: error: decision deny has no reason `soak_to_short`
   |
12 |   deny(reason: soak_to_short)
   |                ^^^^^^^^^^^^^
   = help: did you mean `soak_too_short`? deny declares: not_eligible, soak_too_short, no_rule_matched

deploy/guardrails.sigil:16:8: error: the reason is a named argument
   |
16 |   deny(soak_too_short)
   |        ^^^^^^^^^^^^^^
   = help: write `deny(reason: soak_too_short)`
```

Why every argument is named: [Why arguments are named](/understanding/language-choices/#why-arguments-are-named).

To carry text computed from the input next to the reason, see [Add text computed from the input](/guides/patterns/#add-text-computed-from-the-input).

## The payload

Every argument other than `reason:` is a payload field.

- Payload arguments are named: `approvers: approvers`, never a bare `approvers`.
- A payload field of an enum type takes a bare value: `tier: critical`; see [Enum values](/reference/expressions/#enum-values).
- Field types, defaults and required-ness come from the kind. A field with a default may be left out; a field without one must be passed.
- A payload key the kind doesn't declare is a compile error, and so is a value of the wrong type.
- Passing the same key twice is a compile error.
- Values are [expressions](/reference/expressions/) over inputs, params, lets and imported lets, evaluated when the rule fires.
- A payload expression that hits a runtime error makes `Eval` return that error; see [Runtime errors](/reference/evaluation/#runtime-errors).
- When a compile error involves a decision's payload, the help lists the decision's reasons and payload fields from the kind.

```text
payments/production.sigil:18:33: error: decision approve has no payload field "bak"
   |
18 |   approve(reason: payments_sre, bak: 15m)
   |                                 ^^^
   = help: did you mean "bake"? approve takes reason: release_manager | payments_sre, and bake: duration = 1h
```

## The default decision

```sigil
default deny(reason: no_rule_matched)
```

The kind's `default` is a constructor with the same reason and payload rules, except that every payload value must be a constant; the declaration is on [Kind files](/reference/kind-files/#default).

## The conflict outcome

```sigil
conflict deny(reason: conflicting_rules)
```

A `collect one` kind may also declare the result of a [conflict](/reference/evaluation/#resolution). It's a constructor with the same rules as the default; the declaration is on [Kind files](/reference/kind-files/#conflict).
