package build

import (
	"reflect"
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
)

// The ways a field is read.
const (
	readField  = iota // build.Field: the path crosses no optional
	readOpt           // build.Opt: the path crosses one, so it ends in a `?.` chain
	readOptPtr        // build.OptPtr: like Opt, of a pointer field
)

// root is a shadow value the builder resolves field addresses against:
// the input of a document, the variable of a quantifier or filter, or the
// argument of the function given to Sel. Its pointer-to-struct fields are
// allocated, so the address of any field below it can be taken.
type root struct {
	v    reflect.Value // the value, addressable
	name string        // the variable it stands for; empty for the input and for Sel
}

// segment is one field on a path from a root: its Sigil name, and whether
// a pointer comes before it, which makes the access `?.`.
type segment struct {
	name string
	opt  bool
}

// fieldNode is a field of the input or of a variable in scope, found by
// its address once the document renders.
type fieldNode struct {
	p    reflect.Value // the pointer the builder was given
	t    reflect.Type  // what it points to
	s    Site
	mode int
}

// selNode is a field of a struct value that has no address in a shadow,
// such as a map value: f's result, resolved against f's argument.
type selNode struct {
	x node
	r *root
	p reflect.Value
	t reflect.Type
	s Site
}

// binderNode is a quantifier or a filter.
type binderNode struct {
	rng, body node
	r         *root
	s         Site
	op        ast.Op // OpAny, OpAll, or OpInvalid for a filter
}

// Field is a field of the document's input, or a quantifier's or
// filter's variable, or one of its fields:
//
//	build.Field(&in.Release.Soak)      // release.soak
//	build.Field(e)                     // e, inside build.Any("e", …, func(e *E) …)
//	build.Field(&e.Name)               // e.name
//
// p must point into the input the document's body was given, or into a
// variable whose body is being built; the field must carry a `policy`
// tag. Which field it is gets worked out when the document renders, by
// address and type, so Field takes no document. A field reached through
// an optional struct is read with [Opt]; the address of the pointer field
// itself, `&in.Approval`, is an Expr of the pointer type, for [Present]
// and [Coalesce].
func Field[T any](p *T) Expr[T] {
	return Expr[T]{field(callSite("build.Field"), reflect.ValueOf(p), reflect.TypeFor[T](), readField)}
}

// Opt is a field reached through an optional struct, which renders with
// `?.` after the pointer, as `release.parent?.author.name`, and is
// optional itself. Unwrap it with [Coalesce], or test it with [Present].
// Opt on a path that crosses no pointer is an error; use [Field].
func Opt[T any](p *T) Expr[*T] {
	return Expr[*T]{field(callSite("build.Opt"), reflect.ValueOf(p), reflect.TypeFor[T](), readOpt)}
}

// OptPtr is [Opt] for a pointer field reached through an optional struct,
// such as `release.parent?.merged_by`. Optionals don't nest, so the
// result is optional once.
func OptPtr[T any](p **T) Expr[*T] {
	return Expr[*T]{field(callSite("build.OptPtr"), reflect.ValueOf(p), reflect.TypeFor[*T](), readOptPtr)}
}

// Sel is a field of x, a struct value that has no address to take, such
// as a map value or a list element: f gets a fresh S and returns the
// address of the field.
//
//	build.Sel(build.Index(commits, build.Lit(0)), func(c *Commit) *string { return &c.Author })
//
// renders `commits[0].author`. The path may not cross an optional struct.
func Sel[S, F any](x Expr[S], f func(*S) *F) Expr[F] {
	s := callSite("build.Sel")
	if f == nil {
		return Expr[F]{&errNode{s.errorf("the field function is nil")}}
	}
	arg := newShadow[S]()
	return Expr[F]{&selNode{x: x.n, r: &root{v: reflect.ValueOf(arg).Elem()}, p: reflect.ValueOf(f(arg)), t: reflect.TypeFor[F](), s: s}}
}

// Any is the quantifier `any name in xs: body`, true when body holds for
// some element. body gets a fresh E standing for the variable: build the
// variable with Field(e), a field of it with Field(&e.Name). The variable
// is only valid inside body.
func Any[E any](name string, xs Expr[[]E], body func(e *E) Expr[bool]) Expr[bool] {
	return Expr[bool]{binder(callSite("build.Any"), ast.OpAny, name, xs.n, body)}
}

// All is the quantifier `all name in xs: body`, true when body holds for
// every element. The variable works as in [Any].
func All[E any](name string, xs Expr[[]E], body func(e *E) Expr[bool]) Expr[bool] {
	return Expr[bool]{binder(callSite("build.All"), ast.OpAll, name, xs.n, body)}
}

// Filter is `filter name in xs: body`, the elements of xs body holds
// for, in their order. The variable works as in [Any].
func Filter[E any](name string, xs Expr[[]E], body func(e *E) Expr[bool]) Expr[[]E] {
	return Expr[[]E]{binder(callSite("build.Filter"), ast.OpInvalid, name, xs.n, body)}
}

// lower finds the field in the variables in scope, innermost first, then
// in the input.
func (n *fieldNode) lower(l *lowerer) ast.Expr {
	if !n.p.IsValid() || n.p.IsNil() {
		l.errorf(n.s, "the pointer is nil; take the address of a field, like &in.Release.Soak")
		return &ast.BadExpr{}
	}
	roots := make([]*root, 0, len(l.binders)+1)
	for _, r := range slices.Backward(l.binders) {
		roots = append(roots, r)
	}
	roots = append(roots, l.doc.shadow)
	for _, r := range roots {
		if path, ok := r.find(n.p.Pointer(), n.t); ok {
			return n.path(l, r, path)
		}
	}
	for _, r := range roots {
		if name, ok := untagged(r.v, n.p.Pointer(), n.t); ok {
			l.errorf(n.s, "field %s has no `policy` tag, so policies can't read it; tag it, or read a tagged field", name)
			return &ast.BadExpr{}
		}
	}
	l.errorf(n.s, "the %v isn't a field of the input or of a variable in scope; take the address of a tagged field of the body's argument, like &in.Release.Soak (a quantifier's variable is only valid inside its body)", n.p.Type())
	return &ast.BadExpr{}
}

// path builds the access to the field at path below r, checking that the
// way it's read fits whether the path crosses an optional struct.
func (n *fieldNode) path(l *lowerer, r *root, path []segment) ast.Expr {
	var x ast.Expr
	switch {
	case r.name != "":
		x = &ast.Ident{Name: r.name}
	case len(path) == 0:
		l.errorf(n.s, "the input as a whole isn't a value; take one of its fields, like &in.Release")
		return &ast.BadExpr{}
	default:
		x, path = &ast.Ident{Name: path[0].name}, path[1:]
	}
	crosses := false
	for _, seg := range path {
		crosses = crosses || seg.opt
		x = &ast.SelectorExpr{X: x, Sel: &ast.Ident{Name: seg.name}, Optional: seg.opt}
	}
	switch {
	case n.mode == readField && crosses:
		l.errorf(n.s, "%s reaches through an optional struct; read it with build.Opt, which renders `?.`", flat(x, top))
	case n.mode != readField && !crosses:
		l.errorf(n.s, "%s reaches through no optional struct; read it with build.Field", flat(x, top))
	}
	return x
}

func (n *selNode) lower(l *lowerer) ast.Expr {
	x := l.expr(n.x, n.s)
	if n.p.IsNil() {
		l.errorf(n.s, "the field function returned nil; return the address of a field of its argument")
		return &ast.BadExpr{}
	}
	path, ok := n.r.find(n.p.Pointer(), n.t)
	if !ok {
		l.errorf(n.s, "the field function must return the address of a tagged field of its argument, of type *%v", n.t)
		return &ast.BadExpr{}
	}
	for _, seg := range path {
		if seg.opt {
			l.errorf(n.s, "the field reaches through an optional struct, which build.Sel can't read; read the optional struct with build.Opt")
			return &ast.BadExpr{}
		}
		x = &ast.SelectorExpr{X: x, Sel: &ast.Ident{Name: seg.name}}
	}
	return x
}

// lower lowers the range, then the body with the variable in scope.
func (n *binderNode) lower(l *lowerer) ast.Expr {
	rng := l.expr(n.rng, n.s)
	l.binders = append(l.binders, n.r)
	body := l.expr(n.body, n.s)
	l.binders = l.binders[:len(l.binders)-1]
	v := &ast.Ident{Name: n.r.name}
	if n.op == ast.OpInvalid {
		return &ast.FilterExpr{Var: v, Range: rng, Body: body}
	}
	return &ast.QuantExpr{Op: n.op, Var: v, Range: rng, Body: body}
}

// find returns the path from r to the field at address p of type t. The
// type tells a struct from its first field, which shares its address.
func (r *root) find(p uintptr, t reflect.Type) ([]segment, bool) {
	if r.v.Addr().Pointer() == p && r.v.Type() == t {
		return nil, true
	}
	if r.v.Kind() == reflect.Pointer && !r.v.IsNil() {
		// A variable over a list of optional structs: its fields are read
		// with `?.`.
		return search(r.v.Elem(), p, t, nil, true)
	}
	return search(r.v, p, t, nil, false)
}

// field is the node of Field, Opt and OptPtr.
func field(s Site, p reflect.Value, t reflect.Type, mode int) node {
	return &fieldNode{p: p, t: t, s: s, mode: mode}
}

// binder builds a quantifier or filter: it calls body with a fresh
// shadow for the variable.
func binder[E any](s Site, op ast.Op, name string, xs node, body func(e *E) Expr[bool]) node {
	if msg := identError(name); msg != "" {
		return &errNode{s.errorf("variable %s", msg)}
	}
	if body == nil {
		return &errNode{s.errorf("the body function is nil")}
	}
	e := newShadow[E]()
	b := body(e)
	return &binderNode{op: op, rng: xs, body: b.n, r: &root{v: reflect.ValueOf(e).Elem(), name: name}, s: s}
}

// newShadow returns a zero T whose pointer-to-struct fields, at any
// depth, point to zero structs, so the address of every field below it
// can be taken; a T that is itself a pointer to a struct points to one.
// A pointer back to a struct type already on the path stays nil, so a
// recursive type doesn't allocate forever.
func newShadow[T any]() *T {
	p := new(T)
	v := reflect.ValueOf(p).Elem()
	if v.Kind() == reflect.Pointer && v.Type().Elem().Kind() == reflect.Struct {
		v.Set(reflect.New(v.Type().Elem()))
		v = v.Elem()
	}
	allocate(v, map[reflect.Type]bool{})
	return p
}

// allocate fills the pointer-to-struct fields of v, a struct, and of the
// structs below it. path holds the struct types on the way down.
func allocate(v reflect.Value, path map[reflect.Type]bool) {
	if v.Kind() != reflect.Struct || path[v.Type()] {
		return
	}
	path[v.Type()] = true
	defer delete(path, v.Type())
	for _, f := range v.Fields() {
		if !f.CanSet() {
			continue
		}
		switch {
		case f.Kind() == reflect.Struct:
			allocate(f, path)
		case f.Kind() == reflect.Pointer && f.Type().Elem().Kind() == reflect.Struct && !path[f.Type().Elem()]:
			f.Set(reflect.New(f.Type().Elem()))
			allocate(f.Elem(), path)
		}
	}
}

// search looks for the field at address p of type t among the tagged
// fields of v and the structs below them. path is the way to v; opt says
// whether v was reached through a pointer.
func search(v reflect.Value, p uintptr, t reflect.Type, path []segment, opt bool) ([]segment, bool) {
	if v.Kind() != reflect.Struct {
		return nil, false
	}
	for sf, f := range v.Fields() {
		name, ok := tagName(sf)
		if !ok {
			continue
		}
		here := slices.Concat(path, []segment{{name: name, opt: opt}})
		if f.Addr().Pointer() == p && f.Type() == t {
			return here, true
		}
		switch {
		case f.Kind() == reflect.Struct:
			if found, ok := search(f, p, t, here, false); ok {
				return found, true
			}
		case f.Kind() == reflect.Pointer && !f.IsNil():
			if found, ok := search(f.Elem(), p, t, here, true); ok {
				return found, true
			}
		}
	}
	return nil, false
}

// untagged looks for the field at address p of type t among the exported
// fields of v and below, tagged or not. When the way there passes a field
// without a `policy` tag, it returns that field's Go name, for the error
// that says so.
func untagged(v reflect.Value, p uintptr, t reflect.Type) (string, bool) {
	if v.Kind() != reflect.Struct {
		return "", false
	}
	for sf, f := range v.Fields() {
		if !sf.IsExported() {
			continue
		}
		_, tagged := tagName(sf)
		if f.Addr().Pointer() == p && f.Type() == t || reaches(f, p, t) {
			if !tagged {
				return v.Type().Name() + "." + sf.Name, true
			}
			if f.Kind() == reflect.Pointer {
				f = f.Elem()
			}
			return untagged(f, p, t)
		}
	}
	return "", false
}

// reaches reports whether the field at address p of type t is below v,
// tagged or not.
func reaches(v reflect.Value, p uintptr, t reflect.Type) bool {
	if v.Kind() == reflect.Pointer && !v.IsNil() {
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return false
	}
	for sf, f := range v.Fields() {
		if !sf.IsExported() {
			continue
		}
		if f.Addr().Pointer() == p && f.Type() == t || reaches(f, p, t) {
			return true
		}
	}
	return false
}

// tagName returns the name a field's `policy` tag gives it, the way
// policy.NewKind reads it.
func tagName(f reflect.StructField) (string, bool) {
	tag, ok := f.Tag.Lookup("policy")
	if !ok || tag == "-" || !f.IsExported() || f.Anonymous {
		return "", false
	}
	name, _, _ := strings.Cut(tag, ",")
	return name, name != ""
}
