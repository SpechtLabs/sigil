---
title: Go API
icon: mdi:language-go
createTime: 2026/09/24 22:30:00
permalink: /reference/go-api/
---

This page is for Go developers embedding Sigil in a service: defining a kind from Go types, loading and compiling policies, evaluating them, and reloading them at run time. Everything lives in package `policy`, import path `github.com/spechtlabs/sigil/pkg/policy`. Two companion packages build on it: [`policytest`](#testing-policies) runs policy tests from `go test`, and [`cli`](#exporting-the-kind) builds the `sigil` command line into the host's own binary. [The example service](/guides/example-service/) uses all three in a complete Go host.

The API mirrors `regexp`: define a kind once at package level, compile policies once, and evaluate them many times from any goroutine. It's pre-1.0, so names and signatures can still change.

## Defining a kind

A kind starts as ordinary Go structs. The input struct's fields become the kind's `input`s, and nested structs become `type`s. Fields are exposed through `policy:` tags; untagged fields are invisible to policies.

```go
type Input struct {
	Release     Release `policy:"release"`
	Service     Service `policy:"service"`
	Actor       Actor   `policy:"actor"`
	Environment string  `policy:"environment"`
}

type Service struct {
	Name   string            `policy:"name"`
	Tier   string            `policy:"tier"`
	Owners []string          `policy:"owners"`
	Labels map[string]string `policy:"labels"`
}
```

Each decision gets a payload struct. The reason is implicit on every decision, so it never appears in the struct. Payload defaults go in the tag, after the name: `default=` takes a Sigil constant of the field's type. It's the only tag option, and only payload fields take it; an option on an input or on a field of a nested struct makes `NewKind` panic, as a default on a `type` field is an error in a kind file.

```go
type ReviewData struct {
	Approvers []string `policy:"approvers"`
}

type ApproveData struct {
	Bake time.Duration `policy:"bake,default=1h"`
}
```

Decisions are typed handles, declared with their reasons. `policy.None` is the payload for a decision that carries only a reason.

```go
var (
	Deny    = policy.NewDecision[policy.None]("deny", "not_eligible", "soak_too_short", "no_rule_matched")
	Review  = policy.NewDecision[ReviewData]("review", "service_owner")
	Approve = policy.NewDecision[ApproveData]("approve", "release_manager", "payments_sre")
)
```

The reasons are plain strings because `Result.Reason` is one. A handle also reports what it declares: `Name()` returns the decision's name and `Reasons()` its reasons.

Go code that names a reason, to rank it, make it the default or compare a result against it, does so through a reason handle, a `policy.Outcome`. `Reason` returns one and panics when the decision doesn't declare the name, with the hint the checker gives for the same typo in a policy, so a misspelled reason stops the program at init instead of compiling into a comparison that never matches:

```go
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

`Decision()` and `Name()` return the handle's decision and reason names.

`NewKind` ties it together:

```go
var Deploy = policy.NewKind[Input]("DeployApproval",
	policy.WithVersion(1),
	policy.WithDecisions(Deny, Review, Approve), // order = precedence
	policy.WithReasonPrecedence(ReleaseManager, PaymentsSRE),
	policy.WithDefault(NoRuleMatched),
	policy.WithFunc("split", strings.Split),
)
```

| Option | Kind file equivalent | Notes |
| --- | --- | --- |
| `policy.WithVersion(n)` | `kind DeployApproval version n` | Contract version, bumped by every change to the kind. Required, and at least 1 |
| `policy.WithAccepts(n)` | `kind DeployApproval version 3, accepts: n` | Oldest version a policy or module may pin; raise it with a breaking change. From 1 to the version; without it, every version is accepted |
| `policy.WithDecisions(d...)` | `decision ...`, `collect one` and `precedence ...` | Argument order is precedence, highest first |
| `policy.WithCollect(d...)` | `decision ...` and `collect all` | Instead of `WithDecisions`: every fired decision applies. Argument order is declaration order |
| `policy.WithPrecedence(d...)` | `precedence ...` in a `collect all` kind | Ranks a `WithCollect` kind's decisions, so the outcome is every candidate at the top rank. Must list every decision |
| `policy.WithReasonPrecedence(reasons...)` | `precedence approve: release_manager > payments_sre` | Ranks one decision's reasons, highest first, given as reason handles; must list them all, and only that decision's |
| `policy.WithExclusive(outcomes...)` | `exclusive grant_a, grant_b` | Outcomes that can't fire together. An outcome is a decision handle, or `GrantA.Reason("x")` for one reason |
| `policy.WithDefault(reason)` | `default deny(no_rule_matched)` | Result when no rule fires, given as a reason handle; payload fields take their defaults, so every payload field of its decision needs one. Required for `collect one` |
| `policy.WithFunc(name, fn)` | `fn split(string, string) -> list<string>` | One call per function. The Sigil signature is derived from the Go function's type, which returns `T` or `(T, error)`. Host functions must be pure, must terminate and must not panic; see [Evaluating](#evaluating) |
| `policy.WithRecoverHostPanics()` | none; it's host behavior, not contract | A panic in a host function becomes a `*RuntimeError` instead of unwinding out of `Eval`. Off by default; see [Evaluating](#evaluating) |

::: warning Planned
[Host-ordered types](/reference/types/#host-ordered-types), `type Version ordered` in a kind file and `policy.WithOrdered[T](name)` in Go, don't exist yet.
:::

Options that take several values add up, so `policy.WithDecisions(Deny, Review)` and `policy.WithDecisions(Deny), policy.WithDecisions(Review)` declare the same kind, in the same order. A kind takes `policy.WithDecisions` or `policy.WithCollect`, never both and never neither. A collecting kind may leave out `policy.WithDefault`:

```go
var Access = policy.NewKind[AccessInput]("AccessGrant",
	policy.WithVersion(1),
	policy.WithCollect(Read, Write, Admin, CustomerDataWriter, DevEnvWriter),
)
```

`NewKind` reflects over `Input` once and records the index path of every field, so `Eval` reads the host's values in place, without copying them or dispatching on types per call. `policy.WithFunc` derives the Sigil signature from the Go function's type, but not its name. The name is part of the policy contract, like an input's `policy:"..."` tag, so it's always written out: a derived name would let a Go refactor that renames `strings.Split` rename a function in every policy, and function literals, which are how most adapters are written, have no usable name anyway.

The types `NewKind` accepts are listed in [Kind files](/reference/kind-files/#go-type-mapping). Anything else (channels, funcs, interfaces, pointers to slices or maps, map keys that aren't scalars, unexported tagged fields, tag options outside a payload) makes `NewKind` panic at init, and so does a kind that breaks a [validity rule](/reference/kind-files/#validity-rules), such as a missing version or default. The panic lists every problem at once:

```text
policy.NewKind(K): invalid kind:
  p: *[]string is a pointer to a slice (use the slice itself; a nil slice already reads as an empty list)
  invalid kind version 0 (the version is a positive integer that changes when the contract does)
```

That's deliberate: a kind that exists can always be exported, and the exported kind file parses back into the same contract. `Name()` returns the kind's name and `Schema()` its kind file (see [Exporting the kind](#exporting-the-kind)).

## Loading and evaluating

`Load` reads every `.sigil` file in an `fs.FS` into one bundle, indexes the documents by the names in their headers, and compiles the policy with the given name as the root. Imports resolve by name within the bundle, so files are plain containers: one file per policy, one file per team, or everything in one file all work, and so do `embed.FS` and `os.DirFS`. Everything after the name is a load option.

```go
//go:embed policies
var policies embed.FS

p, err := Deploy.Load(policies, "payments.production",
	policy.Require("deploy.guardrails"))
if err != nil {
	log.Fatal(err) // file:line:col plus a fix hint
}
```

| Option | Does |
| --- | --- |
| `policy.Params{...}` | Binds the root policy's params from Go |
| `policy.Require(name, ...)` | Requires the root policy to invoke the named policy unconditionally |
| `policy.From(fsys)` | Inside `Require`: takes the required policy, and everything it uses, from a trusted source (see [below](#where-required-policies-come-from)) |

Binding params from Go, for example from a CRD, uses `policy.Params`. Each value is a Go value of the shape `NewKind` accepts for the param's type: a `string` for `string`, a `[]string` for `list<string>`, a `time.Duration` for `duration`, and so on. The values are type-checked against the `param` declarations and their [`min` and `max`](/reference/policy-files/#bounds) at compile time, just like invocation arguments:

```go
p, err := Deploy.Load(policies, "deploy.gate",
	policy.Params{
		"approvers": []string{"payments-leads"},
		"min_soak":  4 * time.Hour,
	},
	policy.Require("deploy.guardrails"),
)
```

A param the root doesn't declare, a value of the wrong type or outside the bounds, and a param without a default that `Params` doesn't bind are all compile errors. Several `Params` options merge.

`Compile` does the same as `Load` for a single source string, `Deploy.Compile(src, "payments.production", opts...)`. The source is a one-file bundle: it may hold several documents, and imports resolve among them. Every document is checked, so a broken document fails the compile even when the root never uses it. A compiled `*policy.Policy` is immutable and safe for concurrent use; its `Name()` returns the root's name. A host can't evaluate a module: `Load` on a module's name fails with `deploy.common is a module, not a policy`.

`Load` and `Compile` return a `*policy.CompileError` holding every diagnostic, each with its position, the document it's in and a fix hint. `Load` also returns the error from reading `fsys` when the files can't be read. The `CompileError` message quotes the offending lines the way the CLI does. A policy compiled from a string has no file, so its positions start at the line:

```text
3:14 (deploy.gate): error: unknown field "teir" on type Service
  |
3 | when service.teir == "critical" {
  |              ^^^^
  = help: did you mean "tier"? Service declares: name, tier, owners, labels
```

### Bundles and ConfigMaps

Which files `Load` reads, and how it treats duplicates, is specified in [Bundles and resolution](/reference/policy-files/#bundles-and-resolution). In short: every file ending in `.sigil`, skipping names that start with `.`, with a name defined twice being a compile error, and any broken document failing the load.

The loader decides what's a file with `fs.Stat`, which follows symbolic links, never with `DirEntry.Type()`. In a mounted ConfigMap every top-level key is a symbolic link into `..data/`, so a loader that only accepted regular directory entries would load nothing at all, without an error.

A bundle holds the documents of one kind. A policy or module written for another kind is a compile error (`document is for kind AccessGrant, not DeployApproval`), so a host with two kinds reads them from separate directories.

A kind document in the bundle is never taken as the contract. The contract is the host's Go definition. If the bundle holds a kind document with the host kind's name, `Load` compares it with `Deploy.Schema()` and fails on any difference, which also catches a stale export: a policy repository that forgot to regenerate `deploy_approval.sigil` fails to load instead of being checked against an outdated contract. Kind documents for other kinds are ignored.

A Kubernetes ConfigMap mounted as a volume is a directory, so `os.DirFS` reads it unchanged. Kubelet's hidden `..data` directory and its timestamped siblings start with `.`, so the loader skips them and reads each key once:

```go
p, err := Deploy.Load(os.DirFS("/etc/sigil"), "payments.production",
	policy.Require("deploy.guardrails"))
```

A host that reads the ConfigMap through the Kubernetes API gets its `data` as a `map[string]string`. `policy.MapFS` turns that into an in-memory `fs.FS`, with each key as a file name. Keys need the `.sigil` extension to be loaded, like any other file:

```go
cm, err := client.CoreV1().ConfigMaps("deploy-gate").Get(ctx, "deploy-policies", metav1.GetOptions{})
if err != nil {
	return err
}

p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails"))
```

`testing/fstest.MapFS` would do the same job, but it wants a `*fstest.MapFile` per entry and lives in a testing package, so `policy.MapFS` saves every host the conversion. Mixing layouts works too: one key per team, or everything in one key. The host always names the root, so one ConfigMap can serve many policies.

### Positions in errors and traces

Every compile error and every trace entry carries the file, line and column, plus the name of the document it's in, as a `policy.Position`. Line and column are 1-based, and the column counts characters. In text form the name follows the position in parentheses, `policies.sigil:42:5 (payments.production)`, which stays readable when forty documents share one key. `Position.String()` leaves the name out when the file's path already says it, so `deploy/production.sigil:16:5` stays as it is. The Go values always carry both. A policy compiled from a string has no file, so its positions read `4:3 (payments.production)`. A position with line 0 is unknown, as for the kind's default decision, which has no source; `IsValid()` reports false for it and `String()` returns `-`.

### Required policies

`policy.Require` names the policies a root policy must invoke unconditionally. The compiler checks that each one is reachable from the root through top-level invocations only, with no `when` anywhere on the path, and fails the load otherwise, with an error pointing at the gated call or at the root's header when the call is missing. See [Evaluation semantics](/reference/evaluation/#required-policies) for what that guarantees.

The requirement is transitive: the call doesn't have to be in the root file. A shared "team baseline" policy that invokes `deploy.guardrails` at its top level satisfies `Require` for every team that invokes the baseline at theirs, at any depth. [`sigil explain`](/reference/cli/#sigil-explain) shows where each rule comes from, since every call chain starts in the root.

Put the requirement wherever the host loads team policies, and name the policies that hold the denies no team may switch off. The `sigil check --require` flag runs the same check in a policy repository's CI.

#### Where required policies come from

Documents resolve by name, so on its own `Require` only checks that *a* policy called `deploy.guardrails` is invoked, not *which* one. Anyone who can write to the bundle, for example to the ConfigMap behind `policy.MapFS(cm.Data)`, could ship a `deploy.guardrails` with no denies in it and pass the check. `policy.From` closes that by naming the source a required policy must come from:

```go
//go:embed platform
var platformFS embed.FS // or a platform-owned ConfigMap, mounted separately

p, err := Deploy.Load(policy.MapFS(cm.Data), "payments.production",
	policy.Require("deploy.guardrails", policy.From(platformFS)))
```

With `From`, the loader reads the trusted source as its own bundle, separate from the one passed to `Load`:

- The required policy is taken from the trusted source, and so is everything it imports and invokes. A trusted policy never resolves a name in the untrusted bundle, so a team can't redefine `deploy.common.eligible` to switch a guardrail off from underneath it.
- Every name the trusted source defines is reserved. A document in the untrusted bundle that claims one of them, a `deploy.guardrails` or a `deploy.common`, is a compile error naming both definitions, not a silent override in either direction.
- Several `Require` options may name the same source, and it's read once. The loader recognizes the same source by value for a comparable `fs.FS`, such as `embed.FS`, `os.DirFS` or `fs.Sub`, and by map identity for a map-backed one such as `policy.MapFS`.
- Team policies import and invoke trusted documents by name as usual, so `use deploy.guardrails` and `use deploy.common.{cleared}` work unchanged.

Without `From`, the required policy is looked up in the bundle like any other document. That's fine when the whole bundle is trusted, for example an `embed.FS` built from a reviewed repository, and it's how the examples in these docs read. Whenever someone other than the platform team can write to the bundle, pass `From`.

A required policy bounds its own params with [`min` and `max`](/reference/policy-files/#bounds); `Require` takes no bounds of its own.

### Evaluating

`Eval` runs the policy against one input:

```go
res, err := p.Eval(ctx, input)
if err != nil {
	// res still holds the kind's default decision
}
```

`Eval` never returns a nil result. When it returns an error, the result holds the kind's default decision, or an empty outcome for a collecting kind, so a host that fails closed can use `res` directly. The error is one of these:

| Error | When | Fields |
| --- | --- | --- |
| `*policy.RuntimeError` | An index out of range, integer overflow, or a host function that returned an error, or panicked under `WithRecoverHostPanics` | `Message`, `Help`, `Policy` (the root), `Position` (the expression that failed), `Err` (the host function's error, or a `*HostPanicError`) |
| `*policy.ConflictError` | A [conflict](/reference/evaluation/#resolution): two members of an `exclusive` set fired, or a `collect one` kind has several candidates at its top rank | `Message`, `Policy`, `Candidates` (only those that conflict: the top-rank tie or the exclusive set's members; the trace has every candidate) |
| `*policy.AssertionError` | An [assert](/reference/evaluation/#assertions) failed | `Failures`, one `AssertFailure` per failing assert; `Phase`, `policy.InputAsserts` or `policy.OutcomeAsserts` |
| The context's error | `ctx` was done before or during the evaluation | Unwrapped: `errors.Is(err, context.DeadlineExceeded)` holds for a deadline |

`Eval` checks the context before it starts, before every rule and assert, after every host function call, and every few hundred elements a loop goes through, so a deadline cuts off nested quantifiers over a large input. The result is then the kind's default with an empty trace, however far the evaluation got; see [Failed evaluations](/reference/evaluation/#failed-evaluations). Counting loop steps costs about 2.5% in the tightest loop, a quantifier comparing two ints, and nothing measurable on the [evaluation benchmarks](/reference/performance/). Under `context.Background()`, which is never done, nothing is polled.

A `*RuntimeError` keeps its cause: `Err` holds what the host function returned, and `RuntimeError` unwraps to it, so `errors.Is` and `errors.As` find the host's own error through the policy:

```go
res, err := p.Eval(ctx, input)
if errors.Is(err, registry.ErrUnavailable) {
	// a host function couldn't reach the registry; res holds the default
}
```

Host functions must terminate and must not panic. `Eval` can't interrupt a function that never returns, but it stops as soon as one returns after the context is done. Report a failure by returning an error, which becomes a `*RuntimeError` with the kind's default in the result.

By default a panic in a host function isn't recovered: it propagates out of `Eval` and crashes the calling goroutine unless something above recovers it. That's the usual Go answer to a bug, and under `net/http` it's what already happens: the server recovers a handler's panic and logs it with the stack. A queue consumer or a reconcile loop has nothing above it, so one bad input would kill the worker. For those, declare the kind with `policy.WithRecoverHostPanics()`: a panic then becomes a `*RuntimeError` whose message names the function and the panic value, the policy fails closed with the kind's default, and `Err` is a `*policy.HostPanicError` carrying the `Value` and the `Stack` for the host to log. The stack stays out of the message.

```go
var hp *policy.HostPanicError
if errors.As(err, &hp) {
	log.Error("host function panicked", "func", hp.Func, "panic", hp.Value, "stack", string(hp.Stack))
}
```

A conflict error reads like the trace: which rules, at which positions, claimed what. Count conflicts as policy defects, apart from runtime errors and assert failures.

An `AssertionError` lists every assert that failed in the phase that stopped evaluation, input or outcome, sorted by position, including asserts whose own condition raised a runtime error. `Phase` says which phase that was, and with it whose fault the failure is: `policy.InputAsserts` rejects the caller's input, and `policy.OutcomeAsserts` means the policy produced an outcome it forbids, a defect in the policy. Read the phase from `Phase`, not from the trace: the trace is empty after a failed input assert, and also after a failed outcome assert when no rule fired. `Phase.String()` returns `input` or `outcome`, for a metric label. Tell an `AssertionError` apart from a runtime error with `errors.As`, and count it separately:

```go
res, err := p.Eval(ctx, input)
var ae *policy.AssertionError
switch {
case errors.As(err, &ae):
	for _, f := range ae.Failures { // every failing assert, sorted by position
		assertFailures.WithLabelValues(ae.Phase.String(), f.Reason).Inc()
		log.Error("policy assertion failed", "phase", ae.Phase, "reason", f.Reason, "at", f.Location())
	}
case err != nil:
	log.Error("policy evaluation failed", "err", err)
}
```

Each `AssertFailure` carries the assert's `Reason`, the `Policy` it's in, its `Position` and `CallChain`, the `*RuntimeError` in `Cause` if its condition raised one and, for an outcome assert, the candidates that formed the outcome it read in `Outcome`. `Location()` renders the call chain and position. The result that comes with the error holds the kind's default, and its trace lists every candidate the rules produced: none when an input assert failed, since no rule ran.

## Result

```go
type Result struct {
	Decision string         // "review"
	Reason   string         // "service_owner"
	Policy   string         // "payments.production": the policy the host evaluated
	Payload  map[string]any // untyped view; use Decision[T].Match for typed
	Outcome  []Entry        // the winner, or for a collecting kind every candidate, sorted; may be empty
	Trace    Trace          // all candidates, with conditions for the winning decision
}

type Entry struct {
	Decision string
	Reason   string
	Policy   string         // the policy whose rule produced it; empty for the kind's default
	Payload  map[string]any // defaults filled in
	Position Position       // of the constructor; unknown for the default
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

type Condition struct {
	Text     string // the condition as written, on one line
	Position Position
}
```

For `collect one`, `Decision`, `Reason` and `Payload` describe the winner, or the kind's default when nothing fired, and `Outcome` holds that one entry. For `collect all`, those single fields are empty. `Outcome` holds every folded candidate without precedence, or every folded candidate at the top rank with precedence. One `Result` type serves both modes; whether a collecting kind should get its own is still [open](/project/open-questions/#collecting-kinds).

`Policy` on the result names the policy the host evaluated. When the host evaluates `payments.production` and the `service_owner` review wins, that rule lives in `deploy.production`, reached through an invocation, so the entry and the trace candidate name `deploy.production` instead, and the candidate's `CallChain` says how it was reached. `Trace` lists every candidate by policy name, reason and call chain, with each step's file, line, column and document name, and records which conditions held for every candidate of the winning decision. `Candidate.Location()` renders the chain, for example `payments/production.sigil:10:3 → deploy/production.sigil:16:5`, and `Candidate.String()` renders the whole candidate on one line, the way a trace prints it. See [Evaluation semantics](/reference/evaluation/) for how the winner is picked.

## Typed matching

The untyped `Payload` map is there for logging and generic tooling. Application code should match on the decision handle and get the payload struct back:

```go
if r, ok := Review.Match(res); ok {
	requestReview(r.Approvers, res.Reason) // r is a typed ReviewData
}

if a, ok := Approve.Match(res); ok {
	startRollout(a.Bake)
}
```

`Match` returns `true` when the outcome is exactly one entry of that decision. It returns `false` if the result is a different decision, and, on a `collect all` kind with `precedence`, when the top rank holds more than one entry. The fallback result that comes with an error holds the kind's default, so `Deny.Match` reports `true` on it: check `err` before matching.

To check the reason as well, use the reason handle's `Is`, which follows the same rules as `Match` and compares the reason too:

```go
if NoRuleMatched.Is(res) {
	flagUncovered(p.Name()) // the default: no rule covers this deploy
}
```

Comparing `res.Reason` with a string compiles with a typo in it and never matches; `Is` can't, since the handle was checked when it was declared.

A collecting kind can grant a decision more than once, so it matches with `MatchAll`, which returns a `policy.Matched[T]` for every entry of that decision, in outcome order, with its typed `Payload`, `Reason`, `Policy` and `Position`:

```go
for _, g := range Admin.MatchAll(res) {
	grantAdmin(g.Payload.TTL, g.Reason) // g.Payload is a typed AdminData
}
```

`Match` and `Is` on a `collect all` kind without precedence panic, since there's no single decision to match. Use `MatchAll` for that mode. On a `collect one` kind, `MatchAll` returns the winner when it's that decision, and nothing otherwise.

## Dynamic input

::: warning Planned
`Resolver` is a proposal, not an exported API.
:::

Today a Go host decodes external input into its input struct before calling `Eval`, and the CLI uses its own JSON decoder. The proposal would let a host supply values by path instead:

```go
type Resolver interface {
	Get(path string) (Value, bool)
}
```

The evaluator would check each resolved value against the kind's schema and reject a type mismatch with the failing path. The interface and how it integrates with `Eval` are undecided.

## Hot reload

Compiled policies are immutable, so reload is a pointer swap. In-flight evaluations keep using the policy they started with.

```go
var current atomic.Pointer[policy.Policy[Input]]

current.Store(p)

res, err := current.Load().Eval(ctx, input)
```

Reload with last-known-good semantics. A bundle loads as a whole, so on reload one team's typo in a shared bundle fails the compile for every policy in it. Compile the new bundle first, swap only on success, and otherwise keep serving the old policy, log the error and count it:

```go
func reload(fsys fs.FS) {
	p, err := Deploy.Load(fsys, "payments.production",
		policy.Require("deploy.guardrails", policy.From(platformFS)))
	if err != nil {
		log.Error("policy reload failed, keeping the last good policy", "err", err)
		reloadFailures.Inc() // alert on this: the running policy is now stale
		return
	}
	current.Store(p)
}
```

At startup there's no last good policy, so a failed `Load` should stop the process. On Kubernetes that holds a rollout at the old pods instead of serving without a policy. The [example service](/guides/example-service/)'s `internal/store` package does all of this for several roots at once, with reloads on a signal and on a poll.

## Exporting the kind

`Deploy.Schema()` returns the kind file text. The policy repository checks it in as `deploy_approval.sigil`, where the `sigil` CLI reads it without importing the host's code. The defining host uses its Go definition as the source of truth.

The simplest way to write it is the host's own `sigil` binary, built with package `cli` (see [Host binaries](/reference/cli/#host-functions-and-host-binaries)), whose `sigil export` writes the linked kind's `Schema()`:

```go
// cmd/sigil/main.go in the host repository
package main

import (
	"github.com/spechtlabs/sigil/pkg/cli"

	"example.com/deploygate"
)

//go:generate go run . export --out ../../policies/deploy_approval.sigil

func main() {
	cli.Main(cli.WithKind(deploygate.Deploy))
}
```

| `cli` API | Does |
| --- | --- |
| `cli.Main(opts...)` | Runs the whole `sigil` command line on `os.Args` and exits: status 1 when the command failed, after printing why, and 0 otherwise |
| `cli.WithKind(k)` | Links a kind into the binary. Repeat it for a host with several kinds |
| `cli.WithVersion(v)` | Sets the version `sigil version` reports |

A test keeps the copy honest, so a kind change that wasn't exported fails `go test`:

```go
func TestKindFileIsCurrent(t *testing.T) {
	policytest.Schema(t, Deploy, "../policies/deploy_approval.sigil")
}
```

`Schema()` prints the kind in `sigil fmt`'s canonical style, so the exported file also passes `sigil fmt --check`.

## Testing policies

Package `policytest`, import path `github.com/spechtlabs/sigil/pkg/policytest`, runs the test files `sigil test` runs, from `go test`, with the host's Go types and real function implementations:

```go
func TestPolicies(t *testing.T) {
	policytest.Run(t, Deploy, os.DirFS("../policies"), policy.Require("deploy.guardrails"))
}
```

| Function | Does |
| --- | --- |
| `policytest.Run(t, k, fsys, opts...)` | Runs every `*_test.yaml` file in `fsys`. Each file's `policy:` is loaded from `fsys` with `k.Load` and `opts`, the same options the host passes to `Load` |
| `policytest.Schema(t, k, file)` | Fails the test when the kind file at `file`, on disk, isn't `k.Schema()` |

Each test file becomes a subtest named after its path, and each case a subtest of it, so `go test -run` selects them. A test file that can't be read, or whose policy doesn't load, fails its subtest; a case that doesn't get what it expects fails its own. The test file format is described under [`sigil test`](/reference/cli/#sigil-test). A test file can't expect a conflict, so test one with `Eval` and `errors.As` on a `*ConflictError`, as [Testing a conflict](/reference/cli/#testing-a-conflict) shows with plain `testing` and with Ginkgo.

`Kind.Contract` exists for the tooling in this module, `cli` and `policytest`, and returns a type that nothing outside it can use.

## Loading a kind elsewhere

::: warning Planned
`policy.LoadKind`, which would load a kind from its kind file at run time, doesn't exist yet, and neither does an API to bind host functions to such a kind.
:::

Until it does, a Go service that consumes a kind it doesn't define imports the defining host's package, and tooling reads the exported kind file through the CLI. The stock `sigil` binary checks policies against an exported kind file without the host's Go code, and evaluates them as long as no rule reaches a host function call; reaching one is a runtime error. A host binary built with `pkg/cli` supplies the real implementations.

The planned [`sigil gen go`](/reference/cli/#sigil-gen-go) command will generate typed payload structs from a kind file, for services that want them without importing the host.

## Package index

Every exported identifier of package `policy`, for reference:

| Identifier | What it is |
| --- | --- |
| `NewKind[In](name, opts...) *Kind[In]` | Builds a kind from the input struct `In`; panics on an invalid kind |
| `Kind[In].Name`, `.Schema`, `.Load`, `.Compile`, `.Contract` | The kind's name, its kind file, loading and compiling policies, and the tooling hook |
| `Option`, `WithVersion`, `WithAccepts`, `WithDecisions`, `WithCollect`, `WithPrecedence`, `WithReasonPrecedence`, `WithExclusive`, `WithDefault`, `WithFunc`, `WithRecoverHostPanics` | Options for `NewKind` |
| `NewDecision[T](name, reasons...) Decision[T]` | Declares a decision with payload struct `T` |
| `Decision[T].Name`, `.Reasons`, `.Reason`, `.Match`, `.MatchAll` | The decision's name and reasons, one reason as an `Outcome` handle (panics on an undeclared one), and typed matching |
| `Outcome.Decision`, `.Name`, `.Is` | A reason handle's decision and reason names, and whether a result is exactly that reason |
| `DecisionRef`, `OutcomeRef`, `Outcome` | Any `Decision[T]`; a decision or one of its reasons; one reason |
| `None` | The payload of a decision that carries only a reason |
| `Matched[T]` | One entry `MatchAll` returns |
| `LoadOption`, `Params`, `Require`, `RequireOption`, `From` | Options for `Load` and `Compile` |
| `MapFS(files) fs.FS` | A map of file names to contents as an `fs.FS` |
| `Policy[In].Eval`, `.Name` | Evaluating a compiled policy, and its name |
| `Result`, `Entry`, `Trace`, `Candidate`, `Condition` | What an evaluation produced and why |
| `Position` | A place in a bundle, with `IsValid` and `String` |
| `CompileError`, `Diagnostic` | A failed compile and its diagnostics: `Message`, `Help`, `Position`, `End` |
| `RuntimeError`, `ConflictError`, `AssertionError`, `AssertFailure`, `HostPanicError` | A failed evaluation, and the cause of a runtime error a recovered host panic became |
| `AssertPhase`, `InputAsserts`, `OutcomeAsserts` | Which asserts an `AssertionError` reports |
