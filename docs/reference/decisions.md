---
title: Decisions
icon: mdi:directions-fork
createTime: 2026/09/24 22:30:00
permalink: /reference/decisions/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

A decision is the value a policy produces. The kind declares which decisions exist and what data each one carries; a policy builds them with constructors inside `when` bodies.

```sigil
review("service_owner", approvers: approvers)
//     ^ reason         ^ named payload fields, typed by the kind
```

## Constructors, not calls

A decision constructor builds a value the way `Err("...")` does in Rust. Nothing runs, nothing returns early, and later rules still get evaluated. Each constructor that evaluation reaches becomes a candidate, and the host acts on the winning candidate after evaluation finishes. How the winner is picked is on [Evaluation semantics](/reference/evaluation/).

Constructors may only appear:

- directly inside a `when` body, as a statement, and
- in the kind's `default` declaration.

A constructor isn't an expression. It can't be bound with `let`, passed to a function, or compared. The name must be a decision the kind declares; any other name in constructor position is a compile error.

Whether one `when` body may contain several constructors is an [open question](/project/open-questions/).

## Declaring decisions

Decisions come from the kind:

```sigil
decision deny(reason: string)
decision review(reason: string, approvers: list<string>)
decision approve(reason: string, bake: duration = 1h)
```

A kind file declares the decision's name, its reason, and zero or more payload fields with types and optional defaults. See [Kind files](/reference/kind-files/) for the full declaration rules and for `precedence`, which ranks decisions against each other.

## The reason

Every decision's first parameter is `reason: string`. The rules:

- The kind must declare it, first, on every decision. A kind that declares a decision without it is rejected.
- A constructor must pass it, positionally, as the first argument.
- The argument must be a string literal.

A literal reason is a stable identifier. You can grep a policy repo for it, and you can count by it without cardinality blowing up:

```text
policy_decisions_total{decision="review", reason="service_owner"}
```

A computed reason is a compile error:

```text
deploy/production.sigil:34:12: error: decision reason must be a string literal
   |
34 |     review(service.tier, approvers: approvers)
   |            ^^^^^^^^^^^^
   = help: put dynamic text in a `detail` field; declare `detail: string = ""` on decision review in the kind
```

Error messages on this page are illustrative; the exact layout isn't fixed yet.

The linter warns when one policy uses the same reason twice. It doesn't fail compilation, and reasons may repeat across policies.

### Dynamic text: `detail`

When a decision needs text computed from input, the host declares an optional `detail: string` payload field on that decision. Unlike the reason, `detail` accepts any `string` expression:

```sigil
// kind: decision deny(reason: string, detail: string = "")
when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short", detail: service.name)
}
```

`detail` is an ordinary payload field. The only thing special about it is the convention: reason is for machines and dashboards, detail is for humans reading one specific result.

## The payload

Everything after the reason is the payload.

- Payload arguments are named-only: `approvers: approvers`, never a bare `approvers`. Argument order can't cause bugs, and every call site documents itself.
- Named arguments can appear in any order.
- Field types, defaults and required-ness come from the kind. A field with a default may be left out; a field without one must be passed.
- A payload key the kind doesn't declare is a compile error, and so is a value of the wrong type.
- Passing the same key twice is a compile error. (proposed)
- Values are ordinary [expressions](/reference/expressions/) over inputs, params, lets and aliased lets, evaluated when the rule fires. A payload expression that hits a runtime error makes `Eval` return that error.

```text
payments/production.sigil:9:27: error: decision approve has no payload field "bak"
   |
 9 |   approve("payments_sre", bak: 15m)
   |                           ^^^
   = help: approve is declared as: decision approve(reason: string, bake: duration = 1h)
```

When a compile error involves a decision, the message quotes the decision's signature from the kind.

## The default decision

The kind names the decision that wins when no rule fires:

```sigil
default deny("no_rule_matched")
```

It's a constructor like any other and follows the same rules. Every payload value in it must be a constant, because there's no rule context to evaluate expressions in. (proposed)

## What the host gets back

After evaluation, the host receives a result describing the winner:

| Field    | Example              | Meaning                                                  |
| -------- | -------------------- | -------------------------------------------------------- |
| Decision | `review`             | Name of the winning decision                             |
| Reason   | `service_owner`      | Its reason literal                                       |
| Policy   | `payments.production` | The policy that produced it (see the note below)        |
| Payload  | `approvers: [...]`   | The typed payload, with defaults filled in               |
| Trace    |                      | Every candidate, and for the winner, which conditions held |

The trace identifies each rule by policy name, reason and source position, so the language needs no separate syntax for naming rules. When nothing fires, the result holds the kind's default and the trace lists no candidates.

::: warning Unspecified
When the host evaluates `payments.production` and the `service_owner` review wins, that rule lives in the `use`d `deploy.production` base. It's unsettled whether `Policy` names the policy the host evaluated (`payments.production`) or the policy whose rule won (`deploy.production`). The trace carries each candidate's source position either way.
:::

On the Go side, `Decision[T].Match` gives typed access to the payload. See the [Go API](/reference/go-api/).
