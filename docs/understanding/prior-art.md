---
title: Prior art
icon: mdi:bookshelf
createTime: 2026/09/24 22:30:00
permalink: /understanding/prior-art/
---

Sigil borrows heavily. Its closest relative is Cedar, for schema validation and deny-overrides semantics, while the expression layer takes most of its shape from filt-rs. The table sums it up; the sections below say why each choice went the way it did.

| Project                                               | What Sigil takes                                                                                                                                                 | What Sigil avoids                                                                           |
| ----------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------- |
| [filt-rs](https://github.com/SierraSoftworks/filters) | Friendly expression syntax, `in`/`like`/`contains`, durations, single-method object interface, parse once / eval many, errors with line, column and a fix hint | Unknown properties resolving to `null`. Fine for filters, but it makes deny rules fail open |
| [Cedar](https://www.cedarpolicy.com/)                 | Schema-checked policies, forbid overrides permit, policy templates with slots                                                                                    | Its principal/action/resource model is too narrow for arbitrary host inputs                 |
| [CEL](https://github.com/google/cel-go)               | Non-Turing-complete by construction, static cost estimation, host-declared variables and functions                                                               | It's an expression language only, with no notion of rules, decisions or composition         |
| [Rego](https://www.openpolicyagent.org/docs/policy-language) | Nothing syntactic. The lesson is the learning curve                                                                                                       | Datalog semantics, implicit iteration, partial rule sets that engineers struggle to read    |
| HCL / YAML DSLs                                       | Declarative feel                                                                                                                                                 | Block nesting that fights templating, anchors as a reuse mechanism, stringly-typed matchers |

## filt-rs

[filt-rs](https://github.com/SierraSoftworks/filters) is a small Rust filter language, and most of Sigil's expression syntax comes from it. Operators like `in`, `like` and `contains` read naturally to people who've never seen the language, duration literals such as `30m` remove a whole class of unit bugs, and the host exposes data through a single-method interface instead of a reflection-heavy object model. Sigil's `Resolver` for dynamic input is the same idea.

filt-rs also gets error messages right: file, line, column, what went wrong and a concrete fix. Sigil copies that format.

The part Sigil deliberately doesn't copy is unknown properties evaluating to `null`. For a filter, that's a reasonable choice, because a typo just means the filter matches nothing and you notice. In a policy it's dangerous. A deny rule with a typo in its condition never fires, and the request sails through to whatever lower-precedence rule approves it. [Strict schema, forgiving data](/understanding/strictness/) is the long version of that argument.

Sigil also differs on case sensitivity: string comparison is case-sensitive, because Kubernetes labels and most identifiers in this domain are.

## Cedar

[Cedar](https://www.cedarpolicy.com/), from AWS, is the closest thing to Sigil in spirit. Policies validate against a schema before they run, `forbid` always beats `permit`, and policy templates with slots let you stamp out per-tenant variants without copying text.

All three ideas show up in Sigil in a generalised form. The schema becomes the _kind_. Forbid-overrides-permit becomes a `precedence` declaration, so a host can define `deny > review > approve` or any other order its decisions need. Templates with slots become typed `param`s bound through `use`.

What Sigil can't take is Cedar's fixed data model. Every Cedar request is a principal, an action, a resource and a context. That fits access control well and fits "should this hotfix ship to production before it finished soaking in staging" badly. Sigil lets the host define arbitrary typed inputs instead.

## CEL

[CEL](https://github.com/google/cel-go) proved that a non-Turing-complete expression language can be pleasant to use and cheap to embed. Two ideas carry over directly: the host declares the variables and functions an expression can use, and the compiler estimates the worst-case cost of an expression statically so a host can reject expensive ones before they run.

CEL stops at expressions, though. It has no rules, no decisions, no notion of combining several conditions into an outcome and no composition story. Every project that embeds CEL for policies ends up building those pieces around it, which is the same rebuild-it-again problem Sigil exists to stop.

## Rego

[Rego](https://www.openpolicyagent.org/docs/policy-language) contributes nothing syntactic. The lesson it teaches is about learning curves.

Rego is powerful and its Datalog roots make some things elegant, but engineers who don't write it daily struggle with implicit iteration (`some x; input.roles[x] == "admin"`), partial rule sets that merge across files, and the fact that an undefined value makes a whole rule body silently false. Sigil's quantifiers are explicit (`any r in actor.roles: r like "sre-*"`), its rules are independent blocks, and its schema checking turns most "undefined" cases into compile errors.

## HCL and YAML DSLs

The declarative feel is worth keeping: a policy should describe conditions and outcomes, not a procedure. What goes wrong with YAML-based rule engines is everything around that feel. Deeply nested blocks fight text templating, because indentation becomes load-bearing. Anchors and aliases end up as the reuse mechanism, which nobody enjoys debugging. Matchers are strings interpreted at runtime, so `tier: "critical"` and `teir: "critical"` are both valid YAML.

Sigil keeps statements keyword-led and whitespace-insensitive so templating can't break them, replaces anchors with `use` and `let`, and types every matcher against the kind.
