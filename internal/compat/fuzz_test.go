package compat_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/compat"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/types"
)

// names are what mutations name new declarations: fresh names, and names
// the corpus kind and its policies already use, so collisions and
// ambiguous enum values come up.
var names = []string{"batch", "standard", "premium", "region", "hotfix", "notify", "escalate", "extra", "tier", "free", "frozen", "security", "home", "Tier", "Plan", "Size"}

// FuzzCompare compares two arbitrary kind files. Whatever they declare,
// a kind compared with itself has no changes, every change has a mirror
// image when the arguments swap, a removal is never compatible, and two
// kinds differ exactly when their canonical forms do.
func FuzzCompare(f *testing.F) {
	f.Add(deploy, deploy)
	f.Add(deploy, edit(deploy, "free | premium", "free | standard | premium"))
	f.Add(deploy, edit(deploy, "deny > review > approve", "deny > approve > review", "exclusive deny.not_eligible, approve\n", ""))
	f.Add(access, edit(access, "collect all\n", "collect one\nprecedence admin > read\n"))
	f.Add(access, edit(access, "admin(reason: oncall)", "admin(reason: oncall, ttl: 8h)"))
	f.Fuzz(func(t *testing.T, a, b string) {
		old, errs := check.LoadKind("old.sigil", []byte(a))
		if errs != nil {
			return
		}
		next, errs := check.LoadKind("new.sigil", []byte(b))
		if errs != nil {
			return
		}
		properties(t, old, next)
	})
}

// FuzzCompareMutations mutates the kind in testdata/corpus, whose
// policies all compile against it, as shape says. Besides the properties
// FuzzCompare checks, it holds Compare to its promise: when every change
// is compatible, every policy of the corpus still compiles against the
// new kind, and the header is fine with `version` bumped and `accepts`
// left alone.
func FuzzCompareMutations(f *testing.F) {
	src, err := os.ReadFile(filepath.Join("testdata", "corpus", "gate.sigil"))
	if err != nil {
		f.Fatal(err)
	}
	base, errs := check.LoadKind("gate.sigil", src)
	if errs != nil {
		f.Fatal(errs)
	}
	policies := corpus(f)
	for op := range byte(mutations) {
		f.Add([]byte{op, 0, 1, 2, 3})
		f.Add([]byte{op, 1, 2, 3, 4, op + 1, 5, 6, 7})
	}
	f.Fuzz(func(t *testing.T, shape []byte) {
		next, _ := check.LoadKind("gate.sigil", src) // a fresh copy to mutate
		m := &mutator{k: next, shape: shape}
		for n := 0; n < 6 && m.pos < len(shape); n++ {
			m.mutate()
		}
		if next.Validate(nil) != nil {
			return
		}
		next.Version, next.Accepts = base.Version+1, base.Accepts
		r := properties(t, base, next)
		if r.Breaking() > 0 {
			if r.OK() {
				t.Fatalf("breaking changes without raising accepts passed:\n%s", next.Source())
			}
			return
		}
		if !r.OK() {
			t.Fatalf("compatible changes with version bumped failed: %+v\n%s", r.Problems, next.Source())
		}
		for _, p := range policies {
			c := check.New(p.file)
			c.Policy(p.doc, next)
			if errs := failures(c.Errors()); errs != nil {
				t.Fatalf("Compare found only compatible changes %v, but %s no longer compiles:\n%v\n%s", paths(r.Changes), p.file, errs, next.Source())
			}
		}
	})
}

// properties checks what holds for any two valid kinds, and returns the
// report on old and next.
func properties(t *testing.T, old, next *kind.Kind) *compat.Report {
	t.Helper()
	for _, k := range []*kind.Kind{old, next} {
		if self := compat.Compare(k, k); len(self.Changes) != 0 || len(self.Problems) != 0 {
			t.Fatalf("a kind compared with itself has changes %v and problems %+v:\n%s", paths(self.Changes), self.Problems, k.Source())
		}
	}
	r := compat.Compare(old, next)
	if again := compat.Compare(old, next); !reflect.DeepEqual(r, again) {
		t.Fatal("Compare isn't deterministic")
	}
	if same := canonical(old) == canonical(next); same != (len(r.Changes) == 0) {
		t.Fatalf("canonical forms equal: %v, but changes %v:\n%s\nthen:\n%s", same, paths(r.Changes), old.Source(), next.Source())
	}
	rev := compat.Compare(next, old)
	mirrored(t, r.Changes, rev.Changes)
	mirrored(t, rev.Changes, r.Changes)
	return r
}

// mirrored checks that every change in forward has its mirror image in
// back, the changes with the arguments swapped: an addition is a removal
// there, and the other way round, and a change or reorder is one there
// too, of the same class. The exception is a payload field's default,
// which breaks when it's dropped and not when it's added. An ambiguous
// value mirrors the removal of the value, or of its whole enum.
func mirrored(t *testing.T, forward, back []compat.Change) {
	t.Helper()
	find := func(path string, ops ...compat.Op) *compat.Change {
		for i, c := range back {
			if c.Path == path && slices.Contains(ops, c.Op) {
				return &back[i]
			}
		}
		return nil
	}
	for _, c := range forward {
		if c.Op == compat.Removed && c.Class == compat.Compatible {
			t.Fatalf("%s %s is compatible", c.Op, c.Path)
		}
		var m *compat.Change
		switch c.Op {
		case compat.Added:
			m = find(c.Path, compat.Removed)
		case compat.Removed:
			m = find(c.Path, compat.Added, compat.Ambiguous)
		case compat.Ambiguous:
			m = find(c.Path, compat.Removed)
			if m == nil {
				enum, _, _ := strings.Cut(c.Path, " value ")
				m = find(enum, compat.Removed)
			}
		default:
			m = find(c.Path, c.Op)
			if m != nil && m.Class != c.Class && strings.Contains(c.Old, " = ") == strings.Contains(c.New, " = ") {
				t.Fatalf("%s %s is %s one way and %s the other", c.Op, c.Path, c.Class, m.Class)
			}
		}
		if m == nil {
			t.Fatalf("%s %s has no mirror image among %v", c.Op, c.Path, paths(back))
		}
	}
}

// canonical renders k as a kind file without its version numbers, and
// with the default and conflict outcomes passing only the fields that
// differ from the field's default, so two kinds that mean the same print
// the same.
func canonical(k *kind.Kind) string {
	c := *k
	c.Version, c.Accepts = 1, 1
	c.Default, c.Conflict = trim(k, k.Default), trim(k, k.Conflict)
	return c.Source()
}

func trim(k *kind.Kind, o *kind.Default) *kind.Default {
	if o == nil {
		return nil
	}
	out := &kind.Default{Decision: o.Decision, Reason: o.Reason, Args: map[string]any{}}
	dec := k.Decision(o.Decision)
	for name, v := range o.Args {
		if f := dec.Field(name); f == nil || !f.HasDefault || constant.Format(f.Default) != constant.Format(v) {
			out.Args[name] = v
		}
	}
	return out
}

// policy is a parsed policy of the corpus.
type policy struct {
	doc  *ast.PolicyDoc
	file string
}

// corpus parses the policies in testdata/corpus, and fails unless each
// compiles against the corpus kind.
func corpus(f *testing.F) []policy {
	files, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.sigil"))
	if err != nil {
		f.Fatal(err)
	}
	var out []policy
	for _, file := range files {
		src, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		parsed, errs := parser.ParseFile(file, src)
		if errs != nil {
			f.Fatal(errs)
		}
		for _, doc := range parsed.Docs {
			if p, ok := doc.(*ast.PolicyDoc); ok {
				out = append(out, policy{doc: p, file: file})
			}
		}
	}
	if len(out) == 0 {
		f.Fatal("testdata/corpus holds no policy")
	}
	return out
}

// failures returns the errors among diagnostics, or nil.
func failures(errs diag.ErrorList) diag.ErrorList {
	var out diag.ErrorList
	for _, e := range errs {
		if e.Severity == diag.SeverityError {
			out = append(out, e)
		}
	}
	return out
}

func paths(cs []compat.Change) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = string(c.Op) + " " + c.Path
	}
	return out
}

// mutations is how many kinds of mutation [mutator.mutate] knows.
const mutations = 31

// mutator changes a kind as shape says, one byte at a time. Mutations
// that make the kind invalid are fine: the fuzz target skips those kinds.
type mutator struct {
	k     *kind.Kind
	shape []byte
	pos   int
}

// next returns the next byte of the shape, or 0 past its end.
func (m *mutator) next() int {
	if m.pos >= len(m.shape) {
		return 0
	}
	m.pos++
	return int(m.shape[m.pos-1])
}

// pick returns an index below n, which must be positive.
func (m *mutator) pick(n int) int { return m.next() % n }

func (m *mutator) name() string { return names[m.pick(len(names))] }

// typ picks a type for a new field, input or function result.
func (m *mutator) typ() types.Type {
	all := make([]types.Type, 0, 6+len(m.k.Enums)+len(m.k.Types))
	all = append(all, types.String, types.Int, types.Bool, types.Duration, &types.List{Elem: types.String}, &types.Optional{Elem: types.String})
	for _, e := range m.k.Enums {
		all = append(all, e)
	}
	for _, s := range m.k.Types {
		all = append(all, s)
	}
	return all[m.pick(len(all))]
}

// value returns a constant of t, and whether t has one a payload field
// may default to.
func (m *mutator) value(t types.Type) (any, bool) { //nolint:emptyinterface // constants are typed by their Sigil type
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.String:
			return names[m.pick(len(names))], true
		case types.Int:
			return int64(m.next()), true
		case types.Bool:
			return m.next()%2 == 0, true
		case types.Duration:
			return time.Duration(m.next()) * time.Minute, true
		}
	case *types.List:
		return []any{}, true
	case *types.Enum:
		if len(t.Values) > 0 {
			return constant.EnumValue(t.Values[m.pick(len(t.Values))]), true
		}
	}
	return nil, false
}

// mutate applies one mutation.
func (m *mutator) mutate() { //nolint:gocyclo // one case per mutation reads better than a table of closures
	k := m.k
	switch m.pick(mutations) {
	case 0: // add an enum value
		if len(k.Enums) > 0 {
			e := k.Enums[m.pick(len(k.Enums))]
			e.Values = append(e.Values, m.name())
		}
	case 1: // remove an enum value
		if len(k.Enums) > 0 {
			e := k.Enums[m.pick(len(k.Enums))]
			if len(e.Values) > 0 {
				e.Values = remove(e.Values, m.pick(len(e.Values)))
			}
		}
	case 2: // reorder an enum's values
		if len(k.Enums) > 0 {
			e := k.Enums[m.pick(len(k.Enums))]
			swap(e.Values, m.next(), m.next())
		}
	case 3: // add an enum
		k.Enums = append(k.Enums, &types.Enum{Name: []string{"Size", "Stage", "Support"}[m.pick(3)], Values: []string{m.name()}})
	case 4: // remove an enum
		if len(k.Enums) > 0 {
			k.Enums = remove(k.Enums, m.pick(len(k.Enums)))
		}
	case 5: // add a struct field
		if len(k.Types) > 0 {
			s := k.Types[m.pick(len(k.Types))]
			s.Fields = append(s.Fields, &types.Field{Name: m.name(), Type: m.typ()})
		}
	case 6: // remove a struct field
		if len(k.Types) > 0 {
			s := k.Types[m.pick(len(k.Types))]
			if len(s.Fields) > 0 {
				s.Fields = remove(s.Fields, m.pick(len(s.Fields)))
			}
		}
	case 7: // change a struct field's type
		if len(k.Types) > 0 {
			s := k.Types[m.pick(len(k.Types))]
			if len(s.Fields) > 0 {
				s.Fields[m.pick(len(s.Fields))].Type = m.typ()
			}
		}
	case 8: // add an input
		k.Inputs = append(k.Inputs, &kind.Input{Name: m.name(), Type: m.typ()})
	case 9: // remove an input
		if len(k.Inputs) > 0 {
			k.Inputs = remove(k.Inputs, m.pick(len(k.Inputs)))
		}
	case 10: // change an input's type
		if len(k.Inputs) > 0 {
			k.Inputs[m.pick(len(k.Inputs))].Type = m.typ()
		}
	case 11: // add a host function
		k.Funcs = append(k.Funcs, &kind.Func{Name: m.name(), Params: []types.Type{types.String}, Result: m.typ()})
	case 12: // remove a host function
		if len(k.Funcs) > 0 {
			k.Funcs = remove(k.Funcs, m.pick(len(k.Funcs)))
		}
	case 13: // change a host function's result
		if len(k.Funcs) > 0 {
			k.Funcs[m.pick(len(k.Funcs))].Result = m.typ()
		}
	case 14: // add a decision, ranked anywhere
		name := []string{"escalate", "hold", "defer"}[m.pick(3)]
		k.Decisions = append(k.Decisions, &kind.Decision{Name: name, Reasons: []string{"incident"}})
		if len(k.Precedence) > 0 {
			k.Precedence = slices.Insert(k.Precedence, m.pick(len(k.Precedence)+1), name)
		}
	case 15: // remove a decision, from the ranking and the exclusive sets too
		if len(k.Decisions) > 0 {
			i := m.pick(len(k.Decisions))
			name := k.Decisions[i].Name
			k.Decisions = remove(k.Decisions, i)
			k.Precedence = slices.DeleteFunc(k.Precedence, func(s string) bool { return s == name })
			k.Exclusive = slices.DeleteFunc(k.Exclusive, func(set []kind.Outcome) bool {
				return slices.ContainsFunc(set, func(o kind.Outcome) bool { return o.Decision == name })
			})
		}
	case 16: // add a reason, ranked anywhere
		if len(k.Decisions) > 0 {
			d := k.Decisions[m.pick(len(k.Decisions))]
			r := m.name()
			d.Reasons = append(d.Reasons, r)
			if len(d.Ranked) > 0 {
				d.Ranked = slices.Insert(d.Ranked, m.pick(len(d.Ranked)+1), r)
			}
		}
	case 17: // remove a reason, from the ranking too
		if len(k.Decisions) > 0 {
			d := k.Decisions[m.pick(len(k.Decisions))]
			r := m.reason(d)
			d.Reasons = slices.DeleteFunc(d.Reasons, func(s string) bool { return s == r })
			if d.Ranked != nil {
				d.Ranked = slices.DeleteFunc(d.Ranked, func(s string) bool { return s == r })
			}
		}
	case 18: // add a payload field, with a default or without
		if len(k.Decisions) > 0 {
			d := k.Decisions[m.pick(len(k.Decisions))]
			f := &kind.Field{Name: m.name(), Type: m.typ()}
			if v, ok := m.value(f.Type); ok && m.next()%2 == 0 {
				f.Default, f.HasDefault = v, true
			}
			d.Fields = append(d.Fields, f)
		}
	case 19: // remove a payload field
		if len(k.Decisions) > 0 {
			d := k.Decisions[m.pick(len(k.Decisions))]
			if len(d.Fields) > 0 {
				i := m.pick(len(d.Fields))
				for _, o := range []*kind.Default{k.Default, k.Conflict} {
					if o != nil && o.Decision == d.Name {
						delete(o.Args, d.Fields[i].Name)
					}
				}
				d.Fields = remove(d.Fields, i)
			}
		}
	case 20: // drop, add or change a payload field's default
		if f := m.payloadField(); f != nil {
			v, ok := m.value(f.Type)
			switch {
			case f.HasDefault && m.next()%2 == 0:
				f.Default, f.HasDefault = nil, false
			case ok:
				f.Default, f.HasDefault = v, true
			}
		}
	case 21: // change a payload field's type
		if f := m.payloadField(); f != nil {
			f.Type, f.Default, f.HasDefault = m.typ(), nil, false
		}
	case 22: // switch collect
		if k.Collect == kind.CollectOne {
			k.Collect, k.Conflict = kind.CollectAll, nil
			if m.next()%2 == 0 {
				k.Precedence = nil
			}
		} else {
			k.Collect = kind.CollectOne
		}
	case 23: // reorder the decisions' precedence
		swap(k.Precedence, m.next(), m.next())
	case 24: // add, drop or reorder a scoped precedence
		if len(k.Decisions) > 0 {
			d := k.Decisions[m.pick(len(k.Decisions))]
			switch {
			case len(d.Ranked) == 0:
				d.Ranked = slices.Clone(d.Reasons)
				swap(d.Ranked, m.next(), m.next())
			case m.next()%2 == 0:
				d.Ranked = nil
			default:
				swap(d.Ranked, m.next(), m.next())
			}
		}
	case 25: // add an exclusive set of two decisions, or remove one
		if len(k.Exclusive) > 0 && m.next()%2 == 0 {
			k.Exclusive = remove(k.Exclusive, m.pick(len(k.Exclusive)))
		} else if len(k.Decisions) > 1 {
			a, b := m.pick(len(k.Decisions)), m.pick(len(k.Decisions))
			k.Exclusive = append(k.Exclusive, []kind.Outcome{{Decision: k.Decisions[a].Name}, {Decision: k.Decisions[b].Name}})
		}
	case 26: // change the default
		if k.Default != nil && len(k.Decisions) > 0 {
			d := k.Decisions[m.pick(len(k.Decisions))]
			k.Default = &kind.Default{Decision: d.Name, Reason: m.reason(d), Args: map[string]any{}}
		}
	case 27: // add, remove or change the conflict outcome
		if k.Conflict != nil && m.next()%2 == 0 {
			k.Conflict = nil
		} else if len(k.Decisions) > 0 {
			d := k.Decisions[m.pick(len(k.Decisions))]
			k.Conflict = &kind.Default{Decision: d.Name, Reason: m.reason(d), Args: map[string]any{}}
		}
	case 28: // reorder the declarations of one section
		switch m.pick(5) {
		case 0:
			swap(k.Enums, m.next(), m.next())
		case 1:
			swap(k.Types, m.next(), m.next())
		case 2:
			swap(k.Inputs, m.next(), m.next())
		case 3:
			swap(k.Funcs, m.next(), m.next())
		default:
			swap(k.Decisions, m.next(), m.next())
		}
	case 29: // reorder a struct's fields, a decision's reasons or its payload fields, or an exclusive set
		switch m.pick(4) {
		case 0:
			if len(k.Types) > 0 {
				swap(k.Types[m.pick(len(k.Types))].Fields, m.next(), m.next())
			}
		case 1:
			if len(k.Decisions) > 0 {
				swap(k.Decisions[m.pick(len(k.Decisions))].Reasons, m.next(), m.next())
			}
		case 2:
			if len(k.Decisions) > 0 {
				swap(k.Decisions[m.pick(len(k.Decisions))].Fields, m.next(), m.next())
			}
		default:
			if len(k.Exclusive) > 0 {
				swap(k.Exclusive[m.pick(len(k.Exclusive))], m.next(), m.next())
			}
		}
	default: // rename the kind
		k.Name = "Renamed"
	}
}

// payloadField picks a payload field of any decision, or nil.
func (m *mutator) payloadField() *kind.Field {
	var all []*kind.Field
	for _, d := range m.k.Decisions {
		all = append(all, d.Fields...)
	}
	if len(all) == 0 {
		return nil
	}
	return all[m.pick(len(all))]
}

// remove returns s without its element i.
func remove[T any](s []T, i int) []T {
	return slices.Delete(slices.Clone(s), i, i+1)
}

// swap swaps two elements of s, picked by i and j, when s has two.
func swap[T any](s []T, i, j int) {
	if len(s) > 1 {
		i, j = i%len(s), j%len(s)
		s[i], s[j] = s[j], s[i]
	}
}

// reason picks one of d's reasons, or the empty name when it has none.
func (m *mutator) reason(d *kind.Decision) string {
	if len(d.Reasons) == 0 {
		return ""
	}
	return d.Reasons[m.pick(len(d.Reasons))]
}
