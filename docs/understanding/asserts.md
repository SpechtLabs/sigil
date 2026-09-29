---
title: Asserts and decisions
icon: mdi:alert-octagon-outline
createTime: 2026/09/29 12:00:00
permalink: /understanding/asserts/
---

A policy has two ways to say no. A `deny` is a decision: the host gets it back as the outcome and acts on it. A failed `assert` is an error: the evaluation fails, and the host gets an `*AssertionError` together with the kind's default. The two look similar in source, and picking the wrong one either hides a bug or turns an ordinary refusal into an alert.

The syntax is under [`assert`](/reference/policy-files/#assert), and the phases under [Assertions](/reference/evaluation/#assertions).

## When to use an assert instead of a deny

Asserts and decisions answer different questions. A decision is an outcome the author expected and the host acts on, such as denying a deploy that hasn't soaked. A failed assert means something is wrong with the policy, the host or the input, and it should reach whoever owns the evaluation as an error.

```sigil
assert("negative_soak", release.soak >= 0s)

when release.soak < min_soak and not release.hotfix {
  deny(soak_too_short)
}
```

A release that soaked for two hours is a normal fact about the world, and `soak_too_short` is the right answer to it. A release that soaked for minus two hours means the host computed the soak wrong, and no decision about it is trustworthy. The first is a deny. The second is an assert.

An assert can only fail the evaluation, and the host records that as an error, not as a decision.

## Keep caller-trippable asserts rare

Every failed assert lands in the host's error metrics. An assert that input from a caller can trip lets that caller fill those metrics, so keep those asserts rare, and make them mean it. Input a caller can legitimately send is an outcome to expect, and an expected outcome is a decision. [Handle failed evaluations](/guides/handle-errors/#count-failures-in-metrics) shows how to count assert failures apart from decisions.

## Why an assert reason is a string

```sigil
assert("critical_needs_team_label", service.labels has "team")
```

An assert names its reason first, as a decision constructor does. Unlike a decision reason, it's a string literal, not a name the kind declares. An assert belongs to its policy, and the kind has no say in it. The reason is still a stable identifier for metrics and grep, and dynamic text isn't allowed, for the same reason a decision reason can't be computed: see [Decisions and reasons](/understanding/decisions/#why-reasons-are-declared-names). Whether assert reasons should be declared too, and how a failing assert could say which value was wrong, are [open questions](/project/open-questions/#assertions).

## Input asserts and outcome asserts

An assert runs as soon as what it reads is ready. One that reads only the input, params and lets is an _input assert_, checked before any rule runs. One that reads `outcome` is an _outcome assert_, checked once the outcome exists. The checker sorts each assert into its group from the condition alone.

There's no keyword to pick the group. It would repeat what the checker already sees, and the only group that makes sense for an assert is the earliest one it can run in.

### Preconditions

Checking input asserts first is what makes them useful as preconditions. With `assert("critical_needs_team_label", service.labels has "team")` in a critical-service block, an input without the label fails with that reason. Without the assert, a rule reading `service.labels["team"]` would get `""` from the missing key and decide on it, and a rule indexing a list the input left short would fail with a bare index error that says nothing about the input. It also means no rule and no host function call runs on input the policy has declared invalid.

### Every failure, not the first

Every failing assert in a phase is reported, sorted by source position. Stopping at the first failure would make the error depend on evaluation order, which Sigil keeps out of every other result too. A phase that fails ends the evaluation, so a failed input assert hides the outcome asserts, which never get an outcome to check.

The phase also says whose problem the failure is. A failed input assert rejects the caller's input. A failed outcome assert means the policy produced an outcome it forbids itself, which is a defect in the policy. The error's `Phase` field carries that, and a host labels its metrics with it.

## Why only asserts can read `outcome`

`outcome` is the list of decisions the host will get back. A `when` condition or a `let` can't read it, because then a rule could depend on its own result:

```sigil
when admin not in outcome {
  admin(oncall)
}
```

If that compiled, the rule would fire exactly when it doesn't. An assert can read `outcome` safely because it never produces a candidate or changes the outcome, so nothing it concludes can feed back into what it read.

The same restriction applies to decision values: `approve` and `approve.release_manager` as bare names are only values inside an `assert` condition. `when deny == approve` has nothing to say.

## Guardrails for collecting kinds

In a `collect one` kind such as `DeployApproval`, the guardrail is a deny. It outranks every approve, and the host [requires](/understanding/composition/#the-safety-guarantee) the policy that holds it, so no team can gate it.

A [collecting kind](/reference/kind-files/#collecting-kinds) fits decisions that combine instead of competing, such as roles a user can hold at the same time. It returns every decision that fired, nothing outranks anything, and composing policies can only add grants, so it has no deny in the usual sense. A guardrail can't cancel a team's grant. Its guardrails are asserts instead, which fail the evaluation:

```sigil
policy access.guardrails: AccessGrant@1

assert("sod_customer_dev",
  [customer_data_writer, development_environment_writer] exclusive in outcome)
```

`outcome` is the whole root's outcome, including when the assert sits in an invoked policy. That's what lets a required guardrail check what every other policy in the composition granted. Through [`outcome.<decision>`](/reference/expressions/#candidates) it can check what they carry too, such as the approvers of every review. It also means an assert can fail because of a rule in a policy it has never seen, and a guardrail needs exactly that reach. Required with `policy.Require`, the guardrail runs on every evaluation, and in a collecting kind, where nothing outranks anything, a required policy's asserts are the only guardrail there is.

`xor`, `one in` and `exclusive in` exist mostly for asserts like this one. `exclusive in` is mutual exclusion as separation-of-duties rules mean it: holding none of the listed roles is fine, holding two is not.

### Why a guardrail says `all`

A collecting kind can return several candidates of one decision, so a guardrail over candidates quantifies with `all`. For a collecting kind with a `review` decision and a `requestor` input:

```sigil
assert("no_self_review",
  all r in outcome.review: requestor.name not in r.approvers)
```

With `any`, one clean review would hide a self-review next to it. `all` over no candidates is true, which is what a guardrail wants when the decision didn't fire.

Candidates also have no equality and no order a policy can see. The order a collecting kind returns them in falls back to source position, so an assert that read `outcome.review[0]` would change its answer when someone moved a rule. A policy can range over candidates with `any`, `all` or `filter` and read each one's fields, and nothing else.

## `exclusive` in the kind, or an assert in a policy

The separation-of-duties assert above has a second home. A kind can declare the same relation itself:

```sigil
exclusive customer_data_writer, development_environment_writer
```

It's the same relation `exclusive in` tests over `outcome`, but declared by the host in the kind, where no `when` can gate it and no policy has to be required to carry it. Candidates from two members of the set fail the evaluation with a conflict, under either collect mode.

In a `collect all` kind, `exclusive` replaces the pattern of an `exclusive in outcome` assert in a required policy. The assert still works. The difference is who owns the invariant: the assert belongs to a policy author, and the kind line belongs to the host. Use the kind line when the host itself wants two outcomes never to fire together, and an assert for an invariant the platform team owns. Anything richer than mutual exclusion needs an expression, and kind files are pure declarations; whether asserts should move into kinds is [still open](/project/open-questions/#assertions).

## Related

- [Decisions and reasons](/understanding/decisions/) covers what a deny is.
- [Composition without templating](/understanding/composition/) covers how required policies keep guardrails in force.
- [Strict schema, forgiving data](/understanding/strictness/#every-failure-fails-closed) covers what a failed evaluation returns.
