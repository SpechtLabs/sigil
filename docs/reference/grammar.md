---
title: Grammar
icon: mdi:code-braces
createTime: 2026/09/24 22:30:00
permalink: /reference/grammar/
---

The complete syntax of Sigil source files and the three kinds of document they hold: policies, modules and kinds.

A file uses the `.sigil` extension and may hold several documents, and each document's header keyword (`policy`, `module` or `kind`) decides which grammar applies to it. This page covers what parses; what type-checks is on the other reference pages. The grammar needs one token of lookahead everywhere except at the start of a call argument, where it needs two (see [Calls](#calls)).

## Notation

The grammar uses the W3C EBNF notation from the XML specification:

| Notation      | Meaning                               |
| ------------- | ------------------------------------- |
| `A ::= ...`   | Production                            |
| `"when"`      | Literal token                         |
| `A B`         | Sequence                              |
| `A \| B`      | Alternative                           |
| `A?`          | Optional                              |
| `A*`          | Zero or more                          |
| `A+`          | One or more                           |
| `[a-z]`       | Character class (lexical rules only)  |
| `[^x]`        | Any character except `x`              |
| `A - B`       | `A` but not `B`                       |
| `/* ... */`   | Comment                               |

Whitespace and comments may appear between any two tokens. The parser ignores both, so none of the productions mention them. Comments are still tokens; see [Whitespace and comments](/reference/lexical/#whitespace-and-comments).

## Lexical grammar

```text
Ident       ::= [A-Za-z_] [A-Za-z0-9_]* - Keyword
Keyword     ::= "policy" | "module" | "use" | "as" | "param" | "let" | "pub"
              | "when" | "assert"
              | "kind" | "version" | "enum" | "type" | "input" | "fn" | "decision"
              | "precedence" | "collect" | "default" | "conflict"
              | "and" | "or" | "xor" | "not" | "in" | "all" | "any" | "filter"
              | "one" | "exclusive" | "has" | "like" | "matches" | "present"
              | "true" | "false" | "outcome"

Int         ::= [0-9]+
Float       ::= [0-9]+ "." [0-9]+
Duration    ::= ( [0-9]+ Unit )+          /* units unique, largest first */
Unit        ::= "ms" | "s" | "m" | "h" | "d"

String      ::= '"' ( [^"#x5C#xA] | Escape )* '"'
Escape      ::= "\" EscapeBody     /* any escape Go accepts in an
                                     interpreted string literal */
RawString   ::= "`" [^`]* "`"

Comment     ::= "//" [^#xA#xD]*
Separator   ::= "---"
```

The lexer takes the longest match. A run of digits followed directly by a unit is a `Duration`; `ms` wins over `m` followed by `s`. A number followed directly by any other letter or `_` is a lexical error, and so is a `Float` followed by a unit (`1.5h`). `??`, `?.`, `==`, `!=`, `<=`, `>=`, `->` and `---` are single tokens. `|` is a token of its own, used only in kind files. A float needs digits on both sides of its point, so `?.` never splits into `?` and a number.

`Ident` excludes keywords, but field names don't: see `Name` below.

## Source files

```text
SourceFile   ::= Separator* ( Document ( Separator* Document )* Separator* )?
Document     ::= PolicyDoc | ModuleDoc | KindDoc
```

A document ends where the next one's header starts, so the `Separator` between two documents is optional. `sigil fmt` writes exactly one between each pair and none before the first or after the last. See [Bundles](/reference/bundles/) for how documents from several files form one bundle.

A header keyword only starts a document at top-level statement position: outside every pair of braces, parentheses and brackets, where the parser expects the next statement. Anywhere else, `policy`, `module` and `kind` are names like any other keyword (see [Keywords as field names](#keywords-as-field-names)). None of these end a document:

```sigil
kind JIT_Approval version 1

type Resource {
  kind: string // field declaration inside a type body
  policy: string
  labels: map<string, string>
}

input resource: Resource

decision review {
  reason: cluster_access
  approvers: list<string>
}

decision deny {
  reason: not_eligible | soak_too_short | no_rule_matched
}

collect one
precedence deny > review

default deny(reason: no_rule_matched)

---

policy jit.sandbox: JIT_Approval@1

when resource.kind == "kube_cluster" // field access after `.`
  and resource.policy != "" {
  review(reason: cluster_access, approvers: ["sre-leads"])
}
```

The first `type` body is the case to watch: `kind:` sits at the start of a line, where a statement could begin, but it's inside braces, so it's a field.

## Policy files

```text
PolicyDoc    ::= PolicyHeader UseStmt* PolicyStmt*
PolicyHeader ::= "policy" PolicyName ":" Ident ( "@" Int )?   /* the checker requires the pin */
PolicyName   ::= Ident ( "." Ident )*     /* no whitespace around "." */

PolicyStmt   ::= ParamStmt | LetStmt | WhenStmt | AssertStmt | Call

UseStmt      ::= "use" PolicyName ( "as" Ident | "." "{" ImportList "}" )?
ImportList   ::= ImportItem ( "," ImportItem )* ","?
ImportItem   ::= Ident ( "as" Ident )?

ParamStmt    ::= "param" Ident ":" Type ( "=" Expr )? ( "," Bound )*
Bound        ::= ( "min" | "max" ) ":" Expr     /* identifiers, not keywords */
LetStmt      ::= "pub"? "let" Ident "=" Expr
WhenStmt     ::= "when" Expr "{" RuleItem* "}"
RuleItem     ::= WhenStmt | LetStmt | AssertStmt | Call
AssertStmt   ::= "assert" "(" String "," Expr ","? ")"

Call         ::= Ident "(" CallArgs? ")"
CallArgs     ::= NamedArgs
               | Expr ( "," NamedArg )* ","?       /* old positional reason; the checker rejects it */

NamedArgs    ::= NamedArg ( "," NamedArg )* ","?
NamedArg     ::= Name ":" Expr
Name         ::= Ident | Keyword          /* field and payload names */
```

A `Call` is either a decision constructor or a policy invocation, and the parser doesn't need to know which. The checker decides by the name:

| Name is                | `Call` is            | Arguments                                                                                              |
| ---------------------- | -------------------- | ------------------------------------------------------------------------------------------------------ |
| A decision of the kind | Decision constructor | All named, in any order: `reason:` with one of the decision's declared reasons, and the payload fields |
| An imported policy     | Policy invocation    | All named                                                                                              |
| Anything else          | Compile error        |                                                                                                        |

The parser still accepts a positional first argument, the old way to pass the reason. The checker rejects it with the labeled call as the fix, and `sigil fmt` rewrites it to `reason:` placed first. The value of `reason:` may be any expression to the parser; the checker requires a bare reason name, so `deny(reason: "soak_too_short")` parses and then fails with a hint to drop the quotes.

A `LetStmt` in a `RuleItem` is a scoped let and can't be `pub`; the parser reports a `pub` there and keeps the let, so its uses still resolve. That scoped let names are unique per document, and that a `pub let` in a policy can't read a param, are checked after parsing. See [Scoped lets](/reference/policy-files/#scoped-lets).

An `AssertStmt` takes its reason first and unlabeled, unlike a decision constructor, and the reason is a plain string literal, never a raw string. The `)` ends the condition, including a quantifier body that would otherwise run on. An assert takes nothing else: no named arguments. See [Assertions](/reference/evaluation/#assertions).

`UseStmt`s come before every other statement. A `use` after a `param`, `let`, rule or invocation is a parse error with a hint to move it up.

In `use deploy.common.{cleared}`, the `.` before `{` follows the last path segment directly, like the dots inside the path.

## Module files

```text
ModuleDoc    ::= ModuleHeader UseStmt* LetStmt*
ModuleHeader ::= "module" PolicyName ":" Ident ( "@" Int )?   /* the checker requires the pin */
```

A module contains nothing but imports and `let`s. A `param`, `when`, `assert` or call in a module is a parse error with a hint that it belongs in a policy.

## Kind files

```text
KindDoc      ::= KindHeader KindStmt*
KindHeader   ::= "kind" Ident "version" Int ( "," "accepts" ":" Int )?   /* "accepts" is an identifier */

KindStmt     ::= EnumDecl | TypeDecl | InputDecl | FnDecl | DecisionDecl
               | PrecedenceDecl | ExclusiveDecl | CollectDecl | DefaultDecl
               | ConflictDecl

EnumDecl     ::= "enum" Ident ":" Ident ( "|" Ident )*
TypeDecl     ::= "type" Ident "{" FieldDecl* "}"
FieldDecl    ::= Name ":" Type
InputDecl    ::= "input" Ident ":" Type
FnDecl       ::= "fn" Ident "(" ( Type ( "," Type )* ","? )? ")" "->" Type
DecisionDecl ::= "decision" Ident "{" DecisionField* "}"
               | "decision" Ident ( "(" ( PayloadField ( "," PayloadField )* ","? )? ")" )? "{" Ident+ "}"
                                                      /* old form; the kind loader rejects it */
DecisionField ::= ReasonField | PayloadField
ReasonField  ::= "reason" ":" Ident ( "|" Ident )*    /* "reason" is an identifier */
PayloadField ::= Name ":" Type ( "=" Expr )?
PrecedenceDecl ::= "precedence" ( Ident ":" )? Ident ( ">" Ident )*   /* "d:" scopes it to d's reasons */
ExclusiveDecl ::= "exclusive" Outcome ( "," Outcome )+
Outcome      ::= Ident ( "." Ident )?                 /* a decision, or one of its reasons */
CollectDecl  ::= "collect" ( "one" | "all" )
DefaultDecl  ::= "default" Call
ConflictDecl ::= "conflict" Call
```

::: warning Planned
`type Version ordered`, for [host-ordered types](/project/planned/#host-ordered-types), doesn't parse yet.
:::

That an enum's values are unique, that a decision has exactly one `ReasonField` and that it doesn't name a type, that a kind declares `collect` once, that `collect one` comes with a `precedence` over decisions, that `precedence` names every decision (or every reason of its decision) once, that an `ExclusiveDecl` names declared outcomes, that defaults, and the payloads of `default` and `conflict`, are constants, and that only a `collect one` kind declares a `conflict`, at most once, are semantic rules, checked after parsing. See [Kind files](/reference/kind-files/).

## Types

```text
Type         ::= "?" BaseType | BaseType
BaseType     ::= "list" "<" Type ">"
               | "map" "<" Type "," Type ">"
               | Ident                    /* bool, int, ..., a struct type or an enum */
```

`??T` doesn't parse, so optionals don't nest. Optional types only appear in kind files in practice, since params can't be optional.

## Expressions

```text
Expr         ::= OrExpr
OrExpr       ::= AndExpr ( ( "or" AndExpr )+ | "xor" AndExpr )?   /* no mixing */
AndExpr      ::= NotExpr ( "and" NotExpr )*
NotExpr      ::= "not" NotExpr
               | Quantifier
               | Filter
               | RelExpr
Quantifier   ::= ( "any" | "all" ) Ident "in" Coalesce ":" Expr
Filter       ::= "filter" Ident "in" Coalesce ":" Expr
RelExpr      ::= Coalesce ( RelOp Coalesce )?          /* non-associative */
RelOp        ::= "==" | "!=" | "<" | "<=" | ">" | ">="
               | "in" | "not" "in" | "all" "in" | "any" "in"
               | "one" "in" | "exclusive" "in"
               | "has" | "like" | "matches"
Coalesce     ::= Additive ( "??" Coalesce )?          /* right-associative */
Additive     ::= Unary ( ( "+" | "-" ) Unary )*
Unary        ::= ( "-" | "present" ) Unary | Postfix
Postfix      ::= Primary ( "." Name | "?." Name | "[" Expr "]" | "(" Args? ")" )*
Args         ::= Expr ( "," Expr )* ","?

Primary      ::= Literal | Ident | "outcome" | "(" Expr ")" | ListLit | MapLit
Literal      ::= "true" | "false" | Int | Float | Duration | String | RawString
ListLit      ::= "[" ( Expr ( "," Expr )* ","? )? "]"
MapLit       ::= "{" ( MapEntry ( "," MapEntry )* ","? )? "}"
MapEntry     ::= Coalesce ":" Expr
```

Syntax alone accepts a few things the checker rejects:

| Construct                                 | Parses                            | Rejected by the checker with                                                                                              |
| ----------------------------------------- | --------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| `outcome`                                 | Anywhere an expression can appear | A compile error outside an `assert` condition                                                                             |
| Call expression `f(...)`                  | On any postfix expression         | A compile error unless `f` is a host function name                                                                        |
| Call statement                            | On any identifier                 | A compile error unless the name is a decision or an imported policy                                                       |
| Pattern after `like` or `matches`         | Any expression                    | A compile error unless it's a string literal                                                                              |
| `reason:` value                           | Any expression                    | A compile error unless it's a declared reason name                                                                        |
| Positional reason, `deny(soak_too_short)` | Yes                               | A compile error whose help is the labeled call; `sigil fmt` rewrites it                                                   |
| `decision name(fields) { reasons }`       | Yes                               | A kind loader error whose help is the new form; `sigil fmt` rewrites it                                                   |
| Header without `@N`                       | Yes                               | A compile error that suggests the kind's current version                                                                  |
| `.name`                                   | On any postfix expression         | A compile error unless the left side is a struct, a decision in an `assert`, a whole import, or an enum (`Tier.critical`) |

A `MapEntry` key is an expression like any other, so the `team` in `{team: "payments"}` is a name, not a string. When no name `team` is declared, the error's help suggests `"team"`, the mirror of the hint that drops the quotes from a constructor reason.

## Operator precedence

The expression grammar encodes this table, lowest to highest. It matches [Expressions](/reference/expressions/).

| Level | Operators                   | Associativity  |
| ----- | --------------------------- | -------------- |
| 1     | `or`                        | left           |
| 1     | `xor`                       | none           |
| 2     | `and`                       | left           |
| 3     | `not`                       | prefix         |
| 4     | `==` `!=` `<` `<=` `>` `>=` | none           |
| 4     | `in`, `not in`              | none           |
| 4     | `all in`, `any in`          | none           |
| 4     | `one in`, `exclusive in`    | none           |
| 4     | `has`                       | none           |
| 4     | `like`, `matches`           | none           |
| 5     | `??`                        | right          |
| 6     | `+` `-`                     | left           |
| 7     | `-`, `present`              | prefix         |
| 8     | `.field` `?.field` `[key]` `f(args)` | left (postfix) |

`or` and `xor` share level 1, but `OrExpr` takes either a chain of `or` or a single `xor`, never both, so `a xor b xor c` and `a or b xor c` fail to parse with a hint to add parentheses.

A quantifier or a filter sits at level 3 as an alternative to `not`. Its range is parsed at level 5, and its body is a full `Expr`, so the body extends as far right as the enclosing construct allows.

## How the parser decides

### Quantifier or operator

`all` and `any` have two jobs. `all r in xs: body` is a quantifier; `a all in b` is the subset operator. The parser tells them apart by position, with no extra lookahead:

- Where an operand is expected (the start of an expression, after `and`, `or`, `not`, or `(`), `all` or `any` starts a quantifier and must be followed by an identifier, `in`, a range and `:`.
- Where an operator is expected (right after a complete operand), `all` or `any` must be followed by `in` and forms the binary operator.

`not` works the same way. At operand position it's unary negation; at operator position it must be followed by `in` and forms `not in`.

`one` and `exclusive` only exist at operator position, followed by `in`. At operand position they're a parse error; there's no `one x in xs: ...` quantifier.

```sigil
all r in actor.roles: r != "admin"      // quantifier: `all` at operand position
actor.roles all in allowed_roles       // operator: `all` after the operand `actor.roles`
not eligible                           // unary not
"admin" not in actor.roles             // `not in` after an operand
```

`filter` has one job: it only starts a filter, at operand position, and there's no `filter in` operator.

Because the quantifier and filter alternatives live at level 3, `x == all r in xs: p` and `x in filter r in xs: p` don't parse. Put the quantifier or filter in parentheses.

### Where a quantifier or filter body ends

The body is an `Expr`, so it takes every `and` and `or` that follows:

```sigil
any r in actor.roles: r like "sre-*" and eligible
// is
any r in actor.roles: (r like "sre-*" and eligible)
```

It stops at a token that can't continue an expression: `)`, `]`, `}`, `,`, `{` in operator position, or a statement keyword. So in `assert("no_root", all g in grants: g != "root")` the body ends at `)`, and in a list literal or an argument list it ends at the next `,`.

### Statement boundaries

Newlines never end anything. Every top-level statement starts with one of these:

| Document | Statement starters                                                                                                                       |
| -------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Policy   | `policy`, `use`, `param`, `let`, `pub`, `when`, `assert`, or an identifier followed by `(` (a decision constructor or policy invocation) |
| Module   | `module`, `use`, `let`, `pub`                                                                                                            |
| Kind     | `kind`, `enum`, `type`, `input`, `fn`, `decision`, `precedence`, `collect`, `default`, `conflict`                                        |

- None of those keywords can continue an expression, and an expression never continues with a bare identifier. When the parser is inside a `let` expression and meets `let`, `when` or `guardrails(`, the expression is over.
- A header keyword or a `---` ends the whole document the same way.
- No other statement starts with an identifier.

Why: [Why the language looks like this](/understanding/language-choices/).

```sigil
let a = environment == "production" let b = "deployer" in actor.roles guardrails(min_soak: 4h) when a and b { review(reason: service_owner, approvers: approvers) }
```

That line parses the same as the formatted version, although `sigil fmt` would never produce it.

Two other boundaries work the same way:

- A `when` condition ends at a `{` in operator position. A `{` in operand position starts a map literal instead, which is how `when service.labels has {"team": "payments"} { ... }` parses: the first `{` follows `has`, the second follows a complete expression.
- In a `type` body, a field's type ends where the next `Name :` begins, because a type never continues with a name.
- An enum's value list, and a decision's `reason:` list, end at the first token after a value that isn't `|`. A list can break across lines, and `|` may start a line.

### Calls

A `Call`'s arguments are named, but the parser still accepts the old positional reason as the first argument, so `sigil fmt` can rewrite it. It tells the two apart at the first argument: a `Name` followed by `:` starts a named argument, and anything else is parsed as an expression, the reason. That's the one place the grammar needs two tokens of lookahead, and it's never ambiguous, because no expression starts with a name followed by `:`.

### Keywords as field names

The grammar allows any keyword wherever a field or payload name appears: after `.`, in `type` bodies, in decision fields and in named arguments. If `Service` declared a `type` field, `service.type` would parse, because the token after `.` is always a name. Top-level names (inputs, params, lets, imported names, host functions, decisions, types, enums and enum values), decision reasons, and quantifier and filter variables must still be plain identifiers.

### Closing angle brackets

`>>` isn't a token, so `map<string, list<string>>` lexes as two `>`. `>=` is a token, which makes `param m: map<string, int>= {}` a problem. The parser splits a `>=` that closes a type argument list into `>` and `=`, and nowhere else. `sigil fmt` writes ` = ` with spaces, which avoids the question.

## Parse errors

A parse error reports the file, line and column, what the parser expected, and a fix when one is obvious:

```text
deploy/production.sigil:9:3: error: expected a decision constructor, invocation, `when`, `let` or `assert`, found `param`
  |
9 |   param tmp: duration
  |   ^^^^^
  = help: `param` is only allowed at the top level; move it outside the `when` block
```

This is the plain-text layout the parser produces. The CLI adds color on a terminal.
