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

## Params and `use`

A policy declares typed parameters:

```sigil
policy deploy.production: DeployApproval

param min_soak: duration = 24h
param approvers: list<string>
param tiers: list<string> = ["standard", "internal"]
```

`approvers` has no default, so it's required. The others have defaults a team can override. A team policy then instantiates the base with `use`:

```sigil
policy payments.production: DeployApproval

use deploy.production(
  min_soak: 4h,
  approvers: ["payments-leads"],
)

when "payments-sre" in actor.teams {
  approve("payments_sre", bake: 15m)
}
```

The compiler checks every binding against the declared type, so `min_soak: "4h"` is a type error pointing at the team file, not a rendering bug discovered in production. Go code can bind params the same way, straight from a CRD or config, without generating any text. See [Policy files](/reference/policy-files/) for the full rules and [Per-team policies](/guides/team-policies/) for a walkthrough.

`use deploy.production(...) as base` also exposes the base policy's `let`s as `base.cleared`, so a team can reuse a matcher instead of copying it. A policy can `use` the same base twice under different aliases, once per environment for example. The `use` graph must be acyclic, and only policies of the same kind can be composed; pulling an access-request policy into `DeployApproval` is a compile error.

If someone runs policies through a text templater anyway, nothing breaks. The grammar ignores whitespace and indentation, every statement starts with a keyword, and trailing commas are legal everywhere. That's a fallback, not the intended path.

## The safety guarantee

Composition is a union of candidates. `use` adds the base policy's rules to the set that gets evaluated; it can't remove a rule, disable one or change what one decides. Combined with [precedence](/understanding/order-independence/), that gives one guarantee:

> A decision the base policy makes explicitly is never downgraded by a composed policy.

If the base denies, the result is deny, whatever the team adds. In the example above, the payments team added an approve for its SRE team, but the base policy's `not_eligible` and `soak_too_short` denies still win over it because `deny` outranks `approve`. The team can widen who gets approved; it can't approve a deploy the base explicitly rejects.

This is what makes it reasonable for a platform team to own the base and let product teams own their instantiations without reviewing every change.

## What the guarantee doesn't cover

Two things sit outside it, and both are easy to miss.

**The kind's default.** When no rule fires, the kind's `default` applies, typically a deny. That default isn't a decision the base made explicitly, so a team rule can turn it into an approve. That's by design: it's exactly how teams add approvals the base didn't anticipate. But it means "the base has no approve rule for X" doesn't imply "X will be denied". If the base wants something denied no matter what teams add, it has to say so with an explicit `deny`.

**Params.** A team binds params, so a team can lower `min_soak` from 24 hours to 4, or to zero, and the base's `soak_too_short` deny moves with it. The union-of-candidates argument doesn't help here because the team didn't add a rule; it changed an input to an existing one. Letting a base policy pin or bound its params, for example `param min_soak: duration = 24h min 1h`, is an [open question](/project/open-questions/).

## Trade-offs

Union-only composition means a team can't carve out an exception to a base deny. If payments genuinely needs a deny lifted, the change belongs in the base policy, reviewed by whoever owns it. Some teams will find that frustrating. The alternative, letting composed policies override base rules, would make every base policy's guarantees conditional on every team's edits, and the whole point of a shared base is that its denies hold.

Tie-breaking is the other rough edge. When a base rule and a team rule both approve with different bake times, the MVP picks the earliest source position, and `use`d policies count as earlier. So the base's `approve("release_manager")` with its default 1h bake wins over the team's 15m. That's predictable but not obviously what a team expects; merge functions in the kind would fix it and are on the [open questions](/project/open-questions/) page.
