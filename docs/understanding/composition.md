---
title: Composition without templating
icon: mdi:layers-triple
createTime: 2026/09/24 22:30:00
permalink: /understanding/composition/
---

Every shared policy eventually needs per-team variants. Payments wants a shorter soak time, the data team wants a different approver group, and nobody wants to maintain five copies of the same eligibility rules. In YAML rule engines the answer is almost always text templating: run the policy through `text/template` or Helm, substitute a few values, and hope the result still parses.

Sigil makes templating a language feature, so nobody has to reach for a text templater to get a per-team policy.

## What goes wrong with text templating

A text templater doesn't know anything about the language it's generating. It will happily substitute a string where a list belongs, drop an indentation level so a rule ends up in the wrong block, or produce a file that parses but means something different. Errors surface after rendering, pointing at line numbers in the generated output that don't exist in any file the author can open. And the template itself can't be type-checked, because it isn't valid policy until someone renders it.

Reuse gets worse over time, too. Once teams copy a rendered policy and edit it by hand, the shared base and the team version drift, and a fix to the base never reaches them.

## Three pieces: modules, params and invocation

Sigil splits reuse along two lines that templating mixes up: sharing names and sharing rules.

**Shared names live in modules.** A module holds typed `let`s and nothing else, so importing from it can never change a decision:

```sigil
module deploy.common: DeployApproval

let owns_service = actor.teams any in service.owners
let cleared =
  split(service.labels["regions"], ",") all in actor.regions
let eligible =
  "deployer" in actor.roles
  and environment == "production"
  and service.labels has {
    "app.kubernetes.io/managed-by": "argocd",
    "platform.example.com/lifecycle": "ga",
  }
```

**Shared rules live in policies with typed params.** The platform team keeps its denies in one policy and its approvals in another:

```sigil
policy deploy.guardrails: DeployApproval

use deploy.common.{eligible}

param min_soak: duration = 24h

when not eligible {
  deny("not_eligible")
}

when release.soak < min_soak and not release.hotfix {
  deny("soak_too_short")
}
```

```sigil
policy deploy.production: DeployApproval

use deploy.common.{cleared, owns_service}

param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]

when cleared {
  when service.tier == "critical"
    and "release_manager" in actor.roles {
    approve("release_manager")
  }

  when service.tier in tiers
    and owns_service {
    review("service_owner", approvers: approvers)
  }
}
```

`approvers` has no default, so it's required. The others have defaults a team can override.

**A team composes them by invocation.** `use` imports a name; calling it like a decision constructor adds the policy's rules with its params bound:

```sigil
policy payments.production: DeployApproval

use deploy.guardrails
use deploy.production
use deploy.common.{cleared}

guardrails(min_soak: 4h)

when service.labels["compliance"] == "pci" {
  production(approvers: ["payments-leads", "security-leads"])
}

when service.labels["compliance"] != "pci" {
  production(approvers: ["payments-leads"])
}

when cleared and "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

The compiler checks every argument against the declared type, so `min_soak: "4h"` is a type error pointing at the team file, not a rendering bug discovered in production. Go code can bind params the same way, straight from a CRD or config, without generating any text. See [Policy files](/reference/policy-files/) for the full rules and [Per-team policies](/guides/team-policies/) for a walkthrough.

An invocation inside a `when` block adds the block's conditions to every rule of the invoked policy, the same way nested `when` blocks do. That's how a team composes a shared policy with conditions of its own: PCI-scoped services above need a second approver group, everything else doesn't. The same policy can be invoked any number of times with different arguments, the invocation graph must be acyclic, and only policies of the same kind can be composed; pulling an access-request policy into `DeployApproval` is a compile error.

If someone runs policies through a text templater anyway, nothing breaks. The grammar ignores whitespace and indentation, every statement starts with a keyword or an invoked policy's name, and trailing commas are legal everywhere. That's a fallback, not the intended path.

## Why `use` only imports

An earlier draft of this design had one keyword do both jobs: `use deploy.production(approvers: [...]) as base` included every rule of the base and exposed its `let`s as `base.cleared`. Review found two problems with that. It was hard to tell what an included policy contributed, because reaching a matcher meant pulling in rules too. And there was no way to compose a policy with extra conditions, as in "these approvals, but only for PCI services".

Splitting the keyword fixes both. `use` never adds a rule, so an import line is always safe to read past. Invocation is a statement, so it goes wherever a decision can go, including inside `when`.

The import syntax follows Rust: path first, then an optional selection, `use deploy.common.{cleared}`. The path comes before the names, so an editor can complete them, and a policy name has one spelling everywhere, with no quoted paths. TypeScript's `import { cleared } from "deploy/common"` puts the names first, and Go's `import c "deploy/common"` has no selective form, so every shared matcher would be qualified. Go's forced qualification does show every name's origin at the use site; the `qualified-imports` lint gives teams that, opt-in.

## The safety guarantee

Composition is a union of candidates. An invocation adds the invoked policy's rules to the set that gets evaluated; it can't remove a rule, disable one or change what one decides. Combined with [precedence](/understanding/order-independence/), a deny that fires always beats an approve that fires.

Gating is what makes invocation useful, and it's also what weakens that. `when false { guardrails() }` switches off every deny in `guardrails`, and a real condition does the same in subtler ways. So the guarantee that a base's denies hold can't come from the language alone any more. It comes from the host:

```go
p, err := Deploy.Load(policies, "payments.production",
	policy.Require("deploy.guardrails"))
```

The compiler checks that `deploy.guardrails` is invoked from the root through top-level invocations only, with no `when` on the path. Move `guardrails(min_soak: 4h)` into either `when` block above and the build fails. With that in place:

> Every candidate a required policy produces is always in the candidate set, so a required policy's deny is never outranked.

If the guardrails deny, the result is deny, whatever the team adds. In the example above, the payments team added an approve for its SRE team, but `not_eligible` and `soak_too_short` still win over it because `deny` outranks `approve`. The team can widen who gets approved; it can't approve a deploy the guardrails explicitly reject.

Protection is explicit now: the host decides which policies are guardrails, instead of every composed policy being protected implicitly. That's what makes it reasonable for a platform team to own the guardrails and let product teams own their compositions without reviewing every change.

## What the guarantee doesn't cover

Three things sit outside it, and all are easy to miss.

**Policies the host doesn't require.** Only required policies are protected. `deploy.production` isn't, so a team can gate it however it likes, or not invoke it at all. That's the point: approvals are what teams tune. A policy that holds denies but isn't required gets the `gated-deny` lint when someone invokes it under `when`, because that's the pattern that silently switches denies off.

**The kind's default.** When no rule fires, the kind's `default` applies, typically a deny. That default isn't a decision any policy made explicitly, so a team rule can turn it into an approve. That's by design: it's exactly how teams add approvals the platform didn't anticipate. But it means "no policy has an approve rule for X" doesn't imply "X will be denied". If the platform wants something denied no matter what teams add, it has to say so with an explicit `deny` in a required policy.

**Params.** A team binds params, so a team can lower `min_soak` from 24 hours to 4, or to zero, and the `soak_too_short` deny moves with it. The union-of-candidates argument doesn't help here because the team didn't add a rule; it changed an input to an existing one. Letting a required policy bound its params, either in the file (`param min_soak: duration = 24h min 1h`) or from the host, is an [open question](/project/open-questions/).

## Seeing the whole picture

Invocation makes composition flexible, so tooling has to make it transparent. [`sigil explain`](/reference/cli/#sigil-explain) flattens a policy into one list of guarded decisions: every invocation inlined, its gates pushed down into each rule's condition, and every param shown as its bound value. The answer to "what does this policy actually do" is one command away, without opening every file it invokes.

## Alternatives considered

- **Denies can't be gated.** Invoking under `when` would gate only approvals and reviews. Rejected as magic: it breaks "nesting is conjunction", the one rule the evaluation model rests on.
- **Inheritance in the header**, as in `policy payments.production: deploy.production(min_soak: 4h)`. Rejected because it reads like class inheritance, allows only one base, and still gives no way to add conditions.
- **The host layers the base itself**, with something like `policy.Base("deploy.guardrails", params)`, so team files never mention it. That's a valid mode for platforms where teams shouldn't see or bind base params. It's deferred rather than rejected, because the team file then no longer shows the whole picture.

## Trade-offs

Union-only composition means a team can't carve out an exception to a required deny. If payments genuinely needs a deny lifted, the change belongs in the guardrails, reviewed by whoever owns them. Some teams will find that frustrating. The alternative, letting composed policies override base rules, would make every guardrail conditional on every team's edits, and the whole point of a guardrail is that its denies hold.

Tie-breaking is the other rough edge. When two rules approve with different bake times, the MVP picks the earliest source position, where an invoked rule's position is its call site first. In `payments.production`, `production(...)` is invoked above the team's own rule, so `deploy.production`'s `approve("release_manager")` with its default 1h bake wins over the team's 15m. That's predictable, and a team can see it by reading its own file top to bottom, but it's not obviously what a team expects; merge functions in the kind would fix it and are on the [open questions](/project/open-questions/) page.
