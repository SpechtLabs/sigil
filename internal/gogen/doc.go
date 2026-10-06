// Package gogen generates Go code from a kind: the source `sigil gen go`
// writes, so a Go service that doesn't import the defining host builds
// the kind from typed Go declarations and reads results with a type
// switch. It is the static twin of [gokind.Synthesize], which builds Go
// types for a kind file at run time with reflection, and it maps types
// the same way: `int` is int64, `float` float64, an optional a pointer,
// and a struct type or enum keeps the kind's name.
//
// # What it generates
//
// [Generate] writes one file that declares, in a kind file's order:
//
//   - every enum as a named string type with a constant per value,
//     `TierCritical` for `critical` of enum Tier,
//   - every struct type under its own name, a field per field, tagged
//     with the Sigil name,
//   - the input struct, Input, which is policy.NewKind's type parameter,
//   - a payload struct of its own per decision, ApproveData for
//     decision approve, with field defaults in the tags, so a type
//     switch on policy.Result.Value has one case per decision,
//   - a policy.Decision variable per decision, Approve, and a reason
//     handle per reason, ApproveReleaseManager, for a switch on
//     policy.Result.Why,
//   - Funcs, a struct of one func field per host function, when the kind
//     declares any, and
//   - NewKind, which builds the kind with policy.NewKind.
//
// The kind NewKind builds exports a kind file byte-identical to the kind's
// [kind.Kind.Source], so it loads policies from a bundle that holds the
// kind file it was generated from.
//
// # Names
//
// Struct types and enums keep the kind's names, since a binding names a
// type after its Go type; one whose name Go doesn't export also gets an
// exported alias. Everything else is named by [GoName], and when two
// declarations map to one identifier, the one declared first in the
// order [Generate] documents keeps it and the later one gets a number
// from 2.
//
// # What it rejects
//
// Some valid kind files have no Go equivalent, because policy.NewKind
// fixes what a kind file leaves open, such as the order of the struct
// types. [Generate] reports each such declaration with a diagnostic that
// says how to rewrite the kind file. A kind file a host exported
// already declares everything in the order policy.NewKind does.
package gogen
