package check

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// Info holds what the checker learned: the type of every expression, the
// lets in dependency order, and the decision each constructor builds.
type Info struct {
	Types        map[ast.Expr]types.Type
	Constructors map[*ast.CallStmt]*kind.Decision
	Lets         []*ast.LetStmt // in an order where every let follows the lets it reads
}

// TypeOf returns the recorded type of x, or nil when x wasn't checked.
func (i *Info) TypeOf(x ast.Expr) types.Type {
	if i == nil {
		return nil
	}
	return i.Types[x]
}

// Checker checks expressions and statements of one file and collects
// what it finds.
type Checker struct {
	info      *Info
	letStates map[string]*letState // while lets are being typed
	file      string
	errs      diag.ErrorList
}

// New returns a checker for the named file.
func New(file string) *Checker {
	return &Checker{file: file, info: &Info{
		Types:        map[ast.Expr]types.Type{},
		Constructors: map[*ast.CallStmt]*kind.Decision{},
	}}
}

// Info returns the types recorded so far.
func (c *Checker) Info() *Info { return c.info }

// Errors returns every diagnostic so far, in source order.
func (c *Checker) Errors() diag.ErrorList {
	if len(c.errs) == 0 {
		return nil
	}
	c.errs.Sort()
	return c.errs
}

// errorf records a diagnostic covering at.
func (c *Checker) errorf(at ast.Node, help, format string, args ...any) {
	c.errs = append(c.errs, &diag.Error{
		File: c.file, Msg: fmt.Sprintf(format, args...), Help: help, Pos: at.Pos(), End: at.End(),
	})
}

// Expr checks x in env and returns its type, or types.Invalid after
// reporting what's wrong. An empty list or map literal has no type of its
// own; where the context knows the type, use ExprAs.
func (c *Checker) Expr(x ast.Expr, env *Env) types.Type {
	t := c.expr(x, env, nil)
	if untyped(t) {
		c.errorf(x, "add an element, or use the literal where a typed list or map is expected", "cannot infer the type of `%s`", ast.Sprint(x))
		return c.record(x, types.Invalid)
	}
	return t
}

// ExprAs checks x where a value of type want is expected. An empty list
// or map literal takes its type from want; anything else must match it.
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

func isNumber(t types.Type) bool   { return t == types.Int || t == types.Float }
func isOptional(t types.Type) bool { _, ok := t.(*types.Optional); return ok }

// elemOf returns the type inside an optional, or t itself.
func elemOf(t types.Type) types.Type { //nolint:returninterface // a type is any of five kinds
	if opt, ok := t.(*types.Optional); ok {
		return opt.Elem
	}
	return t
}
