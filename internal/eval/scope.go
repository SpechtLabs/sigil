package eval

import (
	"github.com/spechtlabs/sigil/internal/gokind"
)

// Scope maps the names a document declares to how the compiled code
// reads them: params are constants bound at compile time, lets are
// expressions evaluated at most once per frame, on first use, and
// quantifier variables get frame slots as the compiler meets them.
// Inputs and host functions come from the binding.
type Scope struct {
	binding *gokind.Binding
	consts  map[string]Value
	names   map[string]int // let name to index in lets
	slots   map[string]int
	lets    []Expr
	nslots  int
	nconds  int // `when` conditions, each with a memo slot in a frame
}

// NewScope returns a scope over the kind's binding.
func NewScope(b *gokind.Binding) *Scope {
	return &Scope{binding: b, consts: map[string]Value{}, names: map[string]int{}, slots: map[string]int{}}
}

// Bind makes name a constant, as a param bound at compile time is.
func (s *Scope) Bind(name string, v Value) { s.consts[name] = v }

// Let declares a let and returns its index. The expression is supplied
// with SetLet once compiled, so lets can be declared before any of them
// is compiled and refer to each other in any order.
func (s *Scope) Let(name string) int {
	if i, ok := s.names[name]; ok {
		return i
	}
	i := len(s.lets)
	s.names[name] = i
	s.lets = append(s.lets, nil)
	return i
}

// SetLet sets the expression of the let with index i.
func (s *Scope) SetLet(i int, e Expr) { s.lets[i] = e }

// Declare gives name a slot and returns it. The caller binds the value
// with Frame.Set before evaluating.
func (s *Scope) Declare(name string) int {
	if slot, ok := s.slots[name]; ok {
		return slot
	}
	slot := s.nslots
	s.slots[name] = slot
	s.nslots++
	return slot
}

// Cond reserves a memo slot for a `when` condition and returns it.
func (s *Scope) Cond() int {
	s.nconds++
	return s.nconds - 1
}

// Slots returns how many slots a frame for this scope needs.
func (s *Scope) Slots() int { return s.nslots }
