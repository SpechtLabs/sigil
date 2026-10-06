package lsp

import (
	"slices"

	"github.com/spechtlabs/sigil/internal/token"
)

// site is the call whose arguments the cursor is in: a host function's,
// whose arguments are positional, or a constructor's or an invocation's,
// whose arguments are named. Signature help shows it, and completion takes
// the type an argument expects from it.
type site struct {
	name  string   // the called name
	arg   string   // the named argument whose value the cursor is in; "" between arguments, or for a host function
	given []string // the argument names given before the cursor
	index int      // which argument the cursor is in, from 0
	fn    bool     // a host function's call
}

// The operators the operand at the cursor can follow, as cursor.op holds
// them. Each infix one has an operand before it, in cursor.left.
const (
	opCond    = "cond"    // the start of a `when` or assert condition, or a quantifier's or filter's body
	opNot     = "not"     // the prefix `not`
	opPresent = "present" // the prefix `present`
	opNeg     = "neg"     // the prefix `-`
)

// The infix operators spelled as words, as cursor.op holds them and
// completion offers them.
const (
	opAnd         = "and"
	opOr          = "or"
	opXor         = "xor"
	opIn          = "in"
	opNotIn       = "not in"
	opAnyIn       = "any in"
	opAllIn       = "all in"
	opOneIn       = "one in"
	opExclusiveIn = "exclusive in"
	opHas         = "has"
	opLike        = "like"
	opMatches     = "matches"
)

// infix are the infix operators by the token that ends them, and for
// those spelled with two words, the word before `in`.
var infix = map[token.Kind]string{
	token.Eq: "==", token.NotEq: "!=", token.Lt: "<", token.LtEq: "<=", token.Gt: ">", token.GtEq: ">=",
	token.Plus: "+", token.Minus: "-", token.Coalesce: "??",
	token.KwAnd: opAnd, token.KwOr: opOr, token.KwXor: opXor,
	token.KwHas: opHas, token.KwLike: opLike, token.KwMatches: opMatches, token.KwIn: opIn,
}

// twoWord are the words that make `in` a two-word operator.
var twoWord = map[token.Kind]string{
	token.KwNot: opNotIn, token.KwAll: opAllIn, token.KwAny: opAnyIn, token.KwOne: opOneIn, token.KwExclusive: opExclusiveIn,
}

// siteOf returns the call whose arguments the cursor is in, the innermost
// one, or nil outside every call. A bracket inside the call's arguments,
// such as a list literal, doesn't hide it.
func (c *cursor) siteOf(frames []frame) *site {
	for _, f := range slices.Backward(frames) {
		switch {
		case level(f.kind):
			return nil
		case f.kind == frameCall:
			return c.arguments(f.open, f.name, false)
		case f.kind == frameGroup && c.toks[f.open].Kind == token.LParen && f.open > 0 && c.toks[f.open-1].Kind == token.Ident:
			if f.open < 2 || c.toks[f.open-2].Kind != token.Dot && c.toks[f.open-2].Kind != token.OptDot {
				return c.arguments(f.open, f.name, true)
			}
		}
	}
	return nil
}

// arguments reads the arguments of the call whose `(` is at index open,
// up to the cursor.
func (c *cursor) arguments(open int, name string, fn bool) *site {
	s := &site{name: name, fn: fn}
	depth := 0
	for i := open + 1; i < len(c.toks); i++ {
		switch c.toks[i].Kind {
		case token.LParen, token.LBracket, token.LBrace:
			depth++
		case token.RParen, token.RBracket, token.RBrace:
			depth--
		case token.Colon:
			if depth == 0 && !fn && i > open+1 {
				s.arg = c.toks[i-1].Text
				s.given = append(s.given, s.arg)
			}
		case token.Comma:
			if depth == 0 {
				s.index++
				s.arg = ""
			}
		}
	}
	return s
}

// operator records what the operand at the cursor follows, among the
// tokens from index from: an infix operator, with the operand before it,
// a prefix one, or the start of a condition. The operand's expected type
// follows from it.
func (c *cursor) operator(from int) {
	i := c.lastIndex()
	if i < from {
		return
	}
	t := c.toks[i]
	op, start := infix[t.Kind], i
	switch {
	case t.Kind == token.KwIn && i > 0 && twoWord[c.toks[i-1].Kind] != "":
		op, start = twoWord[c.toks[i-1].Kind], i-1
	case t.Kind == token.KwIn && i > 1 && c.toks[i-1].Kind == token.Ident && binderKeyword(c.toks[i-2].Kind):
		return // the range of a quantifier or a filter
	case t.Kind == token.Minus && (i == from || !endsOperand(c.toks[i-1])):
		op = opNeg
	case t.Kind == token.KwNot:
		op = opNot
	case t.Kind == token.KwPresent:
		op = opPresent
	case t.Kind == token.KwWhen:
		op = opCond
	case t.Kind == token.Colon && slices.ContainsFunc(c.binders, func(b binder) bool { return b.colon == i }):
		op = opCond
	}
	c.op = op
	if op == "" || op == opCond || op == opNot || op == opPresent || op == opNeg || start <= from {
		return
	}
	if first := c.operandStart(start - 1); first >= from {
		c.left = [2]int{first, start - 1}
	}
}

// binderKeyword reports whether k starts a quantifier or a filter.
func binderKeyword(k token.Kind) bool {
	return k == token.KwAny || k == token.KwAll || k == token.KwFilter
}
