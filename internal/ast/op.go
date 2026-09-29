package ast

import "strconv"

// Op is an operator. Prefix, infix and quantifier operators share one type
// because a message such as "a quantifier can't be an operand of `==`" needs
// to spell any of them.
type Op uint8

// The operators, in the order of the precedence table at
// https://sigil.specht-labs.de/reference/expressions/, lowest first.
// The quantifiers, which the table leaves out, come last. [Op.String] gives each one's spelling; the
// two-word forms such as OpNotIn are one operator. Operators without a
// comment are infix.
const (
	// OpInvalid is the zero Op and names no operator.
	OpInvalid Op = iota

	OpOr
	OpXor
	OpAnd
	OpNot // prefix

	OpEq
	OpNotEq
	OpLt
	OpLtEq
	OpGt
	OpGtEq
	OpIn
	OpNotIn
	OpAllIn
	OpAnyIn
	OpOneIn
	OpExclusiveIn
	OpHas
	OpLike
	OpMatches

	OpCoalesce

	OpAdd
	OpSub
	OpNeg     // prefix
	OpPresent // prefix

	OpAny // quantifier
	OpAll // quantifier
)

var opNames = [...]string{
	OpOr:          "or",
	OpXor:         "xor",
	OpAnd:         "and",
	OpNot:         "not",
	OpEq:          "==",
	OpNotEq:       "!=",
	OpLt:          "<",
	OpLtEq:        "<=",
	OpGt:          ">",
	OpGtEq:        ">=",
	OpIn:          "in",
	OpNotIn:       "not in",
	OpAllIn:       "all in",
	OpAnyIn:       "any in",
	OpOneIn:       "one in",
	OpExclusiveIn: "exclusive in",
	OpHas:         "has",
	OpLike:        "like",
	OpMatches:     "matches",
	OpCoalesce:    "??",
	OpAdd:         "+",
	OpSub:         "-",
	OpNeg:         "-",
	OpPresent:     "present",
	OpAny:         "any",
	OpAll:         "all",
}

// String returns the operator as written in source, or "op(N)" for a value
// outside the declared operators.
func (op Op) String() string {
	if int(op) < len(opNames) && opNames[op] != "" {
		return opNames[op]
	}
	return "op(" + strconv.Itoa(int(op)) + ")"
}

// IsComparison reports whether op is on the non-associative comparison
// level of the precedence table.
func (op Op) IsComparison() bool { return OpEq <= op && op <= OpMatches }
