---
title: Facts, vocabulary and rules
icon: mdi:layers-outline
createTime: 2026/10/01 12:00:00
permalink: /understanding/facts-vocabulary-rules/
---

A deploy gate that honors a change freeze needs three things: which environments are frozen right now, what "frozen" means for the deploy in front of it, and what happens to a frozen deploy. The first changes whenever someone flips a flag, the second almost never, and the third whenever a policy changes. They also belong to different people. Sigil keeps them in three layers, each written in what suits its owner and the rate it changes at.

How to build the middle layer in Go is in [Build policies in Go](/guides/build-policies-in-go/). The [example service](/guides/example-service/) runs all three layers for its change freeze.

## Three layers, three owners

| Layer      | Written in                                                      | Owned by        | Changes                            |
| ---------- | --------------------------------------------------------------- | --------------- | ---------------------------------- |
| Facts      | Go host code that fills an input: flags, a catalog, a schedule  | The service     | Whenever the world changes         |
| Vocabulary | Modules of `pub let`s, by hand or built in Go with `pkg/build`  | The platform    | Rarely, reviewed as rendered Sigil |
| Rules      | Handwritten policies                                            | Every team      | Often, with no Go involved         |

**Facts** are what the host knows when a request arrives. The host resolves them, from a feature-flag service, a service catalog or an on-call schedule, and puts them in a field of the input, the way it already puts the release and the actor there:

```go
type Freeze struct {
	Environments []string `policy:"environments" json:"environments"`
	Unknown      bool     `policy:"unknown" json:"unknown"`
}
```

**Vocabulary** names what the facts mean. A module turns `freeze` into a condition every policy can read by name:

```sigil
module deploy.freeze: DeployApproval@2

pub let is_frozen = freeze.unknown or environment in freeze.environments
```

**Rules** decide. They're written in the vocabulary, so a rule says `is_frozen` and not the expression behind it:

```sigil
when is_frozen {
  deny(reason: change_freeze)
}
```

Each layer can change without the others. A flag flip changes a fact, and no file changes. A platform that decides an unknown freeze should only block production changes the module, in one reviewed diff, and no team policy changes. A team that adds a rule doesn't need to know where the freeze comes from.

## Vocabulary works like a SQL view

A database already splits things this way. Tables hold the facts, which the application writes all day. A view names a query once, `active_customers`, so nobody repeats its `WHERE` clause. Reports select from the view and never need to know which flags make a customer active.

A `pub let` is a view over the input. It holds no data of its own and is computed from each input it's evaluated on. Rules select from it the way reports select from a view. And like a view, it's a contract: when someone drops a column a view exposes, every report that selects it breaks.

The analogy has one limit. A view can be granted to some readers and not others; a `pub let` can be imported by any document of the kind. Who may _define_ the name is what [trusted sources](#vocabulary-is-a-public-api) control.

## Why the live value is data, not part of the module

There's an obvious shortcut once the platform builds its module in Go. The Go code could read the flag itself and render the answer into the module, `pub let is_frozen = environment in ["production"]`, rendering a new one on every flip. Sigil's split puts the live value in the input instead, because the shortcut breaks three things.

**Every flip becomes a policy reload.** A host compiles its bundle once and swaps the compiled policy as a whole. With the freeze in the module, a flag flip means re-rendering, recompiling and swapping, and the freeze is only as current as the last reload that succeeded. A reload that fails for an unrelated reason, such as one team's typo in a shared bundle, also keeps the old freeze in force. With the freeze in the input, the host reads the flag on its own schedule, and the compiled policy doesn't change.

**The rendered file stops being reviewable.** The committed module is what reviewers approve. If the running module is rendered from a flag, the file in the repository and the file in the service differ most of the time, and a review approves neither. A module that reads `freeze.environments` changes only when its meaning changes, so its diff is worth reading.

**Replay breaks.** The same compiled policy and the same input always give the same result, trace included ([Evaluation semantics](/reference/evaluation/)). That's what lets a logged input reproduce a decision with `sigil eval`, for a test or for an audit; deploygate writes the whole input into each decision log line for that reason. With the freeze baked into the module, the logged input doesn't carry it, so replaying a decision from last Tuesday runs today's module and gets today's answer. With the freeze in the input, the logged input has it, and the replay gets last Tuesday's answer.

The input is also where facts are the host's to fill. A fact a caller could set is a fact the caller controls, so the host overwrites the field after it decodes a request; deploygate refuses a request body that carries a `freeze` at all.

## Fail closed where reviewers can see it

A flag service can be down, and then the host doesn't know whether production is frozen. Someone has to decide what that means, and there are two places to do it.

The host could decide in Go, for example by filling `Environments` with every environment it knows when the flag is unreachable. The policy would read `environment in freeze.environments` and never know the difference. But a policy reviewer reading that line would conclude that an outage of the flag service freezes nothing, and the code that says otherwise sits in a Go package they never open.

Sigil's split puts the decision in the vocabulary. The host reports what it knows, including that it doesn't know: `unknown: true` once its last answer is older than a configured staleness. The module says what that means:

```sigil
pub let is_frozen = freeze.unknown or environment in freeze.environments
```

The fail-closed choice is now in the file a policy reviewer reads, in the same language as the rules, and a [policy test](/guides/test-policies/) can pin it with an input whose `unknown` is true.

The host still owns one half of failing closed: it must never turn a value it can't read into an empty freeze. A flag that says `"Production"` when the host knows `production`, or holds a number where a list belongs, is a failed lookup, not "nothing frozen". deploygate counts it as a failed refresh, keeps its last answer, and reports `unknown` once that answer goes stale, so a typo left in the flag service ends up, once the staleness limit passes, as a deny the policy can see.

## When a host function is the better tool

Putting facts in the input works when the host can collect every fact a decision might need, cheaply, before it evaluates. The frozen environments are a short list. Many facts aren't: whether an image digest is signed, whether the actor is in one of a directory's thousands of groups, whether a ticket is approved. The host can't put every answer into every input, and it doesn't know which ones the rules will ask for.

For those, a [host function](/reference/go-api/#kind-options) is the better tool. The kind declares `fn signed(string) -> bool`, the host implements it against the registry, and a rule asks for exactly the digest it cares about.

The price is replay. A host function's answer isn't part of the input, and the trace doesn't record what a host function returned yet, so a logged input no longer reproduces the decision on its own. The kind's contract asks host functions to be pure, the same arguments giving the same result, and a lookup against a live system is only that for as long as the system doesn't change. Recording host-function results in the trace is [planned](/project/planned/#host-function-results-in-the-trace). Until then, prefer an input field whenever the host can fill it, and keep host functions for lookups whose space is too large to prefetch.

## Vocabulary is a public API

Every `pub let` a module exports is a name in other teams' policies. Renaming `is_frozen`, or changing its type, breaks every policy that imports it. That breakage is a compile error, which is the good case: `sigil check` fails in each policy repository, pointing at the import. Changing what the name means without renaming it is worse. If `is_frozen` starts covering `staging`, every rule written against it changes behavior on the next reload, and no team's check notices.

So a vocabulary module deserves the care of a Go package's exported API: add names freely, keep the meaning of the ones that exist, and introduce a new name instead of repurposing an old one. Sigil has no [module versions](/project/planned/#module-versioning) today, but a module name can carry one, and `deploy.freeze.v2` next to `deploy.freeze` lets teams move at their own pace. Tooling that diffs a module's exported names is [planned](/project/planned/#sigil-breaking-for-modules) too.

A vocabulary module also has to come from the platform. Documents resolve by name, so a team that could ship its own `deploy.freeze`, with `pub let is_frozen = false`, would switch the freeze off for itself. [`policy.Trusted`](/reference/go-api/#trusted) loads the platform's vocabulary from a source teams can't write to and reserves every name it defines, even when no required policy imports it; [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source) covers the same problem for guardrails.

## Guardrail or vocabulary

A module can't decide anything. It names conditions, and nothing happens until a rule reads one. Publishing `is_frozen` on its own protects nothing: a team that never writes `when is_frozen` deploys through the freeze.

A freeze every team must honor is therefore a rule, in a guardrail the host [requires](/understanding/composition/#the-safety-guarantee):

```sigil
policy deploy.guardrails: DeployApproval@2

use deploy.freeze.{is_frozen}

when is_frozen {
  deny(reason: change_freeze)
}
```

Vocabulary is what rules are written in, the guardrails' included. A team can read `is_frozen` in its own rules too, and can't change what it means. The test is who has to act: when the platform needs something to hold for every team, it writes the rule into a required guardrail; when each team decides what to do about a fact, the platform publishes the name and leaves the rule to them.

## Why build the vocabulary in Go

A vocabulary module is ordinary Sigil, and writing it by hand works. The platform team that owns the kind in Go can also build it with [`pkg/build`](/reference/go-builder/), next to the kind, for two reasons.

The first is that it reads the input through the Go fields, `build.Field(&in.Freeze.Unknown)`, and gets each path from the `policy` tags when it renders. Renaming a tag changes the rendered module, the drift test fails, and the regenerated file shows the change to every reviewer. The second is that the Go compiler checks what it can: an `Expr[time.Duration]` can't be compared with an `Expr[string]`. The Sigil checker still has the last word, and `build.Check` runs it in the same test.

The rendered file is committed, and reviewers read it, not the builder code. That's the difference from generating a module at run time: the module the service loads is the one in the repository, so the reasons in the previous sections still hold. The alternatives the composition page rejects for rules apply here too. A text template can't be type-checked before it renders ([What goes wrong with text templating](/understanding/composition/#what-goes-wrong-with-text-templating)), and a module that exists only in memory can't be reviewed or replayed.

The cost is two copies, the Go code and the rendered file, that must agree. A test that calls `build.Diff` fails when they don't, and [Catch drift in CI](/guides/build-policies-in-go/#catch-drift-in-ci) sets it up.

## Trade-offs

Each fact family is a field of the input, so the kind grows. Adding the `freeze` input took the example's kind from version 1 to 2; it's an addition, so every policy pinned to `@1` still loads ([Kinds as contracts](/understanding/kinds/#what-the-version-pin-says)).

The host resolves its facts for every request, including requests no rule asks about. For the freeze that's free, because the host refreshes the flag in the background and every request reads the cached answer. For a fact that costs a network call per request, a host function that only runs when a rule needs it can be cheaper.

Facts in the input are as current as the host's cache. How stale a fact may get becomes host configuration, deploygate's `--freeze-max-staleness`, and the policy only sees the consequence, `unknown`. That's the right owner for the question, but it means the policy can't tighten it.

Whoever can change a fact can change decisions. Lifting the freeze is a flag flip, with no policy review, so access to that flag belongs to the same trust model as access to the platform's policies.

## Related

- [Composition without templating](/understanding/composition/) covers modules, imports and required guardrails.
- [Bundles and trust](/understanding/bundles/) covers where the platform's documents come from.
- [Build policies in Go](/guides/build-policies-in-go/) builds a vocabulary module and serves it as a trusted source.
- [Go builder](/reference/go-builder/) lists every function of `pkg/build`.
