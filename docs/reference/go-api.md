---
title: Go API
icon: mdi:language-go
createTime: 2026/09/24 22:30:00
permalink: /reference/go-api/
---

Every exported symbol of the Go packages `policy`, `policytest` and `cli`: its signature, its rules, what it returns and how it fails.

| Package      | Import path                                  | Holds                                                                               |
| ------------ | -------------------------------------------- | ----------------------------------------------------------------------------------- |
| `policy`     | `github.com/spechtlabs/sigil/pkg/policy`     | [Kinds](#kinds), [loading](#loading), [evaluating](#evaluating), [results](#result) |
| `policytest` | `github.com/spechtlabs/sigil/pkg/policytest` | Running test files from `go test`; see [Package policytest](#package-policytest)    |
| `cli`        | `github.com/spechtlabs/sigil/pkg/cli`        | The `sigil` command line in a host binary; see [Package cli](#package-cli)          |

The API is pre-1.0, so names and signatures can still change. To embed Sigil step by step, see [Embed Sigil in a Go service](/guides/embed-go/). [The example service](/guides/example-service/) uses all three packages.

## Kinds

### `NewKind`

```go
func NewKind[In any](name string, opts ...Option) *Kind[In]
```

| Parameter | Is                                                                                                                                                 |
| --------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `In`      | The input struct. Its tagged fields become the kind's `input`s, and the structs it reaches become `type`s; see [Go type mapping](#go-type-mapping) |
| `name`    | The kind's name, which policies pin in their headers: `policy payments.production: DeployApproval@1`                                               |
| `opts`    | The [kind options](#kind-options)                                                                                                                  |

- Returns the kind. Build it once, at package level.
- The options must include `WithVersion`, and either `WithDecisions` with `WithDefault`, or `WithCollect`.
- Panics when the contract can't be exported: a Go type outside the [mapping](#go-type-mapping), a tag option outside a payload, or a kind that breaks a [validity rule](/reference/kind-files/#validity-rules), such as a missing version or default. The panic lists every problem at once.
- A kind that exists can always be exported, and the exported kind file parses back into the same contract.

```go
var Deploy = policy.NewKind[Input]("DeployApproval",
	policy.WithVersion(1),
	policy.WithDecisions(Deny, Review, Approve), // order = precedence
	policy.WithReasonPrecedence(NotEligible, SoakTooShort, NoRuleMatched),
	policy.WithReasonPrecedence(ReleaseManager, PaymentsSRE),
	policy.WithDefault(NoRuleMatched),
	policy.WithFunc("split", strings.Split),
)
```

```text
policy.NewKind(K): invalid kind:
  p: *[]string is a pointer to a slice (use the slice itself; a nil slice already reads as an empty list)
  invalid kind version 0 (the version is a positive integer that changes when the contract does)
```

### `Kind` methods

```go
type Kind[In any] struct{ /* unexported fields */ }

func (k *Kind[In]) Name() string
func (k *Kind[In]) Schema() string
func (k *Kind[In]) Load(fsys fs.FS, name string, opts ...LoadOption) (*Policy[In], error)
func (k *Kind[In]) Compile(src, name string, opts ...LoadOption) (*Policy[In], error)
func (k *Kind[In]) Contract() *contract.Kind
```

| Method            | Returns                                                                                                                                                                                                                                     |
| ----------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Name()`          | The kind's name, as passed to `NewKind`                                                                                                                                                                                                     |
| `Schema()`        | The kind as a kind file, in the [canonical form](/reference/kind-files/#canonical-form) and in `sigil fmt`'s [canonical style](/reference/cli/#sigil-fmt), so it passes `sigil fmt --check` as it is. It parses back into the same contract |
| `Load`, `Compile` | A compiled policy; see [Loading](#loading)                                                                                                                                                                                                  |
| `Contract()`      | The kind model and its binding to the Go types and functions, for this module's tooling, `cli` and `policytest`. Its type is internal to the module, so nothing outside it can use it                                                       |

A `*Kind` is immutable and safe for concurrent use. To write `Schema()` to a file and keep it current, see [Build a host binary](/guides/host-binary/#export-the-kind).

::: warning Planned
`policy.LoadKind`, which would load a kind from its kind file at run time, and an API to bind host functions to such a kind don't exist yet; see [Loading a kind at run time](/project/planned/#loading-a-kind-at-run-time).
:::

### Kind options

```go
type Option func(*gokind.Options)

func WithVersion(n int) Option
func WithAccepts(n int) Option
func WithDecisions(ds ...DecisionRef) Option
func WithCollect(ds ...DecisionRef) Option
func WithPrecedence(ds ...DecisionRef) Option
func WithReasonPrecedence(reasons ...Outcome) Option
func WithExclusive(outcomes ...OutcomeRef) Option
func WithDefault(reason Outcome) Option
func WithFunc(name string, fn any) Option
func WithRecoverHostPanics() Option
```

Each option corresponds to a declaration of a [kind file](/reference/kind-files/), which `Schema()` writes out.

| Option                             | Kind file equivalent                                 | Rules                                                                                                                                                                                                                                    |
| ---------------------------------- | ---------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `WithVersion(n)`                   | `kind DeployApproval version n`                      | Contract version, bumped by every change to the kind. Required, and at least 1                                                                                                                                                           |
| `WithAccepts(n)`                   | `kind DeployApproval version 3, accepts: n`          | Oldest version a policy or module may pin. From 1 to the version. Without it, every version is accepted. Raise it with a breaking change; see [Versioning](/reference/kind-files/#versioning)                                            |
| `WithDecisions(d...)`              | `decision ...`, `collect one` and `precedence ...`   | Argument order is precedence, highest first. Makes `WithDefault` required                                                                                                                                                                |
| `WithCollect(d...)`                | `decision ...` and `collect all`                     | Instead of `WithDecisions`: every fired decision applies. Argument order is declaration order                                                                                                                                            |
| `WithPrecedence(d...)`             | `precedence ...` in a `collect all` kind             | Ranks a `WithCollect` kind's decisions, so the outcome is every candidate at the top rank. Must list every decision. On a `WithDecisions` kind it makes `NewKind` panic                                                                  |
| `WithReasonPrecedence(reasons...)` | `precedence approve: release_manager > payments_sre` | Ranks one decision's reasons, highest first, given as [reason handles](#decisions-and-reasons). Must list every reason of that decision and no other decision's, once per decision                                                       |
| `WithExclusive(outcomes...)`       | `exclusive grant_a, grant_b`                         | Outcomes that can't fire together, each a decision handle or one reason, `GrantA.Reason("x")`. One set per call, of at least two outcomes. Two of them firing in one evaluation is a `*ConflictError`, under both collect modes          |
| `WithDefault(reason)`              | `default deny(no_rule_matched)`                      | Result when no rule fires, given as a reason handle. Its payload fields take their defaults, so every payload field of its decision needs a `default=`. Required with `WithDecisions`, optional with `WithCollect`                       |
| `WithFunc(name, fn)`               | `fn split(string, string) -> list<string>`           | One call per function. The Sigil signature is derived from `fn`'s type, which returns `T` or `(T, error)`; the name is always written out. Host functions must be pure, must terminate and must not panic; see [Evaluating](#evaluating) |
| `WithRecoverHostPanics()`          | none; it's host behavior, not contract               | A panic in a host function becomes a `*RuntimeError` instead of unwinding out of `Eval`. Off by default                                                                                                                                  |

- Options that take several values add up: `WithDecisions(Deny, Review)` and `WithDecisions(Deny), WithDecisions(Review)` declare the same kind, in the same order.
- A kind takes `WithDecisions` or `WithCollect`, never both and never neither.
- An error `fn` returns becomes a `*RuntimeError` whose `Err` is that error.

A collecting kind without a default:

```go
var Access = policy.NewKind[AccessInput]("AccessGrant",
	policy.WithVersion(1),
	policy.WithCollect(Read, Write, Admin, CustomerDataWriter, DevEnvWriter),
)
```

Why `WithFunc` takes the name: [Why the language looks like this](/understanding/language-choices/).

::: warning Planned
Host-ordered types, `type Version ordered` in a kind file and `policy.WithOrdered[T](name)` in Go, don't exist yet; see [Host-ordered types](/project/planned/#host-ordered-types).
:::

### Decisions and reasons

```go
func NewDecision[T any](name string, reasons ...string) Decision[T]

type Decision[T any] struct{ /* unexported fields */ }

func (d Decision[T]) Name() string
func (d Decision[T]) Reasons() []string
func (d Decision[T]) Reason(name string) Outcome
func (d Decision[T]) Match(res *Result) (T, bool)
func (d Decision[T]) MatchAll(res *Result) []Matched[T]

type Outcome struct{ /* unexported fields */ }

func (o Outcome) Decision() string
func (o Outcome) Name() string
func (o Outcome) Is(res *Result) bool

type None struct{}

type DecisionRef interface {
	OutcomeRef
	Name() string
	// unexported methods
}

type OutcomeRef interface{ /* unexported methods */ }
```

| Symbol                             | Does                                                                                                                                                                                      |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `NewDecision[T](name, reasons...)` | Declares the decision `name` with payload struct `T` and the reasons policies may construct it with. Copies `reasons`                                                                     |
| `Decision[T].Name()`               | The decision's name as policies write it, `"approve"`                                                                                                                                     |
| `Decision[T].Reasons()`            | The declared reasons, in declaration order, as a copy                                                                                                                                     |
| `Decision[T].Reason(name)`         | The reason handle for one declared reason. Panics when the decision doesn't declare `name`, with the hint the checker gives for the same typo in a policy                                 |
| `Decision[T].Match`, `.MatchAll`   | Typed matching; see [Typed matching](#typed-matching)                                                                                                                                     |
| `Outcome.Decision()`               | The name of the handle's decision, `"deny"`                                                                                                                                               |
| `Outcome.Name()`                   | The reason's name, `"no_rule_matched"`                                                                                                                                                    |
| `Outcome.Is(res)`                  | Whether `res` is exactly that reason; see [Typed matching](#typed-matching)                                                                                                               |
| `None`                             | The payload of a decision that carries only a reason                                                                                                                                      |
| `DecisionRef`                      | Any `Decision[T]`, whatever its payload type, so decisions of different payload types pass together to `WithDecisions`, `WithCollect` and `WithPrecedence`. Only `Decision` implements it |
| `OutcomeRef`                       | A decision, standing for any of its reasons, or an `Outcome` for one, as `WithExclusive` takes them. Only those two implement it                                                          |

- `T`'s tagged fields are the decision's payload fields, mapped as in [Go type mapping](#go-type-mapping). The reason is implicit on every decision and never appears in `T`.
- Reasons are plain strings, as `Result.Reason` is.
- An `Outcome` is how Go code names a reason: to rank it with `WithReasonPrecedence`, make it the default with `WithDefault`, declare it exclusive with `WithExclusive`, and compare a result against it with `Is`.
- The zero `Outcome` names no reason, and `NewKind` rejects it.

```go
var (
	Deny    = policy.NewDecision[policy.None]("deny", "not_eligible", "soak_too_short", "no_rule_matched")
	Review  = policy.NewDecision[ReviewData]("review", "service_owner")
	Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")
)

var (
	NotEligible    = Deny.Reason("not_eligible")
	SoakTooShort   = Deny.Reason("soak_too_short")
	NoRuleMatched  = Deny.Reason("no_rule_matched")
	ReleaseManager = Approve.Reason("release_manager")
	PaymentsSRE    = Approve.Reason("payments_sre")
)
```

```text
policy: decision deny has no reason "no_rule_mached" (did you mean "no_rule_matched"? deny declares: not_eligible, soak_too_short, no_rule_matched)
```

Why reasons are declared names: [Decisions and reasons](/understanding/decisions/).

### Go type mapping

`NewKind[In]` walks the input struct `In` by reflection. Every field with a `policy:"name"` tag becomes an input, every struct type it reaches becomes a `type` named after the Go type, and every tagged field of those structs becomes a field. Untagged fields and fields tagged `policy:"-"` are invisible to policies.

| Go                               | Sigil            |
| -------------------------------- | ---------------- |
| `string`, `bool`                 | `string`, `bool` |
| `int`, `int64`                   | `int`            |
| `float64`                        | `float`          |
| `time.Duration`                  | `duration`       |
| `time.Time`                      | `timestamp`      |
| `[]T`                            | `list<T>`        |
| `map[K]T`, scalar `K`            | `map<K, T>`      |
| `*T`                             | `?T`             |
| `*[]T`, `*map[K]T`, `**T`        | rejected         |
| named struct with `policy:` tags | `type`           |

- A named type follows its underlying type: `type Tier string` maps to `string`.
- `*Struct` maps to `?Struct`, whose fields a policy reads with [optional chaining](/reference/expressions/#optional-chaining): `release?.soak ?? 0s`.
- A pointer to a slice or a map is rejected; a nil slice or map already reads as an empty list or map.
- `NewKind` rejects anything else, and lists every problem it finds: other integer and float sizes, unsigned integers, channels, funcs, interfaces, anonymous structs, two Go types with the same name, map keys that aren't scalars (`bool`, `int`, `float`, `string`, `duration` or `timestamp`), and tagged fields that are unexported or embedded.

Payload structs map to decision fields the same way. A payload field's default goes in its tag, after the name:

```go
type ReviewData struct {
	Approvers []string `policy:"approvers"`
}

type ApproveData struct {
	Bake time.Duration `policy:"bake,default=1h"`
}
```

- `default=` takes a Sigil constant of the field's type.
- `default=` is the only tag option, and only payload fields take it. An option on an input's tag or on a field of a `type` struct makes `NewKind` panic, as a default on a `type` field is an error in a kind file.
- A decision without a payload uses `policy.None`.

Host function signatures come from the Go function's type: each parameter type maps like a field, and the result is `T` or `(T, error)`.

The same mapping gives the Go values [`Params`](#params) takes and the Go types a [host binary](/reference/cli/#host-functions-and-host-binaries) decodes inputs into.

## Loading

```go
func (k *Kind[In]) Load(fsys fs.FS, name string, opts ...LoadOption) (*Policy[In], error)
func (k *Kind[In]) Compile(src, name string, opts ...LoadOption) (*Policy[In], error)

type Policy[In any] struct{ /* unexported fields */ }

func (p *Policy[In]) Name() string
func (p *Policy[In]) Eval(ctx context.Context, input In) (*Result, error)
```

| Parameter | Is                                                                        |
| --------- | ------------------------------------------------------------------------- |
| `fsys`    | The files to read, such as an `embed.FS`, `os.DirFS` or [`MapFS`](#mapfs) |
| `src`     | One source string, read as a one-file bundle                              |
| `name`    | The root policy's name                                                    |
| `opts`    | [Load options](#load-options)                                             |

`Load`:

- Reads every `.sigil` file in `fsys` into one bundle; which files it reads is in [Loading files](/reference/bundles/#loading-files).
- Indexes the documents by the names in their headers and compiles the policy `name` as the root. Imports resolve by name within the bundle; see [Name resolution](/reference/bundles/#name-resolution).
- Checks every document, so a broken document fails the load even when the root never uses it.

`Compile`:

- Does the same for one source string. The source may hold several documents, each ending where the next header starts, with or without `---` between them (see [Documents](/reference/bundles/#documents)), and imports resolve among them.
- The source has no file name, so its positions carry only the line, the column and the document's name: `4:3 (payments.production)`.

`*Policy[In]`:

- Both `Load` and `Compile` return one.
- `Name()` returns the root's name as its header declares it, `"payments.production"`.
- It's immutable and safe for concurrent use.
- `Eval` is under [Evaluating](#evaluating).

```go
//go:embed policies
var policies embed.FS

p, err := Deploy.Load(policies, "payments.production", policy.Require("deploy.guardrails"))
```

| Fails when                                                   | Error                                                                                                                                                           |
| ------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A document doesn't parse, type-check or compile              | `*CompileError`                                                                                                                                                 |
| A name is defined twice                                      | `*CompileError`                                                                                                                                                 |
| A document is for another kind                               | `*CompileError`: `document is for kind AccessGrant, not DeployApproval`                                                                                         |
| A kind document with the kind's name differs from `Schema()` | `*CompileError`: `kind document DeployApproval doesn't match the host's kind`; see [Kind documents in a bundle](/reference/bundles/#kind-documents-in-a-bundle) |
| The root is a module                                         | `*CompileError`: `deploy.common is a module, not a policy`                                                                                                      |
| A `Params` value or a `Require` doesn't hold                 | `*CompileError`; see [Load options](#load-options)                                                                                                              |
| `fsys` can't be read (`Load` only)                           | The error from reading `fsys`                                                                                                                                   |

### Compile errors

```go
type CompileError struct {
	Diagnostics []Diagnostic
	// unexported fields
}

func (e *CompileError) Error() string

type Diagnostic struct {
	Message  string   // what's wrong, on one line
	Help     string   // how to fix it; empty when there's no obvious fix
	Position Position // start of the offending source; unknown for a rule of the kind with no source
	End      Position // just after the offending source
}
```

- `Diagnostics` holds every problem found, in source order, each with its position, the document it's in and a fix hint.
- `Error()` quotes the offending lines the way the CLI does; see [Error messages](/reference/cli/#error-messages).

A policy compiled from a string:

```text
3:14 (deploy.gate): error: unknown field "teir" on type Service
  |
3 | when service.teir == "critical" {
  |              ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

### `MapFS`

```go
func MapFS(files map[string]string) fs.FS
```

- Returns an in-memory `fs.FS` with each key as a file name and each value as its contents.
- Keys need the `.sigil` extension to be loaded, like any other file.
- Copies the contents, so later changes to `files` don't affect the result.

To load a ConfigMap read through the Kubernetes API, see [Policies in a ConfigMap](/guides/configmaps/).

### Load options

```go
type LoadOption interface{ /* unexported methods */ }

type Params map[string]any

func Require(name string, opts ...RequireOption) LoadOption

type RequireOption interface{ /* unexported methods */ }

func From(fsys fs.FS) RequireOption
```

| Option               | Does                                                                                       |
| -------------------- | ------------------------------------------------------------------------------------------ |
| `Params{...}`        | Binds the root policy's params from Go                                                     |
| `Require(name, ...)` | Requires the root policy to invoke the named policy unconditionally                        |
| `From(fsys)`         | Inside `Require`: takes the required policy, and everything it uses, from a trusted source |

`LoadOption` is implemented by `Params` and `Require`; `RequireOption` only by `From`.

#### `Params`

- Maps a param name to a Go value of the shape `NewKind` accepts for the param's type: a `string` for `string`, a `[]string` for `list<string>`, a `time.Duration` for `duration`, and so on.
- Values are type-checked against the root's `param` declarations and their [`min` and `max`](/reference/policy-files/#bounds) at compile time, as invocation arguments are.
- Several `Params` options merge; a later value wins for the same name.
- Compile errors: a param the root doesn't declare, a value of the wrong type or outside the bounds, and a param without a default that `Params` leaves unbound.

```go
p, err := Deploy.Load(policies, "deploy.gate",
	policy.Params{
		"approvers": []string{"payments-leads"},
		"min_soak":  4 * time.Hour,
	},
	policy.Require("deploy.guardrails"),
)
```

#### `Require`

- Fails the load unless the root reaches the named policy through top-level invocations only, with no `when` on the path. The rules are in [Required policies](/reference/evaluation/#required-policies).
- The `*CompileError` points at the gated call, or at the root's header when the call is missing.
- Repeat it to require several policies.
- Without `From`, the name is looked up in the bundle like any other document.
- Takes no bounds of its own; a required policy bounds its own params with [`min` and `max`](/reference/policy-files/#bounds).
- `sigil check --require` runs the same check; see [`sigil check`](/reference/cli/#sigil-check).

#### `From`

- Names the trusted source a required policy must come from. The loader reads it as its own bundle, separate from the one passed to `Load`.
- Several `Require` options may name the same source, which is then read once.
- The rules for what resolves where, which names are reserved and when two sources are the same are in [Trusted sources](/reference/bundles/#trusted-sources).

```go
p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails", policy.From(platformFS)))
```

Why `From` exists: [Why required policies need a trusted source](/understanding/bundles/#why-required-policies-need-a-trusted-source).

## Evaluating

```go
func (p *Policy[In]) Eval(ctx context.Context, input In) (*Result, error)
```

| Parameter | Is                                   |
| --------- | ------------------------------------ |
| `ctx`     | Cancels the evaluation               |
| `input`   | One value of the kind's input struct |

- Returns the [result](#result) with its trace, and an [error](#errors) when the evaluation failed.
- Never returns a nil result. With an error, the result holds the kind's default decision, or an empty outcome for a collecting kind; see [Failed evaluations](/reference/evaluation/#failed-evaluations).
- Safe to call from any goroutine. A compiled policy is immutable and safe for concurrent use: concurrent evaluations share no mutable state and take no locks.
- Replacing a compiled policy at run time is a pointer swap, for example through a `sync/atomic.Pointer`. An evaluation in flight keeps the policy it started with. To reload policies, see [Reload without an outage](/guides/configmaps/#reload-without-an-outage).
- Checks `ctx` while it runs, and returns its error once it's done; when it checks and what the result then holds are in [Context checks](/reference/evaluation/#context-checks).
- Host functions run on the calling goroutine, must be pure, must terminate and must not panic. `Eval` stops as soon as a host function returns after the context is done.
- A panic in a host function propagates out of `Eval` unless the kind sets `WithRecoverHostPanics`.

```go
res, err := p.Eval(ctx, input)
if err != nil {
	// res still holds the kind's default decision
}
```

To act on each error, see [Handle failed evaluations](/guides/handle-errors/).

::: warning Planned
A `Resolver` that supplies input values by path instead of a Go struct is a proposal, not an exported API; see [Dynamic input](/project/planned/#dynamic-input).
:::

### Errors

| Error               | When                                                                                                                                                     | Fields                                                                     |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| `*RuntimeError`     | An index out of range, integer overflow, or a host function that returned an error, or panicked under `WithRecoverHostPanics`                            | `Err`, `Message`, `Help`, `Policy`, `Position`                             |
| `*ConflictError`    | A [conflict](/reference/evaluation/#resolution): two members of an `exclusive` set fired, or a `collect one` kind has several candidates at its top rank | `Message`, `Policy`, `Candidates`                                          |
| `*AssertionError`   | An [assert](/reference/evaluation/#assertions) failed                                                                                                    | `Failures`, `Phase`                                                        |
| The context's error | `ctx` was done before or during the evaluation                                                                                                           | Unwrapped: `errors.Is(err, context.DeadlineExceeded)` holds for a deadline |

Tell them apart with `errors.As`.

#### `RuntimeError`

```go
type RuntimeError struct {
	Err      error    // the host function's error or *HostPanicError; nil for an error in the policy itself
	Message  string   // what failed, such as "index 3 out of range for a list of 2"
	Help     string   // what to do about it, when the evaluator knows; empty otherwise
	Policy   string   // the policy being evaluated, the root
	Position Position // the expression that failed
}

func (e *RuntimeError) Error() string
func (e *RuntimeError) Unwrap() error
```

- `Error()` returns the position followed by the message.
- `Unwrap()` returns `Err`, so `errors.Is` and `errors.As` find the host function's own error through the policy.

#### `HostPanicError`

```go
type HostPanicError struct {
	Value any    // what the function passed to panic
	Func  string // the host function's name in the kind
	Stack []byte // the panicking goroutine's stack, as runtime/debug.Stack formats it
}

func (e *HostPanicError) Error() string
func (e *HostPanicError) Unwrap() error
```

- It's the `Err` of a `*RuntimeError` when a host function panicked and the kind sets `WithRecoverHostPanics`.
- The `*RuntimeError`'s message names the function and the panic value. The stack stays out of it and is only in `Stack`.
- `Error()` names the function and the panic value, without the stack.
- `Unwrap()` returns the panic value when it's an error, such as the `runtime.Error` of a nil dereference.

#### `ConflictError`

```go
type ConflictError struct {
	Message    string      // what conflicts
	Policy     string      // the policy being evaluated
	Candidates []Candidate // only those that conflict
}

func (e *ConflictError) Error() string
```

- `Candidates` holds only the candidates that conflict: the top-rank tie, or the members of the exclusive set that fired. The result's trace has every candidate.
- `Error()` returns the message followed by one line per candidate, as [`Candidate.String()`](#result) renders it, so it reads like the trace: which rules, at which positions, claimed what.

#### `AssertionError`

```go
type AssertionError struct {
	Failures []AssertFailure
	Phase    AssertPhase // InputAsserts or OutcomeAsserts
}

func (e *AssertionError) Error() string

type AssertFailure struct {
	Cause     *RuntimeError // the runtime error that kept the assert from being checked; nil when its condition was false
	Reason    string        // the assert's reason, its first argument
	Policy    string        // the policy the assert is in
	CallChain []Position    // the invocations it was reached through; empty for an assert in the evaluated policy itself
	Outcome   []Candidate   // for an outcome assert, the candidates that formed the outcome it read
	Position  Position      // of the assert in its policy
}

func (f AssertFailure) Location() string

type AssertPhase int

const (
	InputAsserts AssertPhase = iota + 1
	OutcomeAsserts
)

func (p AssertPhase) String() string
```

- `Failures` lists every assert that failed in the phase that stopped evaluation, sorted by position, including asserts whose own condition raised a runtime error.
- `Phase` says which phase that was. `InputAsserts` rejects the caller's input; `OutcomeAsserts` means the policy produced an outcome it forbids, a defect in the policy.
- Read the phase from `Phase`, not from the trace: the trace is empty after a failed input assert, and also after a failed outcome assert when no rule fired.
- What the result that comes with the error holds is in [Failed evaluations](/reference/evaluation/#failed-evaluations).
- `Error()` returns each failing assert's reason and location, with its runtime error when it has one.
- `AssertFailure.Location()` renders the call chain and position, as [`Candidate.Location()`](#result) does.
- `AssertPhase.String()` returns `input` or `outcome`, for a metric label, or `none` for the zero value, which no `AssertionError` from `Eval` has.

## Result

```go
type Result struct {
	Decision string         // "review"
	Reason   string         // "service_owner"
	Policy   string         // "payments.production": the policy the host evaluated
	Payload  map[string]any // untyped view; use Decision[T].Match for typed
	Outcome  []Entry        // the winner, or for a collecting kind every candidate, sorted; may be empty
	Trace    Trace          // all candidates, with conditions for the winning decision
	// unexported fields
}

type Entry struct {
	Decision string
	Reason   string
	Policy   string         // the policy whose rule produced it; empty for the kind's default
	Payload  map[string]any // defaults filled in
	Position Position       // of the constructor; unknown for the default
	// unexported fields
}

type Trace struct {
	Candidates []Candidate // sorted by precedence (or declaration order) and then position
}

type Candidate struct {
	Decision   string
	Reason     string
	Policy     string
	Payload    map[string]any
	Position   Position    // of the constructor in its policy
	CallChain  []Position  // the invocations it was reached through, outermost first
	Conditions []Condition // the `when` conditions that held; only for candidates of the winning decision
}

func (c Candidate) Location() string
func (c Candidate) String() string

type Condition struct {
	Text     string // the condition as written, on one line
	Position Position
}
```

| Field                           | `collect one`                                        | `collect all`                                                                                                                             |
| ------------------------------- | ---------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `Decision`, `Reason`, `Payload` | The winner, or the kind's default when nothing fired | Empty                                                                                                                                     |
| `Outcome`                       | That one entry                                       | Every folded candidate; with precedence, every folded candidate at the top rank. Sorted by declaration order, then position. May be empty |
| `Policy`                        | The policy the host evaluated                        | The same                                                                                                                                  |
| `Trace`                         | Every candidate                                      | Every candidate                                                                                                                           |

- Equal candidates, with the same decision, reason and payload, fold into one outcome entry.
- `Entry.Policy` and `Candidate.Policy` name the policy whose rule produced it. When the host evaluates `payments.production` and the `service_owner` review from `deploy.production` wins, `Result.Policy` is `payments.production`, and the entry and the candidate name `deploy.production`.
- `CallChain` lists the invocations a candidate was reached through, each with file, line, column and document name. It's empty for a rule in the evaluated policy itself.
- `Conditions` records which `when` conditions held, outermost first, including the conditions around invocations, for every candidate of the winning decision.
- `Candidate.Location()` renders the whole chain and position: `payments/production.sigil:10:3 → deploy/production.sigil:16:5`.
- `Candidate.String()` renders the candidate on one line, the way a trace prints it: the decision, the reason, the location, and the payload with its fields sorted by name.
- `Payload` maps field names to untyped values, for logging and generic tooling. [Typed matching](#typed-matching) returns the payload struct.

How the winner is picked: [Evaluation semantics](/reference/evaluation/). Whether a collecting kind should get its own result type is [open](/project/open-questions/#collecting-kinds).

## Typed matching

```go
func (d Decision[T]) Match(res *Result) (T, bool)
func (d Decision[T]) MatchAll(res *Result) []Matched[T]
func (o Outcome) Is(res *Result) bool

type Matched[T any] struct {
	Payload  T        // the payload struct, defaults filled in
	Reason   string   // the reason the policy gave
	Policy   string   // the policy whose rule produced it; empty for the kind's default
	Position Position // of the constructor; unknown for the kind's default
}
```

| Kind                             | `Match`                                                                                                            | `Is`                                                      | `MatchAll`                                                |
| -------------------------------- | ------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------- | --------------------------------------------------------- |
| `collect one`                    | `true` and the payload when the outcome is that decision                                                           | `true` when the outcome is that decision with that reason | The winner when it's that decision, and nothing otherwise |
| `collect all` with precedence    | `true` when the outcome is exactly one entry of that decision; `false` when the top rank holds more than one entry | As `Match`, comparing the reason too                      | Every entry of that decision                              |
| `collect all` without precedence | Panics                                                                                                             | Panics                                                    | Every entry of that decision                              |

- `Match` returns the zero `T` and `false` for a nil result or another decision; `Is` returns `false`; `MatchAll` returns nil for a nil result.
- `MatchAll` returns entries in outcome order, each a `Matched[T]` whose fields other than `Payload` are those of the `Entry` it came from.
- The result that comes with an error holds the kind's default, so matching the default decision or reason on it succeeds. Check `err` before matching.
- Comparing `res.Reason` with a string compiles with a typo in it and never matches; `Is` compares through a handle that was checked when it was declared.

```go
if r, ok := Review.Match(res); ok {
	requestReview(r.Approvers, res.Reason) // r is a typed ReviewData
}

if NoRuleMatched.Is(res) {
	flagUncovered(p.Name()) // the default: no rule covers this deploy
}

for _, g := range Admin.MatchAll(res) {
	grantAdmin(g.Payload.TTL, g.Reason) // g.Payload is a typed AdminData
}
```

## Positions

```go
type Position struct {
	File     string // the path in the fs.FS given to Load; empty for a source given to Compile
	Document string // the name in the header of the document at this place, such as "payments.production"
	Line     int
	Column   int
}

func (p Position) IsValid() bool
func (p Position) String() string
```

- Every compile error and every trace entry carries a `Position`.
- `Line` and `Column` are 1-based, and `Column` counts characters, not bytes.
- A position with line 0 is unknown, as for the kind's default decision, which has no source. `IsValid()` reports false for it.

| Position                           | `String()`                                  |
| ---------------------------------- | ------------------------------------------- |
| File path doesn't say the document | `policies.sigil:42:5 (payments.production)` |
| File path says the document        | `deploy/production.sigil:16:5`              |
| No file, from `Compile`            | `4:3 (payments.production)`                 |
| Unknown                            | `-`                                         |

The Go values always carry both the file and the document name, so a trace stays readable when many documents share one file, such as one ConfigMap key. The CLI prints positions the same way.

## Package policytest

Import path `github.com/spechtlabs/sigil/pkg/policytest`. It runs the [test files](/reference/test-files/) `sigil test` runs, from `go test`, with the host's Go types and real function implementations.

```go
func Run[In any](t *testing.T, k *policy.Kind[In], fsys fs.FS, opts ...policy.LoadOption)
func Schema[In any](t testing.TB, k *policy.Kind[In], file string)
```

| Function                   | Does                                                                                                                                                                                            |
| -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Run(t, k, fsys, opts...)` | Runs every `*_test.yaml` file in every directory of `fsys` against the policies in `fsys`. Each file's `policy:` is loaded with `k.Load` and `opts`, the same options the host passes to `Load` |
| `Schema(t, k, file)`       | Fails the test when the kind file at `file`, on disk, isn't `k.Schema()`. The failure shows both versions                                                                                       |

- `Run` skips entries whose names start with `.`, as `Load` does.
- Each test file becomes a subtest named after its path, and each case a subtest of it, so `go test -run 'TestPolicies/access/main_test.yaml/admins'` selects them.
- A test file that can't be read, doesn't parse, or whose policy doesn't compile fails its subtest. A case that doesn't get what it expects fails its own.
- `Run` fails `t` at once when `k` is nil or `fsys` holds no test files.
- A test file can't expect a conflict; see [Test a conflict](/guides/test-policies/#test-a-conflict).

```go
func TestPolicies(t *testing.T) {
	policytest.Run(t, Deploy, os.DirFS("../policies"), policy.Require("deploy.guardrails"))
}

func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, Deploy, "../policies/deploy_approval.sigil")
}
```

To set it up in a host, see [Run them from go test](/guides/test-policies/#run-them-from-go-test).

## Package cli

Import path `github.com/spechtlabs/sigil/pkg/cli`. It builds the whole `sigil` command line into a host's own binary.

```go
func Main(opts ...Option)

type Option = command.Option

func WithKind[In any](k *policy.Kind[In]) Option
func WithVersion(version string) Option
```

| Symbol           | Does                                                                                                                                                                                                  |
| ---------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Main(opts...)`  | Runs the `sigil` command line on `os.Args` and exits: status 1 when the command failed, after printing why, and 0 otherwise. An interrupt or `SIGTERM` cancels the running command. It doesn't return |
| `WithKind(k)`    | Links a kind into the binary. Repeat it for a host with several kinds. A nil `k` is ignored                                                                                                           |
| `WithVersion(v)` | Sets the version `sigil version` reports. When it's empty, the main module's version from the Go build info is reported                                                                               |

What a linked kind changes in each command is in [Host functions and host binaries](/reference/cli/#host-functions-and-host-binaries). To build one, see [Build a host binary](/guides/host-binary/#build-the-binary).

```go
func main() {
	cli.Main(cli.WithKind(deploygate.Deploy))
}
```

## Package index

Every exported identifier of package `policy`:

| Identifier                                                                                                                                                                                             | What it is                                                                                                           |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------- |
| [`NewKind[In](name, opts...) *Kind[In]`](#newkind)                                                                                                                                                     | Builds a kind from the input struct `In`; panics on an invalid kind                                                  |
| [`Kind[In].Name`, `.Schema`, `.Load`, `.Compile`, `.Contract`](#kind-methods)                                                                                                                          | The kind's name, its kind file, loading and compiling policies, and the tooling hook                                 |
| [`Option`, `WithVersion`, `WithAccepts`, `WithDecisions`, `WithCollect`, `WithPrecedence`, `WithReasonPrecedence`, `WithExclusive`, `WithDefault`, `WithFunc`, `WithRecoverHostPanics`](#kind-options) | Options for `NewKind`                                                                                                |
| [`NewDecision[T](name, reasons...) Decision[T]`](#decisions-and-reasons)                                                                                                                               | Declares a decision with payload struct `T`                                                                          |
| [`Decision[T].Name`, `.Reasons`, `.Reason`, `.Match`, `.MatchAll`](#decisions-and-reasons)                                                                                                             | The decision's name and reasons, one reason as an `Outcome` handle (panics on an undeclared one), and typed matching |
| [`Outcome.Decision`, `.Name`, `.Is`](#decisions-and-reasons)                                                                                                                                           | A reason handle's decision and reason names, and whether a result is exactly that reason                             |
| [`DecisionRef`, `OutcomeRef`, `Outcome`](#decisions-and-reasons)                                                                                                                                       | Any `Decision[T]`; a decision or one of its reasons; one reason                                                      |
| [`None`](#decisions-and-reasons)                                                                                                                                                                       | The payload of a decision that carries only a reason                                                                 |
| [`Matched[T]`](#typed-matching)                                                                                                                                                                        | One entry `MatchAll` returns                                                                                         |
| [`LoadOption`, `Params`, `Require`, `RequireOption`, `From`](#load-options)                                                                                                                            | Options for `Load` and `Compile`                                                                                     |
| [`MapFS(files) fs.FS`](#mapfs)                                                                                                                                                                         | A map of file names to contents as an `fs.FS`                                                                        |
| [`Policy[In].Eval`, `.Name`](#evaluating)                                                                                                                                                              | Evaluating a compiled policy, and its name                                                                           |
| [`Result`, `Entry`, `Trace`, `Candidate`, `Condition`](#result)                                                                                                                                        | What an evaluation produced and why                                                                                  |
| [`Position`](#positions)                                                                                                                                                                               | A place in a bundle, with `IsValid` and `String`                                                                     |
| [`CompileError`, `Diagnostic`](#compile-errors)                                                                                                                                                        | A failed compile and its diagnostics: `Message`, `Help`, `Position`, `End`                                           |
| [`RuntimeError`, `ConflictError`, `AssertionError`, `AssertFailure`, `HostPanicError`](#errors)                                                                                                        | A failed evaluation, and the cause of a runtime error a recovered host panic became                                  |
| [`AssertPhase`, `InputAsserts`, `OutcomeAsserts`](#assertionerror)                                                                                                                                     | Which asserts an `AssertionError` reports                                                                            |

Package [`policytest`](#package-policytest) exports `Run` and `Schema`; package [`cli`](#package-cli) exports `Main`, `Option`, `WithKind` and `WithVersion`.
