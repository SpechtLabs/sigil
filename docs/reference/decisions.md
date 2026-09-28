---
title: Decisions
icon: mdi:directions-fork
createTime: 2026/09/24 22:30:00
permalink: /reference/decisions/
---

::: info Draft specification
This page specifies the language as designed. Syntax, kinds, type checking, rules, decisions, asserts and the evaluation trace are implemented; composition (imports and invocation) and the CLI aren't yet. See [Open questions](/project/open-questions/).
:::

A decision is the value a policy produces. The kind declares which decisions exist and what data each one carries; a policy builds them with constructors inside `when` bodies.

```sigil
review(service_owner, approvers: approvers)
//     ^ reason, declared on review in the kind
//                     ^ named payload fields, typed by the kind
```

## Constructors, not calls

A decision constructor builds a value the way `Err("...")` does in Rust. Nothing runs, nothing returns early, and later rules still get evaluated. Each constructor that evaluation reaches becomes a candidate. After evaluation finishes, the host acts on the winning candidate, or, for a [collecting kind](/reference/kind-files/#collecting-kinds), on every candidate. How the outcome is formed is on [Evaluation semantics](/reference/evaluation/).

Constructors may only appear:

- directly inside a `when` body, as a statement, and
- in the kind's `default` declaration.

A constructor isn't an expression. It can't be bound with `let`, passed to a function, or compared. The name must be a decision the kind declares; any other name in constructor position is a compile error.

The bare name without parentheses is different: `approve` on its own is a [`decision` value](/reference/types/#decision), which `assert` conditions compare against [`outcome`](/reference/expressions/#decision-values-and-outcome). It names the decision and builds nothing. (proposed)

A `when` body may contain several constructors; each becomes its own candidate. See [Multiple decisions per block](/project/open-questions/#multiple-decisions-per-block) for why.

## Declaring decisions

Decisions come from the kind:

```sigil
decision deny {
  not_eligible
  soak_too_short
  no_rule_matched
}
decision review(approvers: list<string>) {
  service_owner
}
decision approve(bake: duration = 1h) {
  release_manager
  payments_sre
}
```

A kind file declares the decision's name, its payload fields with types and optional defaults, and the reasons it can be constructed with. See [Kind files](/reference/kind-files/) for the full declaration rules, for `collect one` with `precedence`, which ranks decisions against each other and returns the winner, for `collect all`, which applies all of them, and for `exclusive`, which names outcomes that can't fire together.

## The reason

Every constructor names a reason first, positionally, and the reason is one of the names the kind declares on that decision. The rules:

- The kind declares at least one reason per decision. A decision without reasons has no valid constructor.
- A constructor passes the reason as a bare name, `deny(soak_too_short)`, never as a string and never computed.
- A name the decision doesn't declare is a compile error, with a did-you-mean hint.

A reason is a stable identifier. You can grep a policy repo for it, and you can count by it without cardinality blowing up, and because the set is declared, a dashboard can know every series before the first evaluation:

```text
policy_decisions_total{decision="review", reason="service_owner"}
```

A typo can't create a new series either, which is what a string reason allowed:

```text
deploy/production.sigil:16:10: error: decision deny has no reason `soak_to_short`
   |
16 |     deny(soak_to_short)
   |          ^^^^^^^^^^^^^
   = help: did you mean `soak_too_short`? deny declares: not_eligible, soak_too_short, no_rule_matched
```

Error messages on this page are illustrative; the exact layout isn't fixed yet.

A decision and reason may appear in more than one branch. When two different conditions lead to the same outcome, give both the same reason:

```sigil
param hotfix_min_soak: duration = 1h

when release.soak < min_soak and not release.hotfix {
  deny(soak_too_short)
}
when release.hotfix and release.soak < hotfix_min_soak {
  deny(soak_too_short)
}
```

A metric keyed on decision and reason counts both branches together, which is what you want when they mean the same thing. Auditability doesn't suffer, because the trace records every candidate by source position along with the conditions that held for it, so you can always see which branch, or both, produced the result. Two branches that build the same decision, reason and payload are one outcome; two that build the same reason with different payloads contradict each other, and [Resolution](/reference/evaluation/#resolution) says what happens then.

Reasons are scoped to their decision, so `deny` and `approve` may both declare `release_manager`. As values they're `deny.release_manager` and `approve.release_manager`, and nothing is shared between them.

### Dynamic text: `detail`

When a decision needs text computed from input, the host declares an optional `detail: string` payload field on that decision. Unlike the reason, `detail` accepts any `string` expression:

```sigil
// kind: decision deny(detail: string = "") { soak_too_short ... }
when release.soak < min_soak and not release.hotfix {
  deny(soak_too_short, detail: service.name)
}
```

`detail` is an ordinary payload field. The only thing special about it is the convention: reason is for machines and dashboards, detail is for humans reading one specific result.

## The payload

Everything after the reason is the payload.

- Payload arguments are named-only: `approvers: approvers`, never a bare `approvers`. Argument order can't cause bugs, and every call site documents itself.
- Named arguments can appear in any order.
- Field types, defaults and required-ness come from the kind. A field with a default may be left out; a field without one must be passed.
- A payload key the kind doesn't declare is a compile error, and so is a value of the wrong type.
- Passing the same key twice is a compile error.
- Values are ordinary [expressions](/reference/expressions/) over inputs, params, lets and imported lets, evaluated when the rule fires. A payload expression that hits a runtime error makes `Eval` return that error.

```text
payments/production.sigil:18:27: error: decision approve has no payload field "bak"
   |
18 |   approve(payments_sre, bak: 15m)
   |                           ^^^
   = help: approve is declared as: decision approve(bake: duration = 1h) { release_manager, payments_sre }
```

When a compile error involves a decision, the message quotes the decision's signature from the kind.

## The default decision

The kind names the decision that applies when no rule fires:

```sigil
default deny(no_rule_matched)
```

It's a constructor like any other and follows the same rules: the reason is one the decision declares, and every payload value must be a constant, because there's no rule context to evaluate expressions in.

A collecting kind may leave the default out; then an evaluation where nothing fires has an empty outcome.

## What the host gets back

After evaluation, the host receives a result describing the winner:

| Field    | Example              | Meaning                                                  |
| -------- | -------------------- | -------------------------------------------------------- |
| Decision | `review`             | Name of the winning decision                             |
| Reason   | `service_owner`      | Its reason, one of the names the kind declares           |
| Policy   | `payments.production` | The policy the host evaluated (see the note below)      |
| Payload  | `approvers: [...]`   | The typed payload, with defaults filled in               |
| Trace    |                      | Every candidate, and for each one sharing the winner's decision, which conditions held |

When several candidates survive [resolution](/reference/evaluation/#resolution), a `collect all` kind returns all of them and a `collect one` kind fails with a conflict error instead of a result; see below.

The trace identifies each rule by policy name, reason and source position, so the language needs no separate syntax for naming rules, and two branches with the same reason stay distinguishable. For a rule reached through invocations, the position is the full call chain, for example `payments/production.sigil:14:3 → deploy/production.sigil:16:5`. When nothing fires, the result holds the kind's default and the trace lists no candidates.

When the host evaluates `payments.production` and the `service_owner` review wins, that rule lives in `deploy.production`, which the team policy invokes. The result's `Policy` names the policy the host evaluated, `payments.production`, and the outcome entry and every trace candidate name the policy whose rule they came from, `deploy.production`, along with the call chain. See [What the result's `Policy` field names](/project/open-questions/#what-the-result-s-policy-field-names).

On the Go side, `Decision[T].Match` gives typed access to the payload. See the [Go API](/reference/go-api/).

### Collecting kinds

A collecting kind returns every candidate, not a winner: every candidate that fired without `precedence`, or every candidate at the top rank with it. The result holds a list of entries, each with the decision, reason, policy and payload fields from the table above, sorted by the kind's declaration order and then by source position. It can be empty. `Decision[T].MatchAll` returns every entry of one decision with typed payloads.

### Conflicts

A `collect one` kind promises one candidate. When resolution leaves several at the top rank, or when candidates from two members of an `exclusive` set fire together, the evaluation fails with a `*ConflictError` naming the candidates on each side, and the host gets the kind's default. A conflict is a defect in the policy, not in the input: two rules claimed outcomes the kind says can't both stand. See [Resolution](/reference/evaluation/#resolution).
