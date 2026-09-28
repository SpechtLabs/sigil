package gokind

import (
	"reflect"
)

// Options describes the kind to build.
type Options struct {
	Input     reflect.Type // the input struct
	Default   *Default     // nil for none
	Name      string
	Decisions []Decision // precedence order, or declaration order when Collect is set
	Funcs     []Func
	Version   int
	Accepts   int // the oldest version a document may pin; 0 accepts every version
	// Ranked and Collect record which of WithDecisions and WithCollect
	// added the decisions; both is an error.
	Ranked  bool
	Collect bool
}

// Decision is one decision and its payload struct. None is spelled as an
// empty struct.
type Decision struct {
	Payload reflect.Type
	Name    string
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
