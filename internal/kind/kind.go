// Package kind models the contract between a Go host and its policies:
// the struct types, inputs, host functions and decisions a kind declares,
// how the candidates that fire resolve into what the host gets back, and
// which versions of the contract a document may pin.
//
// A Kind is built from one of two sources, a kind file parsed into an AST
// or a Go input struct walked by reflection, and both produce this one
// model: the checker loads a kind file, and package gokind reflects over
// the Go types a host passes to policy.NewKind. The validity rules at
// https://sigil.specht-labs.de/reference/kind-files/ live here in [Kind.Validate], so
// the two sources can't drift in what they accept. [Kind.Source] prints a kind
// back as a kind file in canonical form, which is what a host exports and
// what a stale export is compared by.
//
// A kind's [Collect] mode says how candidates resolve: `collect one`
// returns the one its [Kind.Precedence] ranks highest, and `collect all`
// returns every candidate that fired, or with a precedence every candidate
// at the top rank. A decision may rank its own reasons in
// [Decision.Ranked], [Kind.Exclusive] names outcomes that can't fire
// together, and [Kind.Default] applies when no rule fires.
package kind

import "github.com/spechtlabs/sigil/internal/types"

// How many candidates a kind returns, from its `collect` declaration.
const (
	CollectUnset Collect = iota // not declared, which Validate rejects
	CollectOne                  // one winner, ranked by precedence
	CollectAll                  // every candidate that fired, or those at the top rank with a precedence
)

// Collect is how many candidates a kind returns.
type Collect int

// Kind is one contract. The slices hold declarations in the order the
// kind file or the Go options give them, which is the order
// [Kind.Source] prints. A Kind that [Kind.Validate] hasn't accepted may
// break any of the rules it checks.
type Kind struct {
	Default    *Default // nil only for a `collect all` kind without one
	Name       string
	Types      []*types.Struct // struct types, in declaration order
	Inputs     []*Input
	Funcs      []*Func
	Decisions  []*Decision
	Precedence []string    // decision names, highest first; empty without a ranking
	Exclusive  [][]Outcome // sets of outcomes that can't fire together
	Version    int         // the contract's version, from 1
	Accepts    int         // the oldest version a document may pin, from 1; 1 accepts every version
	Collect    Collect
}

// Oldest returns the oldest kind version a policy or module may pin,
// which is [Kind.Accepts].
func (k *Kind) Oldest() int {
	return k.Accepts
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
