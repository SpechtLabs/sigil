package lsp

import (
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// How well a completion fits where it goes, best first.
const (
	rankExpected = iota // of the type the context expects
	rankName            // nothing is expected, or a name that may fit, such as an optional of the type
	rankOther           // of another type than the context expects
	rankKeyword         // a keyword that doesn't fit the expected type
)

// The names of the composite types a param declares.
const (
	typeList = "list"
	typeMap  = "map"
)

// expected returns the type the operand at the cursor should have, or
// nil when nothing says:
//
//   - the condition of a `when`, an assert, a quantifier or a filter, the
//     operand of `not`, and either side of `and`, `or` and `xor`: bool;
//   - the right side of a comparison or of `+` and `-`: the left side's
//     type;
//   - after `in`: a list of the left side's type, and after `any in` and
//     the other list operators, the left side's list;
//   - after `has`: the key type of the map on the left;
//   - after `??`: what the optional on the left holds;
//   - an argument: the host function's parameter, the payload field, or
//     the invoked policy's param;
//   - an index: the map's key type, or int for a list;
//   - a param's default or bound: the param's type;
//   - an element of a list or map literal: what the literal's context
//     expects of its elements, keys or values.
func (v *view) expected(c *cursor, env *check.Env) types.Type { //nolint:returninterface // a type is any of five kinds
	if c.op == "" && c.outer != nil {
		return elementType(v.expected(c.outer, env), c.literal)
	}
	var left types.Type
	if c.left[0] >= 0 {
		left = v.typeOf(c.text(c.left[0], c.left[1]), env)
	}
	switch c.op {
	case opCond, opNot, opAnd, opOr, opXor:
		return types.Bool
	case "==", "!=", "<", "<=", ">", ">=", "+", "-":
		if left == types.Timestamp && c.op != "-" {
			return types.Duration
		}
		return known(left)
	case opIn, opNotIn:
		if left = known(left); left != nil {
			return &types.List{Elem: left}
		}
	case opAnyIn, opAllIn, opOneIn, opExclusiveIn:
		return known(left)
	case opHas:
		if m, ok := left.(*types.Map); ok {
			return m.Key
		}
	case "??":
		if opt, ok := left.(*types.Optional); ok {
			return opt.Elem
		}
	case opLike, opMatches:
		return types.String
	}
	if t := v.indexType(c, env); t != nil {
		return t
	}
	if t := v.argType(c, env); t != nil {
		return t
	}
	return v.paramType(c, env)
}

// known returns t, or nil for a type that wasn't worked out.
func known(t types.Type) types.Type { //nolint:returninterface // a type is any of five kinds
	if t == nil || t == types.Invalid {
		return nil
	}
	return t
}

// indexType returns the type of the index the cursor is in: the map's
// key type, or int for a list, or nil.
func (v *view) indexType(c *cursor, env *check.Env) types.Type { //nolint:returninterface // a type is any of five kinds
	if c.index[0] < 0 {
		return nil
	}
	switch t := v.typeOf(c.text(c.index[0], c.index[1]), env).(type) {
	case *types.Map:
		return t.Key
	case *types.List:
		return types.Int
	}
	return nil
}

// argType returns the type the argument the cursor is in takes: a host
// function's parameter at its position, a constructor's payload field, or
// an invoked policy's param, or nil.
func (v *view) argType(c *cursor, env *check.Env) types.Type { //nolint:returninterface // a type is any of five kinds
	s := c.site
	if s == nil {
		return nil
	}
	b, _ := env.Lookup(s.name)
	switch {
	case s.fn && b.Entity == check.Function && s.index < len(b.Func.Params):
		return b.Func.Params[s.index]
	case s.arg == "" || s.fn:
	case b.Entity == check.DecisionName:
		if f := env.Kind().Decision(s.name).Field(s.arg); f != nil {
			return f.Type
		}
	case b.Entity == check.Invocable:
		if p := b.Doc.Param(s.arg); p != nil {
			return known(p.Type)
		}
	}
	return nil
}

// paramType returns the type of the param whose default or bound the
// cursor is in, or nil.
func (v *view) paramType(c *cursor, env *check.Env) types.Type { //nolint:returninterface // a type is any of five kinds
	if c.paramType[0] < 0 {
		return nil
	}
	t, rest := typeOfTokens(c.toks[c.paramType[0]:c.paramType[1]+1], env.Kind())
	if len(rest) > 0 {
		return nil
	}
	return t
}

// typeOfTokens resolves the type at the start of toks, as a param
// declares it, in kind k: a scalar, the kind's struct type or enum, a
// list or map of types, or an optional one. It returns the tokens after
// the type, and a nil type for tokens that don't spell one.
func typeOfTokens(toks []token.Token, k *kind.Kind) (types.Type, []token.Token) { //nolint:returninterface // a type is any of five kinds
	if len(toks) == 0 {
		return nil, nil
	}
	switch t := toks[0]; {
	case t.Kind == token.Question:
		elem, rest := typeOfTokens(toks[1:], k)
		if elem == nil {
			return nil, nil
		}
		return &types.Optional{Elem: elem}, rest
	case t.Kind != token.Ident:
		return nil, nil
	case len(toks) > 1 && toks[1].Kind == token.Lt && (t.Text == typeList || t.Text == typeMap):
		first, rest := typeOfTokens(toks[2:], k)
		var second types.Type
		if t.Text == typeMap && first != nil && len(rest) > 0 && rest[0].Kind == token.Comma {
			second, rest = typeOfTokens(rest[1:], k)
		}
		switch {
		case first == nil || len(rest) == 0 || rest[0].Kind != token.Gt || t.Text == typeMap && second == nil:
			return nil, nil
		case t.Text == typeMap:
			return &types.Map{Key: first, Value: second}, rest[1:]
		}
		return &types.List{Elem: first}, rest[1:]
	}
	name := toks[0].Text
	if b, ok := types.Lookup(name); ok {
		return b, toks[1:]
	}
	if e := k.Enum(name); e != nil {
		return e, toks[1:]
	}
	if s := k.Type(name); s != nil {
		return s, toks[1:]
	}
	return nil, nil
}

// fit ranks a completion of type t where want is expected.
func fit(t, want types.Type) int {
	switch {
	case want == nil:
		return rankName
	case t == nil:
		return rankOther
	case types.Identical(t, want):
		return rankExpected
	}
	if opt, ok := t.(*types.Optional); ok && types.Identical(opt.Elem, want) {
		return rankName
	}
	return rankOther
}

// invokedParam returns the param of the invoked policy whose argument the
// cursor is in, or nil.
func (v *view) invokedParam(c *cursor, env *check.Env) *check.ExportedParam {
	s := c.site
	if s == nil || s.fn || s.arg == "" {
		return nil
	}
	if b, _ := env.Lookup(s.name); b.Entity == check.Invocable {
		return b.Doc.Param(s.arg)
	}
	return nil
}

// elementType returns the type of an element of the literals literal
// spells, outermost first, in a literal of type t: l for a list's
// element, k for a map's key and v for its value. It returns nil where t
// isn't the literal's type.
func elementType(t types.Type, literal string) types.Type { //nolint:returninterface // a type is any of five kinds
	for _, part := range []byte(literal) {
		switch tt := t.(type) {
		case *types.List:
			if part != 'l' {
				return nil
			}
			t = tt.Elem
		case *types.Map:
			switch part {
			case 'k':
				t = tt.Key
			case 'v':
				t = tt.Value
			default:
				return nil
			}
		default:
			return nil
		}
	}
	return t
}
