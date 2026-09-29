// Package gokind builds a kind from Go types by reflection and ties the
// kind back to Go values: it is the machinery behind policy.NewKind, and
// it decodes JSON or YAML input for the CLI and policy tests.
//
// # Building a kind
//
// [Build] turns [Options] into a [kind.Kind]: the input struct becomes the
// inputs and struct types, payload structs become decision fields, and Go
// functions become host function signatures. It produces the same model
// a kind file loads into, so both pass [kind.Kind.Validate]. Every problem
// is reported, not only the first.
//
// A struct field is part of the contract when it carries a
// `policy:"name"` tag; the name is what policies write. Only a decision
// payload field takes an option, `default=<constant>`, parsed as a Sigil
// constant of the field's type. Go types map as
// https://sigil.specht-labs.de/reference/kind-files/#go-type-mapping describes: bool, int and int64, float64, string, [time.Duration] and
// [time.Time] are scalars, slices are lists, maps are maps, a pointer is
// an optional, and a named struct is a struct type called by its Go name.
// A pointer to a pointer, a slice or a map is rejected.
//
// # Bindings
//
// Reflection over types happens once, at build time. The [Binding] that Build
// returns records, for every field, the index path the evaluator reads it
// by, and the Go value of every host function. For a kind with no Go
// types behind it, such as one loaded from a kind file, [Synthesize]
// builds the Go types with [reflect.StructOf] and binds every host
// function to one that returns [*ErrUnbound].
//
// # Decoding
//
// [Binding.DecodeInput] and [Binding.Decode] read a decoded JSON or YAML
// document into the bound Go types, strictly: a key the kind doesn't
// declare is an error. [Binding.Canonical] goes the other way, turning a
// Go value into the dynamically typed form constants use, so values that
// came from different Go types compare equal when their Sigil values do.
package gokind
