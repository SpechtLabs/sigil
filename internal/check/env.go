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
	Unknown      Entity = iota
	Input               // an input the kind declares
	Function            // a host function the kind declares
	DecisionName        // a decision the kind declares, as a value or a constructor
	Param               // a policy param
	Let                 // a let, the document's own or selectively imported
	QuantVar            // the variable of a quantifier such as `any` or `all`
	FilterVar           // the variable of a `filter`
	Module              // a whole import of a module: a qualifier for its pub lets
	Invocable           // a whole import of a policy: a name to invoke
	EnumValue           // a value of one of the kind's enums, or of several
	EnumType            // an enum the kind declares, as the qualifier of `Tier.critical`
)

var entityNames = [...]string{
	Unknown:      "unknown",
	Input:        "input",
	Function:     "host function",
	DecisionName: "decision",
	Param:        "param",
	Let:          "let",
	QuantVar:     "quantifier variable",
	FilterVar:    "filter variable",
	Module:       "module",
	Invocable:    "imported policy",
	EnumValue:    "enum value",
	EnumType:     "enum type",
}

// String implements [fmt.Stringer]. It returns the entity as a message
// names it, like "host function".
func (e Entity) String() string {
	if int(e) < len(entityNames) {
		return entityNames[e]
	}
	return "unknown"
}

// Binding is what a name resolves to. An imported name carries the
// document it came from: a whole import binds Doc alone, and a
// selectively imported let binds Doc and Let, the let's name there.
type Binding struct {
	Type   types.Type // nil for a host function, and for a value several enums declare
	Func   *kind.Func // set for a host function
	Doc    *Exported  // set for an import
	Let    string     // the imported let's own name, for a selective import
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
// inputs, host functions, decisions, enums and enum values. An enum's name
// is bound so that `Tier.critical` can qualify a value. An enum value's
// binding has the enum as its type when only one enum declares the value,
// and no type when several do; the checker then types it by context.
func NewEnv(k *kind.Kind) *Env {
	e := &Env{kind: k, names: map[string]Binding{}}
	if k == nil {
		return e
	}
	for _, en := range k.Enums {
		e.names[en.Name] = Binding{Entity: EnumType, Type: en}
		for _, v := range en.Values {
			if _, taken := e.names[v]; taken {
				e.names[v] = Binding{Entity: EnumValue}
				continue
			}
			e.names[v] = Binding{Entity: EnumValue, Type: en}
		}
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

// Lookup resolves name through the scope chain, innermost scope first,
// and reports whether any scope binds it.
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

// Names returns every name in scope, sorted, for suggestions. A name bound
// in more than one scope of the chain appears once per scope.
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

// Closest returns the name in scope most like name, if one is close
// enough to be a typo of it, by the rule [Nearest] applies.
func (e *Env) Closest(name string) (string, bool) {
	return nearest(name, e.Names())
}
