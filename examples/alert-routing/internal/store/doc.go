// Package store holds the policies alertrouter serves and reloads them in
// place. A compiled policy is immutable, so a reload compiles the whole new
// bundle aside and swaps one pointer; in-flight evaluations finish on the
// bundle they started with, and a bundle that doesn't compile never replaces
// the one that serves.
//
// A [Store] is generic over a kind's input type, the way a host of any kind
// would write it. alertrouter runs one, for the AlertRouting team policies,
// with one root per team in the team directory; [NewRouting] builds it.
//
// # Loading
//
// A load compiles every root with [policy.Kind.Load] and [policy.Require],
// so each team's policy has to invoke platform.paging unconditionally, and
// [policy.From] reads it from the documents embedded in the binary, never
// from the bundle an operator mounts. For a team that comes down to:
//
//	routing.Kind.Load(teamsFS, team+".alerts",
//		policy.Require("platform.paging", policy.From(policies.Platform)))
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
// are serialized. Each runs in an alertrouter.policy.load span and is counted
// on the [telemetry.Metrics] passed with [WithMetrics]. Each is logged too,
// except a failed [Store.InitialLoad], which its caller reports.
package store
