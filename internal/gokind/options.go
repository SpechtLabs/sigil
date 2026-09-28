package gokind

import (
	"reflect"

	"github.com/spechtlabs/sigil/internal/kind"
)

// Options describes the kind to build.
type Options struct {
	Input     reflect.Type // the input struct
	Default   *Default     // nil for none
	Name      string
	Decisions []Decision // precedence order, or declaration order when Collect is set
	Rankings  []Ranking  // reason rankings, one per decision at most
	// Precedence ranks the decisions of a Collect kind by name, which
	// returns every candidate at the top rank; without it a ranked kind's
	// precedence is the order of Decisions.
	Precedence []string
	Exclusive  [][]kind.Outcome
	Funcs      []Func
	Version    int
	Accepts    int // the oldest version a document may pin; 0 accepts every version
	// Ranked and Collect record which of WithDecisions and WithCollect
	// added the decisions; both is an error.
	Ranked  bool
	Collect bool
}

// Decision is one decision, its payload struct and its reasons. None is
// spelled as an empty struct.
type Decision struct {
	Payload reflect.Type
	Name    string
	Reasons []string
}

// Ranking ranks the reasons of one decision, highest first.
type Ranking struct {
	Decision string
	Reasons  []string
}

// Func is a host function: its name and the Go function.
type Func struct {
	Fn   any
	Name string
}

// Default is the default decision and its reason. Its payload comes from
// the payload fields' defaults.
type Default struct {
	Decision string
	Reason   string
}
