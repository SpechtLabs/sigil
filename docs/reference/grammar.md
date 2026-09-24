---
title: Grammar
icon: mdi:code-braces
createTime: 2026/09/24 22:30:00
permalink: /reference/grammar/
---

::: info Draft specification
This page specifies the language as designed. Nothing is implemented yet; see [Open questions](/project/open-questions/).
:::

This is the complete syntax of policy files, module files and kind files. All three use the `.sigil` extension, and the first token (`policy`, `module` or `kind`) decides which grammar applies. It covers what parses; what type-checks is on the other reference pages. The planned parser is hand-written: recursive descent for statements and a Pratt parser for expressions. The grammar below is written so that both fall out of it directly, with one token of lookahead everywhere except at the start of a call argument, where the parser peeks at a second token (see [Calls](#calls)).

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

Whitespace and comments may appear between any two tokens and are discarded. None of the productions mention them.

## Lexical grammar

```text
Ident       ::= [A-Za-z_] [A-Za-z0-9_]* - Keyword
Keyword     ::= "policy" | "module" | "use" | "as" | "param" | "let" | "when"
              | "kind" | "version" | "type" | "input" | "fn" | "decision"
              | "precedence" | "default"
              | "and" | "or" | "not" | "in" | "all" | "any" | "has"
              | "like" | "matches" | "true" | "false"

Int         ::= [0-9]+
Float       ::= [0-9]+ "." [0-9]+
Duration    ::= ( [0-9]+ Unit )+          /* units unique, largest first */
Unit        ::= "ms" | "s" | "m" | "h" | "d"

String      ::= '"' ( [^"#x5C#xA] | Escape )* '"'
Escape      ::= "\" EscapeBody     /* any escape Go accepts in an
                                     interpreted string literal */
RawString   ::= "`" [^`]* "`"

Comment     ::= "//" [^#xA]*
```

The lexer takes the longest match. A run of digits followed directly by a unit is a `Duration`; `ms` wins over `m` followed by `s`. A number followed directly by any other letter is a lexical error. `??`, `==`, `!=`, `<=`, `>=` and `->` are single tokens.

`Ident` excludes keywords, but field names don't: see `Name` below.

## Policy files

```text
PolicyFile   ::= PolicyHeader UseStmt* PolicyStmt*
PolicyHeader ::= "policy" PolicyName ":" Ident
PolicyName   ::= Ident ( "." Ident )*     /* no whitespace around "." */

PolicyStmt   ::= ParamStmt | LetStmt | WhenStmt | Call

UseStmt      ::= "use" PolicyName ( "as" Ident | "." "{" ImportList "}" )?
ImportList   ::= ImportItem ( "," ImportItem )* ","?
ImportItem   ::= Ident ( "as" Ident )?

ParamStmt    ::= "param" Ident ":" Type ( "=" Expr )?
LetStmt      ::= "let" Ident "=" Expr
WhenStmt     ::= "when" Expr "{" RuleItem* "}"
RuleItem     ::= WhenStmt | Call

Call         ::= Ident "(" CallArgs? ")"
CallArgs     ::= Expr ( "," NamedArg )* ","?       /* decision constructor */
               | NamedArgs                         /* policy invocation */

NamedArgs    ::= NamedArg ( "," NamedArg )* ","?
NamedArg     ::= Name ":" Expr
Name         ::= Ident | Keyword          /* field and payload names */
```

A `Call` is either a decision constructor or a policy invocation, and the parser doesn't need to know which. The checker decides by the name: a decision of the kind makes it a constructor, whose first argument must be a string literal reason; an imported policy makes it an invocation, whose arguments must all be named. Anything else is a compile error.

`UseStmt`s come before every other statement. A `use` after a `param`, `let`, rule or invocation is a parse error with a hint to move it up.

In `use deploy.common.{cleared}`, the `.` before `{` follows the last path segment directly, like the dots inside the path.

## Module files

```text
ModuleFile   ::= ModuleHeader UseStmt* LetStmt*
ModuleHeader ::= "module" PolicyName ":" Ident
```

A module contains nothing but imports and `let`s. A `param`, `when` or call in a module is a parse error with a hint that it belongs in a policy.

## Kind files

```text
KindFile     ::= KindHeader KindStmt*
KindHeader   ::= "kind" Ident "version" Int

KindStmt     ::= TypeDecl | InputDecl | FnDecl | DecisionDecl
               | PrecedenceDecl | DefaultDecl

TypeDecl     ::= "type" Ident "{" FieldDecl* "}"
FieldDecl    ::= Name ":" Type
InputDecl    ::= "input" Ident ":" Type
FnDecl       ::= "fn" Ident "(" ( Param ( "," Param )* ","? )? ")" "->" Type
Param        ::= Ident ":" Type
DecisionDecl ::= "decision" Ident "(" DecisionField ( "," DecisionField )* ","? ")"
DecisionField ::= Name ":" Type ( "=" Expr )?
PrecedenceDecl ::= "precedence" Ident ( ">" Ident )*
DefaultDecl  ::= "default" Constructor
```

That `DecisionDecl` starts with `reason: string`, that `precedence` names every decision once, and that defaults are constants are semantic rules, checked after parsing. See [Kind files](/reference/kind-files/).

## Types

```text
Type         ::= "?" BaseType | BaseType
BaseType     ::= "list" "<" Type ">"
               | "map" "<" Type "," Type ">"
               | Ident                    /* bool, int, ..., or a struct type */
```

`??T` doesn't parse, so optionals don't nest. Optional types only appear in kind files in practice, since params can't be optional.

## Expressions

```text
Expr         ::= OrExpr
OrExpr       ::= AndExpr ( "or" AndExpr )*
AndExpr      ::= NotExpr ( "and" NotExpr )*
NotExpr      ::= "not" NotExpr
               | Quantifier
               | RelExpr
Quantifier   ::= ( "any" | "all" ) Ident "in" Coalesce ":" Expr
RelExpr      ::= Coalesce ( RelOp Coalesce )?          /* non-associative */
RelOp        ::= "==" | "!=" | "<" | "<=" | ">" | ">="
               | "in" | "not" "in" | "all" "in" | "any" "in"
               | "has" | "like" | "matches"
Coalesce     ::= Additive ( "??" Coalesce )?          /* right-associative */
Additive     ::= Unary ( ( "+" | "-" ) Unary )*
Unary        ::= "-" Unary | Postfix
Postfix      ::= Primary ( "." Name | "[" Expr "]" | "(" Args? ")" )*
Args         ::= Expr ( "," Expr )* ","?

Primary      ::= Literal | Ident | "(" Expr ")" | ListLit | MapLit
Literal      ::= "true" | "false" | Int | Float | Duration | String | RawString
ListLit      ::= "[" ( Expr ( "," Expr )* ","? )? "]"
MapLit       ::= "{" ( MapEntry ( "," MapEntry )* ","? )? "}"
MapEntry     ::= Coalesce ":" Expr
```

Syntax alone accepts a few things the checker rejects: a call expression on anything but a host function name, a call statement on anything but a decision or an imported policy, a non-literal pattern after `like` or `matches`, a non-literal reason in a constructor, and `.name` on something that isn't a struct or a whole-module import. Leaving those to the checker gives better error messages than a parse failure would.

## Operator precedence

The expression grammar encodes this table, lowest to highest. It matches [Expressions](/reference/expressions/).

| Level | Operators                   | Associativity  |
| ----- | --------------------------- | -------------- |
| 1     | `or`                        | left           |
| 2     | `and`                       | left           |
| 3     | `not`                       | prefix         |
| 4     | `==` `!=` `<` `<=` `>` `>=` | none           |
| 4     | `in`, `not in`              | none           |
| 4     | `all in`, `any in`          | none           |
| 4     | `has`                       | none           |
| 4     | `like`, `matches`           | none           |
| 5     | `??`                        | right          |
| 6     | `+` `-`                     | left           |
| 7     | `-`                         | prefix         |
| 8     | `.field` `[key]` `f(args)`  | left (postfix) |

A quantifier sits at level 3 as an alternative to `not`. Its range is parsed at level 5, and its body is a full `Expr`, so the body extends as far right as the enclosing construct allows.

::: tip Proposed
Three things in this grammar are proposals rather than settled design: level 4 is non-associative, the quantifier body extends to the right, and keywords are allowed as field names. The next sections explain each.
:::

## How the parser decides

### Quantifier or operator

`all` and `any` have two jobs. `all r in xs: body` is a quantifier; `a all in b` is the subset operator. The parser tells them apart by position, with no extra lookahead:

- Where an operand is expected (the start of an expression, after `and`, `or`, `not`, or `(`), `all` or `any` starts a quantifier and must be followed by an identifier, `in`, a range and `:`.
- Where an operator is expected (right after a complete operand), `all` or `any` must be followed by `in` and forms the binary operator.

`not` works the same way. At operand position it's unary negation; at operator position it must be followed by `in` and forms `not in`.

```sigil
all r in actor.roles: r != "admin"      // quantifier: `all` at operand position
actor.roles all in allowed_roles       // operator: `all` after the operand `actor.roles`
not eligible                           // unary not
"admin" not in actor.roles             // `not in` after an operand
```

Because the quantifier alternative lives at level 3, `x == all r in xs: p` doesn't parse. Put the quantifier in parentheses.

### Where a quantifier body ends

The body is an `Expr`, so it takes every `and` and `or` that follows:

```sigil
any r in actor.roles: r like "sre-*" and eligible
// is
any r in actor.roles: (r like "sre-*" and eligible)
```

It stops at a token that can't continue an expression: `)`, `]`, `}`, `,`, `{` in operator position, or a statement keyword.

### Statement boundaries

Newlines never end anything. Every top-level statement starts with a keyword (`policy`, `use`, `param`, `let`, `when` in policy files; `module`, `use`, `let` in module files; `kind`, `type`, `input`, `fn`, `decision`, `precedence`, `default` in kind files), or, in a policy file, with an identifier followed by `(`, which is a policy invocation. None of those keywords can continue an expression, and an expression never continues with a bare identifier, so when the parser is inside a `let` expression and meets `let`, `when` or `guardrails(`, the expression is over. No other statement starts with an identifier, so the parse stays unambiguous. This is what makes the files safe to indent or join however a text templater likes.

```sigil
let a = environment == "production" let b = "deployer" in actor.roles guardrails(min_soak: 4h) when a and b { review("service_owner", approvers: approvers) }
```

That line parses the same as the formatted version, although `sigil fmt` would never produce it.

Two other boundaries work the same way:

- A `when` condition ends at a `{` in operator position. A `{` in operand position starts a map literal instead, which is how `when service.labels has {"team": "payments"} { ... }` parses: the first `{` follows `has`, the second follows a complete expression.
- In a `type` body, a field's type ends where the next `Name :` begins, because a type never continues with a name.

### Calls

A `Call`'s arguments are either one positional reason followed by named payload fields, or named arguments only. The parser tells the two apart at the first argument: a `Name` followed by `:` starts a named argument, anything else starts the positional reason. That's the one place the grammar needs two tokens of lookahead. An expression can't start with an identifier followed by `:`, so the choice is never ambiguous.

### Keywords as field names

A Go host can tag a field with any name, and Kubernetes-shaped data often has a field called `type`, which is a keyword. The grammar allows any keyword wherever a field or payload name appears: after `.`, in `type` bodies, in decision fields and in named arguments. If `Service` declared a `type` field, `service.type` would parse, because the token after `.` is always a name. Top-level names (inputs, params, lets, imported names, host functions, decisions, types) must still be plain identifiers.

### Closing angle brackets

`>>` isn't a token, so `map<string, list<string>>` lexes as two `>`. `>=` is a token, which makes `param m: map<string, int>= {}` a problem. The parser splits a `>=` that closes a type argument list into `>` and `=`. (proposed; `sigil fmt` writes ` = ` with spaces, which avoids the question.)

## Parse errors

A parse error reports the file, line and column, what the parser expected, and a fix when one is obvious:

```text
deploy/production.sigil:9:3: error: expected a decision constructor, invocation or `when`, found `let`
  |
9 |   let tmp = release.soak
  |   ^^^
  = help: `let` is only allowed at the top level; move it outside the `when` block
```

The layout is illustrative; the exact format isn't fixed yet.
