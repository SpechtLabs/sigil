---
title: Decisions
icon: mdi:directions-fork
createTime: 2026/09/24 22:30:00
permalink: /reference/decisions/
---

A decision is the value a policy produces. The kind declares which decisions exist and what data each one carries; a policy builds them with constructors inside `when` bodies. This page gives the rules for writing constructors and describes what the host gets back.

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

The bare name without parentheses is different: `approve` on its own is a [`decision` value](/reference/types/#decision), which `assert` conditions compare against [`outcome`](/reference/expressions/#decision-values-and-outcome). It names the decision and builds nothing.

A `when` body may contain several constructors; each becomes its own candidate. That's what keeps a [collecting kind](#collecting-kinds) readable: granting both `write` and `development_environment_writer` under one condition doesn't need the condition repeated in a second block.

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

- The kind declares at least one reason per decision; a kind with a reasonless decision doesn't load.
- A constructor passes the reason as a bare name, `deny(soak_too_short)`, never as a string and never computed.
- A name the decision doesn't declare is a compile error, with a did-you-mean hint.

A reason is a stable identifier. You can grep a policy repo for it, and you can count by it without cardinality blowing up, and because the set is declared, a dashboard can know every series before the first evaluation:

```text
policy_decisions_total{decision="review", reason="service_owner"}
```

::: tip Count failed evaluations apart
Check the error before you label from the result. When an evaluation fails, with a conflict, a failed assert or a runtime error, the result of a `collect one` kind holds the kind's default, so a counter labelled from it counts every failure under the default, such as `deny/no_rule_matched`, and a policy defect looks like inputs no rule matched. A `collect all` kind returns an empty outcome, and the failure isn't counted at all. Count failures in their own series, labelled by error type, such as `outcome="error", error="conflict"`, and never under the default's reason. The [failed evaluations](/reference/evaluation/#failed-evaluations) table lists what each failure returns, and [Evaluating](/reference/go-api/#evaluating) in the Go API shows how to tell the errors apart.
:::

A typo can't create a new series either, which is what a string reason allowed:

```text
deploy/guardrails.sigil:12:8: error: decision deny has no reason `soak_to_short`
   |
12 |   deny(soak_to_short)
   |        ^^^^^^^^^^^^^
   = help: did you mean `soak_too_short`? deny declares: not_eligible, soak_too_short, no_rule_matched
```

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
   = help: did you mean "bake"? approve is declared as: decision approve(bake: duration = 1h) { release_manager, payments_sre }
```

When a compile error involves a decision's payload, the help quotes the decision's signature from the kind.

## The default decision

The kind names the decision that applies when no rule fires:

```sigil
default deny(no_rule_matched)
```

It's a constructor like any other and follows the same rules: the reason is one the decision declares, and every payload value must be a constant, because there's no rule context to evaluate expressions in. A kind defined in Go sets it with `policy.WithDefault(decision, reason)`, which passes no payload, so every field of that decision needs a default.

A `collect one` kind must declare a default. A collecting kind may leave it out; then an evaluation where nothing fires has an empty outcome.

## What the host gets back

`Eval` returns a `*policy.Result`. For a `collect one` kind it describes the winner:

| Field      | Example               | Meaning                                                                           |
| ---------- | --------------------- | --------------------------------------------------------------------------------- |
| `Decision` | `review`              | Name of the winning decision                                                      |
| `Reason`   | `service_owner`       | Its reason, one of the names the kind declares                                    |
| `Policy`   | `payments.production` | The policy the host evaluated (see the note below)                                |
| `Payload`  | `approvers: [...]`    | The payload by field name, with defaults filled in                                |
| `Outcome`  |                       | The entries the host acts on; for `collect one`, the winner as its single entry   |
| `Trace`    |                       | Every candidate that fired, and for candidates of a decision in the outcome, which conditions held |

Each `Outcome` entry carries `Decision`, `Reason`, `Policy`, `Payload` and the constructor's `Position`. `Trace.Candidates` lists every constructor that fired, winners first, each with the same fields plus `CallChain` and `Conditions`. The full types are in the [Go API](/reference/go-api/#result).

When several candidates are left at the top rank after [resolution](/reference/evaluation/#resolution), a `collect all` kind returns all of them. A `collect one` kind returns a `*ConflictError` along with a result that holds the kind's default; see [Conflicts](#conflicts).

The trace identifies each rule by policy name, reason and source position, so the language needs no separate syntax for naming rules, and two branches with the same reason stay distinguishable. For a rule reached through invocations, the candidate's `CallChain` holds the invocation sites, outermost first, and `Candidate.Location()` renders the whole path, for example `payments/production.sigil:14:3 → deploy/production.sigil:16:5`. When nothing fires, the outcome entry is the kind's default, with an empty `Policy` and an unknown `Position`, and the trace lists no candidates.

When the host evaluates `payments.production` and the `service_owner` review wins, that rule lives in `deploy.production`, which the team policy invokes. The result's `Policy` names the policy the host evaluated, `payments.production`. The outcome entry and the trace candidate name the policy whose rule they came from, `deploy.production`, and the candidate's call chain shows the invocation in `payments.production` that reached it.

On the Go side, `Decision[T].Match` gives typed access to the payload. See the [Go API](/reference/go-api/).

### Collecting kinds

A collecting kind returns every candidate, not a winner: every distinct candidate that fired without `precedence`, or every distinct candidate at the top rank with it. `Result.Outcome` holds the entries, sorted by the kind's declaration order and then by source position, and the single `Decision`, `Reason` and `Payload` fields stay empty. The outcome can be empty. `Decision[T].MatchAll` returns every entry of one decision with typed payloads.

### Conflicts

A `collect one` kind promises one candidate. When resolution leaves several at its top rank, or when candidates from two members of an `exclusive` set fire together under either collect mode, `Eval` returns a `*ConflictError` whose `Candidates` hold only the side that conflicts: the candidates tied at the top rank, or the candidates of the `exclusive` set's members. Other candidates that fired aren't in the error; the result that comes with it has them all in its trace, and holds the kind's default for `collect one` and an empty outcome for `collect all`. A conflict is a defect in the policy, not in the input: two rules claimed outcomes the kind says can't both stand. See [Resolution](/reference/evaluation/#resolution).
