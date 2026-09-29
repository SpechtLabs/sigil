---
title: Why the language looks like this
icon: mdi:format-quote-open
createTime: 2026/09/29 12:00:00
permalink: /understanding/language-choices/
---

Most of Sigil's surface syntax comes from a few commitments in the [design goals](/understanding/design-goals/): a policy should read like the sentence it encodes, it should survive a text templater even though nobody should need one, and every construct should have one way to be written and one meaning. The exact rules are in the reference, mainly [Lexical structure](/reference/lexical/), [Grammar](/reference/grammar/) and [Expressions](/reference/expressions/).

## Why words instead of `&&`, `||` and `!`

```sigil
when release.soak < min_soak and not release.hotfix {
  deny(soak_too_short)
}
```

A policy gets read during a review, or at 3am by someone working out why a deploy was denied, and that reader may not write Sigil every day. `and`, `or` and `not` read better than symbols in a condition that runs over several lines, and they pair naturally with the other word operators: `not in`, `all in`, `any in`, `one in`, `exclusive in`, `has`, `like` and `matches`. A condition such as `"admin" not in actor.roles` reads the way it would be said out loud.

Readability wins most arguments about syntax. The same goal explains why there's no `else` (see [Why rule order never matters](/understanding/order-independence/#why-there-s-no-else)) and why payload arguments are named.

## Why newlines and indentation mean nothing

In a YAML rule engine, indentation decides which block a rule belongs to, so a templater that drops a level moves a rule without anyone noticing. Sigil's grammar ignores whitespace entirely. The lexer throws it away, and the parser finds the end of one statement and the start of the next without it.

That works because every statement starts with a keyword (`use`, `param`, `let`, `when`, `assert` and the others), or with a name followed by `(`, which is a decision constructor or a policy invocation. No keyword can continue an expression, and no expression continues with a bare name, so when the parser is inside a `let` and meets `let`, `when` or `guardrails(`, the expression is over. The whole team policy can be joined onto one line and still parse the same:

```sigil
let a = environment == "production" let b = "deployer" in actor.roles guardrails(min_soak: 4h) when a and b { review(service_owner, approvers: approvers) }
```

`sigil fmt` would never write that, but it means nobody can break a policy by indenting it or joining its lines, whatever tool produced them. Templating is still a fallback, not the intended path. Params and invocations are the real answer, and [Composition without templating](/understanding/composition/) explains why.

Some smaller choices keep the lexer this simple. A policy name such as `deploy.common` is three tokens that the parser joins, rejecting whitespace around the dots, so the lexer never needs to know whether it's reading a policy name or a field access. And keywords are allowed wherever a field or payload name goes, after `.`, in a `type` body and as a named argument, because real Go structs have fields called `kind` or `type`. Without that exception, a Go field tagged `type` couldn't be exported to a kind at all. Top-level names still have to be plain identifiers, so the exception never makes a statement ambiguous.

## Why trailing commas and `---`

A trailing comma is legal in every comma-separated list. Adding an approver group then changes one line in a diff, not two, and a templater that emits a comma after every item still produces valid source:

```sigil
production(
  approvers: ["payments-leads", "security-leads"],
  tiers: ["standard"],
)
```

`---` between documents is optional, since the next `policy`, `module` or `kind` header ends a document by itself. It's there because YAML users expect it, and because it makes document boundaries easy to scan in a long file, such as a ConfigMap key holding a team's whole policy tree. `sigil fmt` always writes it, so a bundle still has one canonical form. The price is small: `a---b` no longer means `a - (-(-b))`, and the lexer says so with a hint to put spaces between the minus signs.

## Why one canonical format

The formatter matters more than it looks. A whitespace-insensitive grammar lets styles drift: one team indents continuation lines by two spaces, another by four, a third puts `and` at the end of the line. One canonical form keeps diffs across teams comparable and makes the formatter's output the only style anyone has to learn. Kinds exported from Go come out in the same style, so a generated kind file passes `sigil fmt --check` untouched.

The formatter still follows the author where it can. Line breaks stay where the author put them, the way `gofmt` treats them, and it doesn't add or remove parentheses, with one exception. A quantifier body extends as far right as it can, so

```sigil
any r in actor.roles: r like "sre-*" and eligible
```

means `any r in actor.roles: (r like "sre-*" and eligible)`. Read quickly, it looks like two conditions joined by `and`. So `sigil fmt` adds parentheses around every quantifier or filter body whose top level is `and`, `or` or `xor`. They change nothing about the parse; they show where the body ends. The full style is under [`sigil fmt`](/reference/cli/#sigil-fmt).

## Why arguments are named

```sigil
review(service_owner, approvers: approvers)
guardrails(min_soak: 4h)
```

Decision payloads and policy invocations take named arguments only. Argument order can't cause a bug, because there is no order, and every call site documents itself: a reviewer sees `min_soak: 4h`, not a bare `4h` in second position. The reason is the one positional argument, always first; [Decisions and reasons](/understanding/decisions/) explains why every decision has one.

A param can't be declared optional either. An optional param would just be a param with a default, so the language offers the default and not the second spelling.

Host function calls are the exception. `split(service.labels["regions"], ",")` passes its arguments by position, because a kind's `fn` declaration has no parameter names to bind them to. A Go function's parameter names aren't recoverable by reflection, so a kind exported from Go couldn't carry them. A call also passes a fixed number of arguments, so a kind rejects a variadic Go function.

## Why a name has exactly one meaning

Each policy and module has one flat namespace: the kind's inputs, host functions and decisions, the document's params and lets, and every name bound by a `use`. Any collision is a compile error, and nothing shadows anything. A param called `release` in a kind that declares `input release` doesn't compile, and neither does a quantifier variable named after a param.

The goal is that any name in a policy has one meaning, which a reader can find without knowing any scoping rules. Several smaller rules follow from it:

- There are no wildcard imports. Every name is either defined in the document or listed in a `use`, so a reader can always find where it comes from.
- A [scoped `let`](/reference/policy-files/#scoped-lets) inside a `when` body is visible only there, but its name is still unique across the document. A trace and `sigil explain` can then name every `let` without saying which block it came from.
- A `let` is private unless it's `pub`, in modules and policies alike. A module author can refactor private helpers without breaking anyone who imports the module, and a policy never exports something by accident.

The one exception exists so that a host can add an input without breaking policies that already use that name. [Adding a name never breaks a policy](/understanding/kinds/#adding-a-name-never-breaks-a-policy) explains how the kind version pin makes that safe.

## Why unused names are only warnings

An import nothing uses, or a private `let` nothing reads, is a lint warning, not a compile error, so commenting out a rule while debugging doesn't break the build just because the import it used is suddenly unused. The warning still shows up in `sigil check`, and a repository that wants it enforced can raise it to an error.

That configuration is strict in turn. `sigil.yaml` rejects a lint name, level or key it doesn't know, so a typo can't leave a lint at its default without anyone noticing. The lints and their defaults are in [Lints](/reference/lints/).

## Why the operators refuse to guess

Several operators reject forms that other languages accept, because the accepted form would be easy to misread or would quietly do the wrong thing. Each rejection is a compile error with a suggestion, so the cost is one edit when the policy is written.

**Comparisons don't chain.** `a < b < c`, `a == b == c` and `a in b == c` are errors. Languages disagree about what a chain means. Python reads `a < b < c` as `a < b and b < c`; C reads it as `(a < b) < c`, and `a == b == c` as a comparison of `a == b`'s result with `c`. A reader brings whichever habit they have, so Sigil accepts neither, and the comparison, membership and matching operators, which all share one precedence level, can't be chained at all. Parentheses or an `and` say what was meant.

**`xor` takes two operands.** Chained, `a xor b xor c` would compute parity, true when an odd number of operands are true, which is almost never what someone writing it expects. "Exactly one of several" has its own operator, `[a, b, c] one in xs`.

**`==` doesn't apply to lists, maps or structs.** A policy rarely means "these two lists are identical". It means subset, overlap or membership, and those have their own operators: `all in`, `any in`, `in`. One `==` would also hide a walk over a whole nested value. That rule catches `actor.regions != []` too, so testing a list for emptiness takes a quantifier or a host function; [Test whether a list is empty](/guides/patterns/#test-whether-a-list-is-empty) shows both.

**Strings aren't ordered.** Byte-wise order is well defined, but it makes `"v10" < "v9"` true, which is exactly the comparison a version rule gets wrong. The next section says what to use instead.

**String comparison is case-sensitive.** Kubernetes labels and most identifiers in this domain are case-sensitive, so `"Prod" == "prod"` is false. filt-rs folds case by default; [Prior art](/understanding/prior-art/#filt-rs) compares the two.

**A map key is tested with `has`, never `in`.** `in` already has two meanings, picked by the type on its right: an element of a list, or a substring of a string. `"env" in service.labels` is an error that suggests `service.labels has "env"`, so a key test has one spelling and `in` doesn't gain a third meaning. The cost is the negative form: there's no `not has` operator, so it's `not service.labels has "env"`, which reads the same way only because `not` binds looser than `has`.

**A glob matches strings, not paths.** `like` knows `*` and `?` and nothing else. `*` crosses `/` and `.`, and there are no character classes or escapes, so a glob can't be invalid and never needs a compile error of its own. The cost is precision: `payments-*` also matches `payments-api/v2`, and a glob can't say "one segment" or "digits only". Anything richer belongs in `matches`, an RE2 regular expression, which has its own trap: it matches anywhere in the string unless it's anchored with `^` and `$`.

## Why there's no version type

Strings aren't ordered, so a rule like "deny clients older than 1.4.0" needs something else. A built-in `version` type was considered and rejected. Semantic versioning's order isn't a layout that a format string, in the style of `time.Parse`, could describe: prereleases sort before releases, their identifiers compare numerically or lexically depending on their content, and build metadata is ignored. The language would have to own those rules, and every other scheme's, forever.

So a version comparison is a host function today. The kind declares something like `fn version_below(string, string) -> bool`, the host implements it with whatever Go library it trusts, and [Compare versions](/guides/patterns/#compare-versions) shows the pattern. The planned direction is [host-ordered types](/project/planned/#host-ordered-types), where the host declares an opaque type in the kind and Go supplies its ordering, so `<` can compare versions without the language knowing what a version is.

## Why `d` is exactly 24 hours

A duration literal such as `2d` is 48 hours. It has no calendar or daylight-saving meaning, because the language has no clock or time zone to apply one to. Leaving the clock out is what makes evaluation deterministic: the same compiled policy and input always produce the same result, trace included, and a rule that needs the current time gets it from the host as a `timestamp` input. Replaying an evaluation for a test or an audit then needs only the input that was evaluated. See [Determinism](/reference/evaluation/#determinism).

## Why host function names are written out

A kind declares a host function with an explicit name, `policy.WithFunc("split", strings.Split)`, even though Go could derive one. The name is part of the policy contract, like an input's `policy:"..."` tag. A derived name would let a Go refactor that renames `strings.Split` rename a function in every policy, and function literals, which is how most adapters are written, have no usable name anyway.

There are no built-in functions either. `split`, `len` and every other function exist only when the kind declares them, so what a policy can call is always in the kind file.

## Related

- [Design goals](/understanding/design-goals/) ranks the goals these choices serve.
- [Prior art](/understanding/prior-art/) shows where the syntax came from.
- [Kinds as contracts](/understanding/kinds/) covers the namespace exception and the version pin.
