---
title: Design goals
icon: mdi:compass-rose
createTime: 2026/09/24 22:30:00
permalink: /understanding/design-goals/
---

Sigil exists because teams keep rebuilding the same thing: a YAML rule engine with label-selector matchers, a handful of ad-hoc operators, and a Go evaluator that nobody wants to own. Each one starts small and ends up with its own quirks around missing keys, list matching and precedence. Sigil replaces that recurring project with one small language, typed against a contract the host application defines, shipped as an importable Go library in the spirit of [filt-rs](https://github.com/SierraSoftworks/filters).

::: info Design phase
Nothing on this site is implemented yet. These pages are the specification, and the goals below are what every later decision gets measured against.
:::

## Who it's for

Two groups of people touch Sigil, and they want different things.

**Policy authors** are engineers on product or platform teams who need to express "approve this deploy if it soaked in staging and the person shipping it owns the service". They read a policy during a review or at 3am while figuring out why a deploy got denied. They shouldn't need to learn a new programming paradigm to do that.

**Host authors** are the Go developers who embed Sigil in an application. They define what inputs exist, which decisions the application understands and what payload each decision carries. They want compile-time guarantees that a policy can't ask for data they don't provide or return a decision they can't act on.

## Goals

### Readable by any engineer on first contact

A policy should read like the sentence it encodes. Terse syntax is fine; `when release.soak < min_soak { deny("soak_too_short") }` needs no explanation. Rego-style logic programming, where a rule body is a set of unification constraints and iteration happens implicitly, is the thing we're steering away from. People who've used OPA know the pattern: the policy works, but only two people on the team can change it.

This goal wins most arguments about syntax. It's why the draft uses `and`/`or`/`not` instead of `&&`/`||`/`!` (still an [open question](/project/open-questions/)), why there's no `else`, and why decision payload arguments are named.

### Finite and halting by design

No loops, no recursion, no user-defined functions. Every policy terminates, and its worst-case cost is computable before it ever runs. A policy engine sits on a request path; an author who accidentally writes something quadratic shouldn't be able to take the host down. [Halting by construction](/understanding/halting/) covers how the language enforces this.

### Typed against a host-defined contract

The host describes its inputs, functions and decisions in a _kind_. Every policy declares which kind it implements, and the compiler checks every field access, comparison and decision payload against it. A typo like `service.teir` fails when the policy loads, with a file, line, column and a suggestion, instead of silently evaluating to nothing at runtime. [Strict schema, forgiving data](/understanding/strictness/) explains why this matters more for a policy language than for a filter language.

### Decisions carry data and a mandatory reason

A decision is more than a boolean. `review` needs to know who reviews; `approve` needs a bake time before the rollout widens. So decisions are constructors with typed payloads, and every one of them takes a reason as its first argument. The reason is a string literal, which makes it a stable identifier you can grep for, put in a metric label and count. Nobody has to reverse-engineer why a deploy got denied from a boolean and a log line.

### Composable and templatable from day one

Teams want their own version of a shared policy, usually with a different soak time or a different approver list. The usual answer is `text/template` over YAML, which works until someone's indentation breaks a production rule. Sigil makes this a language feature: a policy declares typed `param`s, and a team policy instantiates it with `use`. See [Composition without templating](/understanding/composition/).

### Parse once, evaluate many

A compiled policy is immutable and safe for concurrent use, the same way a compiled `regexp.Regexp` is. Hosts compile at startup or on reload and evaluate on every request from any goroutine. Reloading is a pointer swap.

## Non-goals

### General computation

Sigil isn't Turing complete and never will be. If a rule needs something the language can't express, the host adds a pure function to the kind. That keeps the escape hatch in Go, where it gets reviewed, tested and profiled like the rest of the host's code.

### Evaluators in other languages, for now

Only Go hosts can evaluate policies. Non-Go consumers can read the exported kind file (`deploy_approval.sigil`, say), which is enough to type-check policies in CI or drive an editor, but they can't run them. A WASM build of the evaluator is plausible later; it's parked under [open questions](/project/open-questions/) until the Go library is stable.

### Org-wide authorization

OPA and Cedar solve a different problem: one central authorization service answering "can principal P do action A on resource R" for a whole organisation. Sigil targets decisions embedded in a single application, where the host owns the input shape and the decision vocabulary. If you want a central policy decision point, use one of those.

## When goals conflict

They do, occasionally. Readability and strictness pull against each other when a type error would be more precise but less friendly; the answer there is better error messages, not looser types. Composability and safety pull against each other with params, since a team can lower a base policy's `min_soak`. That one is unresolved and sits under "Pinned params" in the [open questions](/project/open-questions/).

When in doubt, the order is: halting first, then strictness, then readability, then everything else. A policy that's pleasant to read but silently fails open is worse than one that's a bit verbose.
