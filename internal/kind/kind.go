// Package kind models the contract between a Go host and its policies:
// the inputs, host functions and decisions a kind declares, and how
// decisions are resolved.
//
// A Kind is built from one of two sources, a kind file parsed into an AST
// or a Go input struct walked by reflection, and both produce this one
// model. The validity rules from docs/reference/kind-files.md live here in
// Validate, so the two sources can't drift in what they accept.
package kind

import "github.com/spechtlabs/sigil/internal/types"

// Kind is one contract.
type Kind struct {
	Default    *Default // nil only for a collecting kind without one
	Name       string
	Types      []*types.Struct // struct types, in declaration order
	Inputs     []*Input
	Funcs      []*Func
	Decisions  []*Decision
	Precedence []string // decision names, highest first; empty for a collecting kind
	Version    int
	Collect    bool // every fired decision applies, instead of one winner
}

// Type returns the struct type called name, or nil.
func (k *Kind) Type(name string) *types.Struct {
	for _, t := range k.Types {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// Input returns the input called name, or nil.
func (k *Kind) Input(name string) *Input {
	for _, in := range k.Inputs {
		if in.Name == name {
			return in
		}
	}
	return nil
}

// Func returns the host function called name, or nil.
func (k *Kind) Func(name string) *Func {
	for _, f := range k.Funcs {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Decision returns the decision called name, or nil.
func (k *Kind) Decision(name string) *Decision {
	for _, d := range k.Decisions {
		if d.Name == name {
			return d
		}
	}
	return nil
}
