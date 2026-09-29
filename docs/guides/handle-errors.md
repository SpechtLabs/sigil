---
title: Handle failed evaluations
icon: mdi:alert-circle-outline
createTime: 2026/09/29 12:00:00
permalink: /guides/handle-errors/
---

An evaluation can fail: an assert doesn't hold, two rules conflict, a rule hits a runtime error, or the context ends. This guide makes your host fail closed on every one of them, tell them apart by whose fault they are, count them apart from real decisions, and bound how long an evaluation may take. It builds on the `Deploy` kind from [Embed Sigil in a Go service](/guides/embed-go/).

## Fail closed

`Eval` never returns a nil result. When it returns an error, the result holds the kind's default decision, `deny(no_rule_matched)` for `DeployApproval`, or an empty outcome for a collecting kind. A kind that [names its conflicts](#name-conflicts-in-the-result) returns its conflict outcome after a conflict instead. Act on that result and handle the error apart:

```go
res, err := p.Eval(ctx, input)
if err != nil {
	// res holds deny(no_rule_matched): refuse the deploy, and report err
	return reject(res, err)
}
```

Check `err` before anything reads the result. The fallback is a real `deny`, so `Deny.Match(res)` reports `true` on it, and a switch on the result alone can't tell a failure from a deny.

What each failure returns, outcome and trace, is in [Failed evaluations](/reference/evaluation/#failed-evaluations). Why every failure fails closed: [Strict schema, forgiving data](/understanding/strictness/#every-failure-fails-closed).

## Tell the failures apart

Each failure has its own error type, and the type says whose fault it is. Match them with `errors.As` and `errors.Is`:

| Error                                                          | Whose fault                                                                                    | Status deploygate answers |
| -------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- | ------------------------- |
| `*policy.AssertionError` with `Phase == policy.InputAsserts`   | The caller's: the policy rejected the input before any rule ran                                | `422`                     |
| `*policy.AssertionError` with `Phase == policy.OutcomeAsserts` | The policy's: it produced an outcome it forbids                                                | `500`                     |
| `*policy.ConflictError`                                        | The policy's: rules claimed outcomes that can't stand together                                 | `500`                     |
| `*policy.RuntimeError`                                         | The policy's or the host's: an index out of range, an overflow, or a host function that failed | `500`                     |
| `context.DeadlineExceeded`                                     | The service's: it didn't decide in time                                                        | `503`                     |
| `context.Canceled`                                             | No one's: the caller left before the answer                                                    | `499`, no body            |

In Go, the same mapping:

```go
// classify says what kind of failure err is, for a metric label, and
// whose fault it is, as an HTTP status.
func classify(err error) (kind string, status int) {
	var (
		ae *policy.AssertionError
		re *policy.RuntimeError
		ce *policy.ConflictError
	)
	switch {
	case errors.As(err, &ae) && ae.Phase == policy.InputAsserts:
		return "assertion", http.StatusUnprocessableEntity // the input is invalid
	case errors.As(err, &ae):
		return "assertion", http.StatusInternalServerError // the policy forbids its own outcome
	case errors.As(err, &re):
		return "runtime", http.StatusInternalServerError
	case errors.As(err, &ce):
		return "conflict", http.StatusInternalServerError
	case errors.Is(err, context.DeadlineExceeded):
		return "timeout", http.StatusServiceUnavailable
	case errors.Is(err, context.Canceled):
		return "", 499 // the client left; nothing failed
	}
	return "unknown", http.StatusInternalServerError
}
```

Match the policy's error types before the context's. A `*RuntimeError` unwraps to the host function's own error, so a host function that returns its own `context.DeadlineExceeded` would otherwise read as the evaluation running out of time.

Read the assert phase from `Phase`, not from the trace. The trace is empty after a failed input assert, and also after a failed outcome assert when no rule fired.

The errors carry what you need for the log:

- An `*AssertionError` lists every assert that failed in `Failures`, sorted by position. Log each one's `Reason` and `Location()`:

  ```go
  var ae *policy.AssertionError
  if errors.As(err, &ae) {
  	for _, f := range ae.Failures {
  		slog.Error("policy assertion failed", "phase", ae.Phase, "reason", f.Reason, "at", f.Location())
  	}
  }
  ```

- A `*ConflictError` prints like the trace: which rules, at which positions, claimed what. Log `err` as it is.
- A `*RuntimeError` keeps the host function's error in `Err` and unwraps to it, so `errors.Is` finds your own sentinel errors through the policy:

  ```go
  res, err := p.Eval(ctx, input)
  if errors.Is(err, registry.ErrUnavailable) {
  	// a host function couldn't reach the registry; res holds the default
  }
  ```

Every field of each error type is in [Errors](/reference/go-api/#errors). Why asserts and conflicts are defects rather than decisions: [Asserts and decisions](/understanding/asserts/).

## Count failures in metrics

Label your decision counter from the result only when `err` is nil. When an evaluation fails, a `collect one` kind's result holds the default, so a counter labeled from it counts the failure as `deny/no_rule_matched`, and a policy defect looks like inputs no rule matched. A kind that [names its conflicts](#name-conflicts-in-the-result) narrows that for conflicts, but not for anything else. A `collect all` kind returns an empty outcome, so the failure isn't counted at all.

Count failures in a series of their own, labeled by the kind of failure, and never under the default's reason:

```go
res, err := p.Eval(ctx, input)
if err != nil {
	if kind, _ := classify(err); kind != "" {
		evalFailures.WithLabelValues(kind).Inc()
	}

	var ae *policy.AssertionError
	if errors.As(err, &ae) {
		for _, f := range ae.Failures {
			assertFailures.WithLabelValues(ae.Phase.String(), f.Reason).Inc()
		}
	}

	return res, err
}

decisions.WithLabelValues(res.Decision, res.Reason).Inc()
```

That gives series like these, where each failure kind has its own and a canceled request has none:

```text
policy_decisions_total{decision="review", reason="service_owner"}
policy_evaluation_failures_total{error="conflict"}
policy_assert_failures_total{phase="input", reason="negative_soak"}
```

`Phase.String()` returns `input` or `outcome`, ready for a label. The kind declares every reason, so the decision series are known before the first evaluation; see [Decisions and reasons](/understanding/decisions/).

## Name conflicts in the result

Counting by the error only works where the error is. A log line, an audit record or a dashboard built from the result alone sees a conflict as `deny(no_rule_matched)`, and sends whoever reads it looking for the rule that should have matched, when several did. Give the kind a conflict outcome, so the result says what happened.

Add a reason to `deny` that no rule constructs, rank it with the other deny reasons, and pass it to `policy.WithConflict`:

```go
var (
	Deny = policy.NewDecision[policy.None]("deny",
		"not_eligible", "soak_too_short", "no_rule_matched", "conflicting_rules")

	ConflictingRules = Deny.Reason("conflicting_rules")
)

var Deploy = policy.NewKind[Input]("DeployApproval",
	// A new conflict outcome changes results, so accepts rises with the version.
	policy.WithVersion(2),
	policy.WithAccepts(2),
	policy.WithDecisions(Deny, Review, Approve), // order = precedence
	policy.WithReasonPrecedence(NotEligible, SoakTooShort, NoRuleMatched, ConflictingRules),
	policy.WithReasonPrecedence(ReleaseManager, PaymentsSRE),
	policy.WithDefault(NoRuleMatched),
	policy.WithConflict(ConflictingRules),
	policy.WithFunc("split", strings.Split),
)
```

The exported kind file ends with the two outcomes together:

```sigil
default deny(no_rule_matched)
conflict deny(conflicting_rules)
```

A conflict now comes back as `deny(conflicting_rules)`, with the same `*policy.ConflictError` and the same trace, so a counter labeled from the result counts it as `deny/conflicting_rules`. Keep counting failures by the error all the same: a failed assert, a runtime error or a deadline still returns `deny(no_rule_matched)`.

- Declaring, changing or removing the conflict outcome changes results without breaking a compile, so raise `accepts` with it, as [Evolve a kind safely](/guides/evolve-a-kind/#watch-for-changes-that-compile-but-change-results) explains.
- Only a `WithDecisions` kind takes it. A collecting kind always returns an empty outcome from a failed evaluation, and `NewKind` panics on a `WithCollect` kind with `WithConflict`.

The rules for the declaration are under [`conflict`](/reference/kind-files/#conflict), and why a kind names its conflicts under [Every failure fails closed](/understanding/strictness/#every-failure-fails-closed).

## Recover host panics

By default a panic in a host function isn't recovered. It propagates out of `Eval` and crashes the calling goroutine unless something above recovers it.

Under `net/http` that's already handled: the server recovers a handler's panic and logs it with the stack. A queue consumer or a reconcile loop has nothing above it, so one bad input kills the worker. For those, declare the kind with `policy.WithRecoverHostPanics()`:

```go
var Deploy = policy.NewKind[Input]("DeployApproval",
	// ... the options from Embed Sigil in a Go service
	policy.WithFunc("split", strings.Split),
	policy.WithRecoverHostPanics(),
)
```

A panic then becomes a `*RuntimeError` whose message names the function and the panic value, and the evaluation fails closed with the kind's default. The stack stays out of the message; `Err` is a `*policy.HostPanicError` that carries it for your log:

```go
var hp *policy.HostPanicError
if errors.As(err, &hp) {
	slog.Error("host function panicked", "func", hp.Func, "panic", hp.Value, "stack", string(hp.Stack))
}
```

Turn it on under an HTTP server too when a panic should count as a failed evaluation. The example service does: without it, gin's recovery would answer an empty `500` that its request metrics and evaluation error count never see.

The option is host behavior, not part of the contract, so it doesn't appear in the exported kind file. It's a safety net: a host function should still report a failure by returning an error, which becomes a `*RuntimeError` the same way.

## Bound evaluation time

Evaluate under a deadline, so an input that makes the policy slow can't hold a request, or a CPU, for longer:

```go
ctx, cancel := context.WithTimeout(ctx, time.Second)
defer cancel()

res, err := p.Eval(ctx, input)
if errors.Is(err, context.DeadlineExceeded) {
	// res holds the kind's default, with an empty trace
}
```

Derive `ctx` from the request's context, so the evaluation also stops, with `context.Canceled`, when the client leaves; where `Eval` checks the context is in [Context checks](/reference/evaluation/#context-checks).

A deadline limits how long one evaluation takes, not how much work an input asks for: a slow input still uses the CPU until the deadline. `Eval` also can't interrupt a host function that never returns; it stops as soon as the function returns. The compiler doesn't compute or enforce a cost budget, so bound the size of your inputs and the work your host functions do yourself, and evaluate under a deadline. A budget is [planned](/project/planned/#static-cost-analysis).

The checks cost nothing measurable on the evaluation benchmarks; see [Performance](/reference/performance/). Why Sigil always halts, and what a deadline adds: [Halting by construction](/understanding/halting/).
