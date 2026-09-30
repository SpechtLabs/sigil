package check

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Checker checks expressions and statements of one file and collects
// what it finds: types in its [Info], diagnostics in [Checker.Errors]. It
// keeps state between calls and isn't safe for concurrent use.
type Checker struct {
	// Resolver finds the documents `use` names. Without one every
	// import is an unknown document.
	Resolver  Resolver
	exported  *Exported
	info      *Info
	letStates map[string]*letState // every let of the document being checked; names are unique per document
	typing    *letState            // the let whose value is being typed, if any
	file      string
	errs      diag.ErrorList
	older     bool // the document pins an older, still accepted kind version
}

// New returns a checker for the named file. The name goes into every
// diagnostic; the checker never opens the file.
func New(file string) *Checker {
	return &Checker{file: file, info: &Info{
		Types:        map[ast.Expr]types.Type{},
		Params:       map[*ast.ParamStmt]types.Type{},
		Constructors: map[*ast.CallStmt]*kind.Decision{},
		Invocations:  map[*ast.CallStmt]string{},
		Reasons:      map[*ast.CallStmt]*ast.Ident{},
		Reads:        map[ast.Expr]Read{},
	}}
}

// Info returns what the checker has recorded so far, across every
// document it checked. It's never nil.
func (c *Checker) Info() *Info { return c.info }

// Exported returns what other documents may import from the document
// checked last, or nil before a document was checked.
func (c *Checker) Exported() *Exported { return c.exported }

// Errors returns every diagnostic so far, in source order, or nil when
// there are none. It sorts the checker's own list in place and returns it.
func (c *Checker) Errors() diag.ErrorList {
	if len(c.errs) == 0 {
		return nil
	}
	c.errs.Sort()
	return c.errs
}

// Expr checks x in env and returns its type, or [types.Invalid] after
// reporting what's wrong. It records the type of every node of x in
// [Info.Types]. An empty list or map literal has no type of its own and
// is an error here; where the context knows the type, use [Checker.ExprAs].
func (c *Checker) Expr(x ast.Expr, env *Env) types.Type {
	return c.exprWith(x, env, nil)
}

// exprWith is [Checker.Expr] with a hint for the parts of x that take
// their type from context, such as a bare enum value. Unlike
// [Checker.ExprAs], it doesn't require x to have the hinted type.
func (c *Checker) exprWith(x ast.Expr, env *Env, hint types.Type) types.Type {
	t := c.expr(x, env, hint)
	if untyped(t) {
		c.errorf(x, "add an element, or use the literal where a typed list or map is expected", "cannot infer the type of `%s`", ast.Sprint(x))
		return c.record(x, types.Invalid)
	}
	return t
}

// ExprAs checks x where a value of type want is expected. An empty list
// or map literal takes its type from want; anything else must be
// [types.Identical] to it, or ExprAs reports the mismatch. It returns the
// type of x, or [types.Invalid] after an error. When want is Invalid it
// checks x but reports no mismatch, since want's own error is already out.
func (c *Checker) ExprAs(x ast.Expr, env *Env, want types.Type) types.Type {
	t := c.expr(x, env, want)
	switch {
	case t == types.Invalid || want == types.Invalid:
		return types.Invalid
	case untyped(t):
		if resolved, ok := c.resolve(x, want); ok {
			return resolved
		}
		c.errorf(x, "", "expected %s, found %s", want, describe(t))
		return c.record(x, types.Invalid)
	case !types.Identical(t, want):
		c.mismatch(x, want, t)
		return types.Invalid
	}
	return t
}

// errorf records a diagnostic covering at.
func (c *Checker) errorf(at ast.Node, help, format string, args ...any) {
	c.errs = append(c.errs, &diag.Error{
		File: c.file, Msg: fmt.Sprintf(format, args...), Help: help, Pos: at.Pos(), End: at.End(),
	})
}

// mismatch reports a value of the wrong type, with a hint for the
// confusions the docs call out.
func (c *Checker) mismatch(x ast.Expr, want, got types.Type) {
	help := ""
	switch {
	case types.Identical(want, types.Duration) && types.Identical(got, types.Int):
		help = "a bare number is never a duration; write a literal like `30m`"
	case isNumber(want) && isNumber(got):
		help = "int and float don't convert implicitly"
	case isOptional(got):
		help = fmt.Sprintf("unwrap it with `??`, like `%s ?? <default>`", ast.Sprint(x))
	default:
		help = enumHint(x, want, got)
	}
	c.errorf(x, help, "expected %s, found %s", want, got)
}

// record stores t as the type of x and returns it.
func (c *Checker) record(x ast.Expr, t types.Type) types.Type {
	c.info.Types[x] = t
	return t
}

// untyped reports whether t is, or contains, an empty literal's placeholder:
// a list or map with a nil parameter.
func untyped(t types.Type) bool {
	switch t := t.(type) {
	case *types.List:
		return t.Elem == nil || untyped(t.Elem)
	case *types.Map:
		return t.Key == nil || t.Value == nil || untyped(t.Key) || untyped(t.Value)
	}
	return false
}

// resolve gives the untyped literal x the type want, recursing into
// nested empty literals, and reports whether want has the right shape.
func (c *Checker) resolve(x ast.Expr, want types.Type) (types.Type, bool) {
	if opt, ok := want.(*types.Optional); ok {
		want = opt.Elem
	}
	var ok bool
	switch x := x.(type) {
	case *ast.ParenExpr:
		_, ok = c.resolve(x.X, want)
	case *ast.ListLit:
		ok = c.resolveList(x, want)
	case *ast.MapLit:
		ok = c.resolveMap(x, want)
	}
	if !ok {
		return nil, false
	}
	return c.record(x, want), true
}

// resolveList resolves the untyped elements of x against want's element
// type.
func (c *Checker) resolveList(x *ast.ListLit, want types.Type) bool {
	l, ok := want.(*types.List)
	if !ok {
		return false
	}
	for _, e := range x.Elems {
		if !c.resolveIfUntyped(e, l.Elem) {
			return false
		}
	}
	return true
}

// resolveMap resolves the untyped values of x against want's value type.
func (c *Checker) resolveMap(x *ast.MapLit, want types.Type) bool {
	m, ok := want.(*types.Map)
	if !ok {
		return false
	}
	for _, e := range x.Entries {
		if !c.resolveIfUntyped(e.Value, m.Value) {
			return false
		}
	}
	return true
}

// resolveIfUntyped resolves x against want when x is still untyped, and
// reports whether x fits.
func (c *Checker) resolveIfUntyped(x ast.Expr, want types.Type) bool {
	if !untyped(c.info.Types[x]) {
		return true
	}
	_, ok := c.resolve(x, want)
	return ok
}

// unify makes two operand types agree when one is an untyped literal,
// returning the types to compare. It resolves the untyped side against
// the other when the shapes match, and leaves both alone otherwise.
func (c *Checker) unify(x ast.Expr, tx types.Type, y ast.Expr, ty types.Type) (types.Type, types.Type) {
	switch {
	case untyped(tx) && !untyped(ty):
		if t, ok := c.resolve(x, ty); ok {
			return t, ty
		}
	case untyped(ty) && !untyped(tx):
		if t, ok := c.resolve(y, tx); ok {
			return tx, t
		}
	}
	return tx, ty
}

// describe names a type for a message, with a readable form for the
// untyped placeholders.
func describe(t types.Type) string {
	switch t := t.(type) {
	case *types.List:
		if untyped(t) {
			return "an empty list"
		}
	case *types.Map:
		if untyped(t) {
			return "an empty map"
		}
	case nil:
		return "nothing"
	}
	return t.String()
}

func isNumber(t types.Type) bool { return t == types.Int || t == types.Float }

func isOptional(t types.Type) bool { _, ok := t.(*types.Optional); return ok }

func isEnum(t types.Type) bool { _, ok := t.(*types.Enum); return ok }

// elemOf returns the type inside an optional, or t itself.
func elemOf(t types.Type) types.Type { //nolint:returninterface // a type is any of five kinds
	if opt, ok := t.(*types.Optional); ok {
		return opt.Elem
	}
	return t
}
