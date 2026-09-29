package eval

import (
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/gokind"
)

// Scope maps the names a document declares to how the compiled code
// reads them: params are constants bound at compile time, lets are
// expressions compiled on first reference and evaluated at most once
// per frame, and quantifier and filter variables get frame slots as the
// compiler meets them. Inputs and host functions come from the binding.
//
// Compiling mutates the scope, so it isn't safe for concurrent use until
// every expression over it is compiled.
type Scope struct {
	binding *gokind.Binding
	consts  map[string]Value
	names   map[string]int          // let name to index in lets
	decls   map[string]*ast.LetStmt // let name to its declaration, for compiling on demand
	slots   map[string]int
	compile func(*ast.LetStmt) Expr // compiles a let's value; set by the policy compiler
	inst    *instance               // the instance the scope belongs to, nil for a bare scope
	lets    []Expr
	nslots  int
	nconds  int // `when` conditions, each with a memo slot in a frame
}

// NewScope returns an empty scope over the kind's binding. A nil b gives a
// scope that reads no input and calls no host function, which is enough
// for constant expressions.
func NewScope(b *gokind.Binding) *Scope {
	return &Scope{binding: b, consts: map[string]Value{}, names: map[string]int{}, decls: map[string]*ast.LetStmt{}, slots: map[string]int{}}
}

// Bind makes name a constant, as a param bound at compile time is.
func (s *Scope) Bind(name string, v Value) { s.consts[name] = v }

// Let declares a let and returns its index, the same index when name is
// declared already. The expression is supplied with [Scope.SetLet] once
// compiled, so lets can be declared before any of them is compiled and
// refer to each other in any order. A policy's scope compiles its lets
// from their declarations on first reference instead.
func (s *Scope) Let(name string) int {
	if i, ok := s.names[name]; ok {
		return i
	}
	i := len(s.lets)
	s.names[name] = i
	s.lets = append(s.lets, nil)
	return i
}

// SetLet sets the expression of the let with index i, as [Scope.Let]
// returned it. It must be set before an expression reading the let is
// evaluated.
func (s *Scope) SetLet(i int, e Expr) { s.lets[i] = e }

// Declare gives name a slot and returns it, the same slot when name has
// one already. The caller binds the value with [Frame.Set] before
// evaluating.
func (s *Scope) Declare(name string) int {
	if slot, ok := s.slots[name]; ok {
		return slot
	}
	slot := s.nslots
	s.slots[name] = slot
	s.nslots++
	return slot
}

// bindVar gives a quantifier's or filter's variable a slot of its own
// while its body compiles, and returns the slot and a func that restores
// what name meant before. Two variables with the same name get different
// slots: a let read inside an `any a in xs` body may be compiled right
// there, and a quantifier of its own over another `a` must not overwrite
// the outer `a` when the let is evaluated.
func (s *Scope) bindVar(name string) (int, func()) {
	prev, had := s.slots[name]
	slot := s.nslots
	s.slots[name] = slot
	s.nslots++
	return slot, func() {
		if had {
			s.slots[name] = prev
		} else {
			delete(s.slots, name)
		}
	}
}

// Cond reserves a memo slot for a `when` condition and returns it. A
// frame evaluates the condition once and keeps its result there.
func (s *Scope) Cond() int {
	s.nconds++
	return s.nconds - 1
}

// Slots returns how many slots a frame for this scope needs.
func (s *Scope) Slots() int { return s.nslots }

// ensure compiles the let with index i if it hasn't been, from its
// declaration. Lets are compiled on first reference so that a document
// imported for its pub lets never compiles a private let nobody reads.
func (s *Scope) ensure(i int) {
	if s.lets[i] != nil || s.compile == nil {
		return
	}
	for name, idx := range s.names {
		if idx == i {
			if decl, ok := s.decls[name]; ok {
				s.lets[i] = s.compile(decl)
			}
			return
		}
	}
}

// value returns the let with index i in frame f, evaluating it on the
// first read.
func (s *Scope) value(f *Frame, i int) Value {
	if !f.done[i] {
		f.lets[i] = s.lets[i](f)
		f.done[i] = true
	}
	return f.lets[i]
}
