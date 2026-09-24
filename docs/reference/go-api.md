---
title: Go API
icon: mdi:language-go
createTime: 2026/09/24 22:30:00
permalink: /reference/go-api/
---

::: warning Planned API
Nothing on this page exists yet. It describes the Go API as currently designed, so the language reference has a concrete host to point at. Package names, function names and signatures will change before the first release.
:::

The API mirrors `regexp`: define a kind once at package level, compile policies once, and evaluate them many times from any goroutine. Everything lives in package `policy`, planned import path `github.com/spechtlabs/sigil/policy`.

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

Each decision gets a payload struct. The reason is implicit on every decision, so it never appears in the struct. Payload defaults go in the tag.

```go
type ReviewData struct {
	Approvers []string `policy:"approvers"`
}

type ApproveData struct {
	Bake time.Duration `policy:"bake,default=1h"`
}
```

Decisions are typed handles. `policy.None` is the payload for a decision that carries only a reason.

```go
var (
	Deny    = policy.Decision[policy.None]("deny")
	Review  = policy.Decision[ReviewData]("review")
	Approve = policy.Decision[ApproveData]("approve")
)
```

`NewKind` ties it together:

```go
var Deploy = policy.NewKind[Input]("DeployApproval",
	policy.Version(1),
	policy.Decisions(Deny, Review, Approve), // order = precedence
	policy.Default(Deny, "no_rule_matched"),
	policy.Func("split", strings.Split),
)
```

| Option                        | Kind file equivalent                  | Notes                                                                  |
| ----------------------------- | ------------------------------------- | ---------------------------------------------------------------------- |
| `policy.Version(n)`           | `kind DeployApproval version n`       | Contract version, compared by `sigil breaking`                         |
| `policy.Decisions(d...)`      | `decision ...` and `precedence ...`   | Argument order is precedence, highest first                            |
| `policy.Default(d, reason)`   | `default deny("no_rule_matched")`     | Result when no rule fires; payload fields take their defaults          |
| `policy.Func(name, fn)`       | `fn split(s: string, sep: string) -> list<string>` | The DSL signature is derived from the Go function's type  |

`NewKind` reflects over `Input` once and builds a precomputed accessor per field path, so `Eval` never touches `reflect`. `policy.Func` derives the DSL signature from the Go function's type.

The types `NewKind` accepts are listed in [Kind files](/reference/kind-files/). Anything else (channels, funcs, interfaces, non-string map keys, unexported tagged fields) makes `NewKind` panic at init. That's deliberate: a kind that exists can always be exported, which is what makes the round trip `Import(Export(k)) == k` hold.

## Loading and evaluating

`Load` reads every document in an `fs.FS` into one bundle, indexes the documents by the names in their headers, and compiles the policy with the given name as the root. Imports resolve by name within the bundle, so files are plain containers: one file per policy, one file per team, or everything in one file all work, and so do `embed.FS` and `os.DirFS`. Everything after the name is a load option; with none, the policy binds everything it needs.

```go
//go:embed policies
var policies embed.FS

p, err := Deploy.Load(policies, "payments.production",
	policy.Require("deploy.guardrails"))
if err != nil {
	log.Fatal(err) // file:line:col plus a fix hint
}
```

| Option                     | Does                                                                 |
| -------------------------- | -------------------------------------------------------------------- |
| `policy.Params{...}`       | Binds the root policy's params from Go                               |
| `policy.Require(names...)` | Requires the root policy to invoke each named policy unconditionally |

Binding params from Go, for example from a CRD, uses `policy.Params`. The values are type-checked against the `param` declarations at compile time, just like invocation arguments:

```go
p, err := Deploy.Load(policies, "deploy.gate",
	policy.Params{
		"approvers": []string{"payments-leads"},
		"min_soak":  4 * time.Hour,
	},
	policy.Require("deploy.guardrails"),
)
```

`Compile` does the same as `Load` for a single source string, `Deploy.Compile(src, "payments.production", opts...)`. The source is a one-file bundle: it may hold several documents, and imports resolve among them. A compiled policy is immutable and safe for concurrent use. A host can't load a module; `Load` on a module's name returns an error saying it has no rules.

### Bundles and ConfigMaps

Which files `Load` reads, and how it treats duplicates, is specified in [Bundles and resolution](/reference/policy-files/#bundles-and-resolution). In short: every file ending in `.sigil`, skipping names that start with `.`, with a name defined twice being a compile error, and any broken document failing the load.

A Kubernetes ConfigMap mounted as a volume is a directory, so `os.DirFS` reads it unchanged. Kubelet's hidden `..data` directory and its timestamped siblings start with `.`, so the loader skips them and reads each key once:

```go
p, err := Deploy.Load(os.DirFS("/etc/sigil"), "payments.production",
	policy.Require("deploy.guardrails"))
```

A host that reads the ConfigMap through the Kubernetes API gets its `data` as a `map[string]string`. `policy.MapFS` turns that into an in-memory `fs.FS`, with each key as a file name:

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

Every compile error and every trace entry carries the file, line and column, plus the name of the document it's in. In text form the name follows the position in parentheses, `policies.sigil:42:5 (payments.production)`, which stays readable when forty documents share one key. The document name is left out of text output when it adds nothing, that is when the file holds only that document and its path matches the name, so `deploy/production.sigil:16:5` stays as it is. (proposed) The Go values always carry both.

### Required policies

`policy.Require` names the policies a root policy must invoke unconditionally. The compiler checks that each one is reachable from the root through top-level invocations only, with no `when` anywhere on the path, and fails the load otherwise, with an error pointing at the gated call or at the root's header when the call is missing. See [Evaluation semantics](/reference/evaluation/#required-policies) for what that guarantees.

Put the requirement wherever the host loads team policies, and name the policies that hold the denies no team may switch off. The `sigil check --require` flag runs the same check in a policy repository's CI.

`Require` trusts names. Documents resolve by name, so anyone who can add a document to the bundle can define a policy called `deploy.guardrails`. If the platform's definition is in the bundle too, the duplicate is a compile error. If it isn't, a team's own `deploy.guardrails`, with no denies in it, satisfies the requirement. In a policy repository, close that by enabling the `path-matches-name` lint as an error and putting CODEOWNERS on the guardrail files. How a host could pin a required policy to a trusted source instead, such as one embedded in its own binary, is an [open question](/project/open-questions/#trusted-sources-for-required-policies).

::: warning Unspecified
Whether a requirement may be met through a chain of other policies ("transitive") or must be met by a call in the root file itself ("direct") is an [open question](/project/open-questions/). The check above describes the transitive reading. So is letting `Require` bound a required policy's params, for example `policy.Require("deploy.guardrails", policy.Min("min_soak", time.Hour))`.
:::

`Eval` runs the policy against one input:

```go
res, err := p.Eval(ctx, input)
if err != nil {
	// res still holds the kind's default decision
}
```

On a runtime error (index out of range, integer overflow, a host function returning an error) `Eval` returns the error together with a result holding the kind's default decision. A host that fails closed can use `res` directly.

## Result

```go
type Result struct {
	Decision string           // "review"
	Reason   string           // "service_owner"
	Policy   string           // "payments.production"
	Payload  map[string]Value // untyped view; use Decision[T].Match for typed
	Trace    Trace            // all candidates, conditions for the winner
}
```

What `Policy` holds is still open. When the host evaluates `payments.production` and the `service_owner` review wins, that rule lives in `deploy.production`, reached through an invocation, so `Policy` could name either one. See [Open questions](/project/open-questions/). `Trace` lists every candidate by policy name, reason and call chain, with each step's file, line, column and document name (for example `payments/production.sigil:14:3 → deploy/production.sigil:16:5`), and records which conditions held for the winner. See [Evaluation semantics](/reference/evaluation/) for how the winner is picked.

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

`Match` returns `false` if the result is a different decision.

## Dynamic input

Sometimes input doesn't arrive as a Go struct, for example a JSON webhook body. A `Resolver` supplies values by path, the same idea as filt-rs's `Filterable`:

```go
type Resolver interface {
	Get(path string) (Value, bool)
}
```

The evaluator checks every resolved value against the kind's schema. A resolver that returns a `string` where the kind declares `list<string>` fails loudly with an error naming the path, instead of coercing it or treating it as absent.

## Hot reload

Compiled policies are immutable, so reload is a pointer swap. In-flight evaluations keep using the policy they started with.

```go
var current atomic.Pointer[policy.Policy[Input]]

current.Store(p)

res, err := current.Load().Eval(ctx, input)
```

## Exporting the kind

`Deploy.Schema()` returns the kind file text. A `go generate` step writes it into the policy repository as `deploy_approval.sigil`, where the `sigil` CLI, the LSP server and other services pick it up without importing the host's code. The defining host never loads a kind file itself; its Go definition is the source of truth.

```go
// Illustrative: a small program in the host repo that writes Deploy.Schema() to disk.
//go:generate go run ./cmd/export-kind -o ../policies/deploy_approval.sigil
```

## Loading a kind elsewhere

Other Go services can load an exported kind dynamically with `policy.LoadKind`. Because the `fn` signatures are in the file, a loaded kind can always type-check policies. Evaluating needs an implementation for every declared function: `LoadKind` returns the list of unbound functions, `Compile` works without them, and `Eval` refuses until they're bound. A CI linter needs only the first half; a second evaluating service needs both.

For services that want typed payload structs instead of a dynamic kind, the planned `sigil gen go` command generates Go code from a kind file. See [CLI & editor tooling](/reference/cli/).
