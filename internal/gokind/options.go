package gokind

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/kind"
)

// Options describes the kind to build, as package policy collects it from
// the type parameter and options of NewKind.
type Options struct {
	Input     reflect.Type // the input struct
	Default   *Default     // nil for none
	Conflict  *Default     // the outcome of a conflict under `collect one`; nil for none
	Accepts   *int         // the oldest version a document may pin; nil accepts every version
	Name      string       // the kind's name, an identifier
	Decisions []Decision   // precedence order, or declaration order when Collect is set
	Rankings  []Ranking    // reason rankings, one per decision at most
	// Precedence ranks the decisions of a Collect kind by name, which
	// returns every candidate at the top rank; without it a ranked kind's
	// precedence is the order of Decisions.
	Precedence []string
	Exclusive  [][]kind.Outcome // sets of outcomes that can't fire together
	Funcs      []Func
	Version    int // the contract's version, from 1
	// Ranked and Collect record which of WithDecisions and WithCollect
	// added the decisions; both is an error.
	Ranked  bool
	Collect bool
	// RecoverHostPanics turns a panic in a host function into a runtime
	// error instead of letting it unwind out of the evaluation.
	RecoverHostPanics bool
}

// Decision is one decision, its payload struct and its reasons. None is
// spelled as an empty struct.
type Decision struct {
	Payload reflect.Type // a struct type whose tagged fields are the payload
	Name    string
	Reasons []string
}

// Ranking ranks the reasons of one decision, highest first.
type Ranking struct {
	Decision string
	Reasons  []string
	// Mixed holds the reasons of other decisions the ranking was given.
	// A ranking orders one decision's reasons, so each is an error; the
	// kind model can't hold them, so Build reports them.
	Mixed []kind.Outcome
}

// Func is a host function: its name and the Go function. Fn must be a
// non-variadic func that returns one value, or a value and an error.
type Func struct {
	Fn   any
	Name string
}

// Default is a decision and reason with no rule behind it: the default
// decision, or the conflict outcome. Its payload comes from the payload
// fields' defaults, so every payload field of the decision needs one.
type Default struct {
	Decision string
	Reason   string
}
