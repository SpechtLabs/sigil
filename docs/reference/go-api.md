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

`Load` compiles a policy by name from an `fs.FS` and resolves its `use` statements through the same filesystem, so `embed.FS` and `os.DirFS` both work. The third argument binds params from Go; `nil` means the policy file binds everything it needs.

```go
//go:embed policies
var policies embed.FS

p, err := Deploy.Load(policies, "payments.production", nil)
if err != nil {
	log.Fatal(err) // file:line:col plus a fix hint
}
```

Binding params from Go, for example from a CRD, uses `policy.Params`. The values are type-checked against the `param` declarations at compile time, just like a `use` binding:

```go
p, err := Deploy.Compile(src, policy.Params{
	"approvers": []string{"payments-leads"},
	"min_soak":  4 * time.Hour,
})
```

`Compile` does the same as `Load` for a single source string. A compiled policy is immutable and safe for concurrent use.

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

What `Policy` holds is still open. When the host evaluates `payments.production` and the `service_owner` review wins, that rule lives in the `use`d `deploy.production` base, so `Policy` could name either one. See [Open questions](/project/open-questions/). `Trace` lists every candidate by policy name, reason and source position, and records which conditions held for the winner. See [Evaluation semantics](/reference/evaluation/) for how the winner is picked.

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
