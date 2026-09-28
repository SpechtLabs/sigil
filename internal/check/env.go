// Package check is the type checker. It turns a kind document into a
// kind.Kind, resolves every name in a policy against the kind and the
// policy's own declarations, gives every expression a type by the rules
// in docs/reference/expressions.md and docs/reference/types.md, and
// reports what doesn't fit with a hint.
//
// The checker records the type of every expression node in an Info, which
// is what the evaluator compiles from: by the time evaluation starts,
// every operator already knows the types of its operands.
package check

import (
	"sort"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Entity says what kind of thing a name refers to, for messages such as
// "`split` is a host function".
type Entity uint8

// The entities a name can refer to.
const (
	Unknown Entity = iota
	Input
	Function
	DecisionName
	Param
	Let
	QuantVar
)

var entityNames = [...]string{
	Unknown:      "unknown",
	Input:        "input",
	Function:     "host function",
	DecisionName: "decision",
	Param:        "param",
	Let:          "let",
	QuantVar:     "quantifier variable",
}

func (e Entity) String() string {
	if int(e) < len(entityNames) {
		return entityNames[e]
	}
	return "unknown"
}

// Binding is what a name resolves to.
type Binding struct {
	Type   types.Type // nil for a host function; see Func
	Func   *kind.Func // set for a host function
	Entity Entity
}

// Env is a scope: the kind's names plus the names a document declares,
// and, inside a quantifier body, its variable. Every document has one
// flat namespace, so Declare refuses a name that's already bound
// anywhere in the chain.
type Env struct {
	kind   *kind.Kind
	parent *Env
	names  map[string]Binding
	// InAssert allows `outcome`, which only assert conditions may read.
	InAssert bool
}

// NewEnv returns the scope of a document of kind k, holding the kind's
// inputs, host functions and decisions.
func NewEnv(k *kind.Kind) *Env {
	e := &Env{kind: k, names: map[string]Binding{}}
	if k == nil {
		return e
	}
	for _, in := range k.Inputs {
		e.names[in.Name] = Binding{Entity: Input, Type: in.Type}
	}
	for _, f := range k.Funcs {
		e.names[f.Name] = Binding{Entity: Function, Func: f}
	}
	for _, d := range k.Decisions {
		e.names[d.Name] = Binding{Entity: DecisionName, Type: types.Decision}
	}
	return e
}

// Kind returns the kind the scope was built for.
func (e *Env) Kind() *kind.Kind { return e.kind }

// Lookup resolves name through the scope chain.
func (e *Env) Lookup(name string) (Binding, bool) {
	for s := e; s != nil; s = s.parent {
		if b, ok := s.names[name]; ok {
			return b, true
		}
	}
	return Binding{}, false
}

// Declare binds name in this scope. It returns the existing binding and
// false when the name is already taken anywhere in the chain, because
// nothing shadows anything.
func (e *Env) Declare(name string, b Binding) (Binding, bool) {
	if prev, taken := e.Lookup(name); taken {
		return prev, false
	}
	e.names[name] = b
	return b, true
}

// Bind binds name in this scope even when the chain already holds it.
// It's only for a document name that takes the name of something its kind
// added after the document's pin; everything else goes through Declare.
func (e *Env) Bind(name string, b Binding) {
	e.names[name] = b
}

// Child returns a nested scope, for a `when` body's lets or a quantifier
// body. It inherits InAssert.
func (e *Env) Child() *Env {
	return &Env{kind: e.kind, parent: e, names: map[string]Binding{}, InAssert: e.InAssert}
}

// Names returns every name in scope, sorted, for suggestions.
func (e *Env) Names() []string {
	var out []string
	for s := e; s != nil; s = s.parent {
		for n := range s.names {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// Closest returns the name in scope most like name, if one is close.
func (e *Env) Closest(name string) (string, bool) {
	return nearest(name, e.Names())
}
