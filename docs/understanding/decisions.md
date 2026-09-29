---
title: Decisions and reasons
icon: mdi:directions-fork
createTime: 2026/09/29 12:00:00
permalink: /understanding/decisions/
---

A boolean is enough for a filter. It isn't enough for a deploy gate. `review` has to say who reviews, `approve` has to say how long the rollout bakes before it widens, and whoever gets paged about a denied deploy wants to know which rule denied it without reading a log line and reverse-engineering the condition. So a Sigil policy produces decisions: values with a name, a reason and a typed payload, all declared by the host in the [kind](/understanding/kinds/).

```sigil
review(reason: service_owner, approvers: approvers)
```

The constructor rules are in [Decisions](/reference/decisions/), and resolution is under [Evaluation semantics](/reference/evaluation/#resolution).

## Constructors, not calls

`deny(reason: soak_too_short)` looks like a function call, but nothing runs. It builds a value, the way `Err("...")` does in Rust. Nothing returns early, and every other rule still gets evaluated. Each constructor that evaluation reaches becomes a _candidate_, and only after every block has run does the evaluator decide which candidates the host gets.

The alternative, a `deny` that stops evaluation like a `return`, would make the first rule reached win, and then the order of rules would decide the outcome. [Why rule order never matters](/understanding/order-independence/) is the long argument against that. Because constructors are values, a `when` body can hold several of them, each its own candidate, which is what keeps a [collecting kind](/reference/kind-files/#collecting-kinds) readable: granting `write` and `development_environment_writer` under one condition doesn't need the condition written twice.

A constructor isn't an expression, though. It can't be bound with `let`, passed to a function or compared. The bare name without parentheses, `approve`, is something else: a decision value that an assert compares against what evaluation produced. [Asserts and decisions](/understanding/asserts/) covers that side.

## Why reasons are declared names

Every constructor names a reason, `reason: soak_too_short`, and the reason is one of a fixed set the kind declares on that decision:

```sigil
decision deny {
  reason: not_eligible | soak_too_short | no_rule_matched
}
```

A reason is a stable identifier. You can grep a policy repository for `soak_too_short` and find every rule that produces it. You can count by it without the label's cardinality blowing up, and because the set is declared, a dashboard can know every series before the first evaluation:

```text
policy_decisions_total{decision="review", reason="service_owner"}
```

A string reason would lose all of that. With strings, a typo creates a new series that nobody's dashboard shows. With declared names, a typo is a compile error:

```text
deploy/guardrails.sigil:12:16: error: decision deny has no reason `soak_to_short`
   |
12 |   deny(reason: soak_to_short)
   |                ^^^^^^^^^^^^^
   = help: did you mean `soak_too_short`? deny declares: not_eligible, soak_too_short, no_rule_matched
```

Declaring reasons in the kind also makes each one a compile-time name that other declarations can point at. A kind ranks the reasons of one decision with a scoped `precedence`, an `exclusive` set can name `approve.release_manager` instead of every approve, and an assert can test `approve.release_manager in outcome`. The Go side gets the same guarantee: a reason handle such as `Deny.Reason("no_rule_matched")` panics at init when the decision doesn't declare that name, so a typo stops the program at start instead of compiling into a comparison that never matches.

The cost is that reasons are part of the contract. Adding one changes the kind, like adding a decision, and removing one is a breaking change. Assert reasons are the exception: they're string literals, for reasons [Asserts and decisions](/understanding/asserts/#why-an-assert-reason-is-a-string) gives. Whether they should be declared too is an [open question](/project/open-questions/#reasons-declared-in-the-kind).

## Why the reason is the decision's own enum

```sigil
decision approve {
  reason: release_manager | payments_sre
  bake: duration = 1h
}
```

To a policy, a decision's reasons behave like an enum: a closed set of bare names, checked at compile time and printed by name. The kind writes them like one, `a | b`, and a constructor passes one like any other field, `reason: release_manager`. What sets the reason apart is where its set lives. It's declared inline, on the decision, and nowhere else.

That's why `reason: Reason`, naming an enum the kind declares, is a compile error. Reasons belong to their decision. `deny` and `approve` may both declare `release_manager`, and they're two names, `deny.release_manager` and `approve.release_manager`, each ranked by its own decision's scoped `precedence` and each named in its own `exclusive` sets. A shared type would tie two decisions' reason sets together: a reason added for `deny` would appear on `approve` as well, and `approve`'s scoped `precedence`, which has to name every reason of `approve`, would stop loading because of a change nobody made to `approve`. With the list inline, a change to one decision's reasons can't reach another decision.

The reason's values also stay out of the policy's namespace, where an enum's values go in. A constructor looks up `reason: release_manager` among its own decision's reasons and nowhere else, so it never needs the namespace to find the name. Leaving reasons out means a policy's `let release_manager` next to `approve(reason: release_manager)` is fine, and a host that adds a reason can't collide with a name some policy already uses. An enum value has no constructor around it: in `let t = critical` nothing but the namespace says where `critical` comes from, so enum values have to live there, and [Enums and versions](/understanding/kinds/#enums-and-versions) covers what that costs a kind that adds one. The declaration rules are under [Kind files](/reference/kind-files/#decision) and the constructor rules under [The reason](/reference/decisions/#the-reason).

## One reason for branches that mean the same thing

A decision and reason can appear in more than one branch. When two different conditions lead to the same outcome, they should share a reason:

```sigil
param hotfix_min_soak: duration = 1h

when release.soak < min_soak and not release.hotfix {
  deny(reason: soak_too_short)
}

when release.hotfix and release.soak < hotfix_min_soak {
  deny(reason: soak_too_short)
}
```

A metric keyed on decision and reason then counts both branches together, which is what you want when they mean the same thing: a release that hasn't soaked long enough. Nothing is lost for auditing. The trace records every candidate by policy, reason and source position, along with the conditions that held for it, so you can always see which branch produced the result, or that both did. That's also why the language has no syntax for naming a rule. The position already identifies it, and two branches with the same reason stay distinguishable.

## Reasons for machines, `detail` for humans

A reason can't be computed. It's always a bare name, never a string built from input, because a reason built from input would bring back the unbounded metric series that declared reasons exist to prevent. When a decision needs text computed from input, such as the service name in a denial message, the host declares an ordinary payload field for it, by convention `detail: string`. The reason is for machines and dashboards; `detail` is for a human reading one specific result. [Add text computed from the input](/guides/patterns/#add-text-computed-from-the-input) shows the pattern.

## Resolution never invents a candidate

Once every rule has run, the evaluator turns the candidates into what the host gets back. [Resolution](/reference/evaluation/#resolution) specifies the four steps: fold equal candidates, check `exclusive` sets, rank, count the top. What they have in common is a design goal. No candidate is ever merged, changed or invented. What the host gets is always something a rule produced, with its reason and position intact, and the only questions the language answers are which candidates count and whether they can stand together.

Folding follows from that goal. Two branches that both build `deny(reason: soak_too_short)` with the same payload produced the same outcome, so the host gets one; the trace still lists both constructors. Anything beyond that is the host's call. If a `collect all` kind gets two `admin` grants with different `ttl`s, the host gets both and decides what two grants of the same role mean. If a deploy kind wants the shorter of two bake times, the host takes it in a few lines of Go over `MatchAll`, in code it can test.

Ties between different reasons of one decision are settled the same way, by a line in the kind and never by position. [Ties within one decision](/understanding/order-independence/#ties-within-one-decision) walks through the release manager and payments SRE example.

## Conflicts are policy defects

Sometimes the candidates can't stand together. Two members of an `exclusive` set fired, or a `collect one` kind, which promises the host one decision, has two candidates at its top rank: the same decision and reason with different payloads, or two reasons the kind didn't rank. Picking one would mean inventing a rule the policy never wrote, so evaluation fails with a conflict instead.

A conflict is a defect in the policy, not in the input: two rules claimed outcomes the kind says can't both stand. That's why the `exclusive` check runs before ranking. A contradiction between two rules doesn't stop being one because a deny happened to fire too and would have outranked both. And it's why a host should count conflicts apart from runtime errors and assert failures, as [Handle failed evaluations](/guides/handle-errors/#tell-the-failures-apart) shows, because each one points at a different problem. Like every failed evaluation, a conflict comes back with a result the host can still act on, and a `collect one` kind can make that result name the conflict instead of claiming no rule matched; [Every failure fails closed](/understanding/strictness/#every-failure-fails-closed) explains what it holds.

## Related

- [Why rule order never matters](/understanding/order-independence/) explains precedence and ties.
- [Asserts and decisions](/understanding/asserts/) covers the other way a policy can say no.
- [Kinds as contracts](/understanding/kinds/) covers how decisions and reasons are declared and ranked.
