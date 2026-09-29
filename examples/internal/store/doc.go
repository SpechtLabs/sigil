// Package store holds the policies deploygate serves and reloads them in
// place. A compiled policy is immutable, so a reload compiles the whole new
// bundle aside and swaps one pointer; in-flight evaluations finish on the
// bundle they started with, and a bundle that doesn't compile never replaces
// the one that serves.
//
// A [Store] is generic over a kind's input type. deploygate runs two: one for
// the DeployApproval team policies, one root per team, and one for the
// AccessGrant bundle, with the single root access.main. [NewDeploy] and
// [NewAccess] build them.
//
// # Loading
//
// A load compiles every root with [policy.Kind.Load] and [policy.Require],
// so each root has to invoke the platform's guardrails unconditionally, and
// [policy.From] reads the guardrails from the documents embedded in the
// binary, never from the bundle an operator mounts. For a team that comes
// down to:
//
//	deploy.Kind.Load(teamsFS, team+".production",
//		policy.Require("deploy.guardrails", policy.From(policies.PlatformDeploy)))
//
// The new [Snapshot] replaces the old one only when every root compiles. A
// failed load keeps the previous snapshot serving and returns a humane error
// whose cause is the [*policy.CompileError] with its diagnostics.
//
// # Reloading
//
// [Store.Watch] reloads on SIGHUP, and at every poll where the [Fingerprint]
// of the bundle's `.sigil` files changed since the last load, successful or
// not, so a broken bundle is reported once rather than at every poll. Loads
// are serialized. Each runs in a deploygate.policies.reload span and is
// counted on the [telemetry.Metrics] passed with [WithMetrics]. Each is
// logged too, except a failed [Store.InitialLoad], which its caller reports.
package store
