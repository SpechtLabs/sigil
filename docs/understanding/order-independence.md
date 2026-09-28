---
title: Why rule order never matters
icon: mdi:sort-variant-off
createTime: 2026/09/24 22:30:00
permalink: /understanding/order-independence/
---

In most YAML rule engines, the first matching rule wins. That sounds simple until two teams edit the same file, one of them adds an approve rule near the top, and a deny rule further down stops firing. Nobody changed the deny rule; its position changed what it meant.

Sigil removes that failure mode. Every `when` block gets evaluated, independently and in no particular order, and the highest-precedence decision among all that fire wins. Moving a rule up or down in a file never changes the outcome.

## How a result gets picked

```mermaid
flowchart LR
  A[Input] --> B[Evaluate every<br/>when block]
  B --> C{Any candidates?}
  C -- yes --> D[Pick highest<br/>precedence]
  C -- no --> E[Kind default]
  D --> F[Result + trace]
  E --> F
```

Each decision constructor that evaluation reaches becomes a _candidate_, carrying its decision name, reason, payload and source position. After every block has run, the evaluator looks at the kind's `precedence` declaration and picks the candidate whose decision ranks highest. With `precedence deny > review > approve`, one `deny` beats any number of `approve`s.

If nothing fires, the kind's `default` applies. For most kinds that's a deny, so an input nobody wrote a rule for fails closed.

Here's what that looks like in the deploy gate used throughout these docs. The platform's guardrails deny a deploy that isn't eligible, and its production policy approves critical services for release managers. Condensed into one file, the two rules are:

```sigil
policy deploy.gate: DeployApproval@1

use deploy.common.{cleared, eligible}

when not eligible {
  deny(not_eligible)
}

when cleared {
  when service.tier == "critical"
    and "release_manager" in actor.roles {
    approve(release_manager)
  }
}
```

A release manager shipping a service that isn't managed by Argo CD produces two candidates, `deny(not_eligible)` and `approve(release_manager)`. Deny wins. Swap the two blocks and deny still wins, because the file order was never consulted.

## Nesting is conjunction, not sequence

A nested `when` fires only if every enclosing condition holds. The inner block above is shorthand for `cleared and service.tier == "critical" and ...`. Nesting exists to avoid repeating shared conditions, not to express "check this first, then that". Nothing short-circuits across blocks, and a decision reached in one block doesn't stop evaluation of the others.

That also means decisions don't `return`. `deny(soak_too_short)` builds a value, the way `Err("...")` does in Rust; the host acts on the winner after evaluation finishes.

## Why there's no `else`

`else` only makes sense relative to something that came before it, which quietly reintroduces order. Worse, it hides the negated condition. A reader has to scroll up to learn what `else` means, and a later edit to the `if` changes the `else` without touching it.

Sigil asks you to write the negation out:

```sigil
when eligible {
  review(service_owner, approvers: approvers)
}

when not eligible {
  deny(not_eligible)
}
```

It costs one line. In exchange, each block states its full condition, a reviewer can read any block in isolation, and a diff that changes `eligible` visibly affects both.

## What the trace buys you

Because every block runs, the evaluator can report every candidate, not just the winner. The trace lists each candidate with its policy, reason and source position (the whole call chain, for a rule reached through invocations), and for every candidate of the winning decision it records which conditions held. If two branches produce the same decision and reason, both show up with their own conditions. When someone asks "why was this denied and not approved", the answer is in the trace: the approve fired too, and deny outranked it.

A first-match engine can't give you that. It stopped looking after the first match.

## Ties within one decision

Precedence settles conflicts between different decisions. It doesn't settle ties within one decision: if two `approve` rules fire with different bake times, something has to happen, and picking the earlier one in the file would be the order dependence this page exists to rule out.

Sigil answers with the kind, not with a position. Reasons are declared on each decision and can be ranked there, so `approve(release_manager)` and `approve(payments_sre)` compete the same way `deny` and `approve` do, by a line in the kind. Two candidates with the same decision and reason and the same payload are one outcome. Two with the same reason and different payloads are a contradiction, and the kind says what that means: a `collect one` kind refuses with a conflict error, a `collect all` kind hands both to the host. The [resolution rule](/reference/evaluation/#resolution) is fold, check `exclusive`, rank, count, and no step reads a position.

Take the canonical example, a critical service deployed by someone who is a release manager and also on `payments-sre`. `deploy.production`'s `approve(release_manager)` (bake 1h, the kind's default) and the team's `approve(payments_sre, bake: 15m)` both fire. With `precedence approve: release_manager > payments_sre` in the kind, the release manager's approval wins, wherever the two rules sit. Without that line, the `collect one` deploy kind can't pick, so `Eval` returns a `*ConflictError` naming both candidates, together with the kind's default. A kind that wants the host to see both approvals declares `collect all` instead, and the host takes the shorter bake in a few lines of Go over `Approve.MatchAll`. Either way, moving the team rule above the call changes nothing.

## What you give up

Order independence isn't free. You can't write "try the specific rule, fall back to the general one" as two rules in sequence; you have to make the general rule's condition exclude the specific case, or rely on the fact that the specific rule's decision has higher precedence. Authors coming from first-match engines find this awkward for about a day. After that, never again debugging "why did moving this rule break prod" tends to win them over.
