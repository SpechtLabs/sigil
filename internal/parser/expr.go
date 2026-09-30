package parser

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/lexer"
	"github.com/spechtlabs/sigil/internal/token"
)

// Binding powers, one per level of the precedence table in
// docs/reference/expressions.md, lowest first. parseExpr(minBP) parses an
// expression and stops before any infix operator whose power is below
// minBP, which is how a Pratt parser encodes precedence: the right operand
// of `and` is parsed with minBP just above `and`'s power, so it takes every
// comparison but stops at the next `and` or `or`.
const (
	lowest     = 1 // or, xor
	bpAnd      = 2 // and
	bpNot      = 3 // not, quantifiers, filters (prefix)
	bpCmp      = 4 // comparisons, membership, has, like, matches
	bpCoalesce = 5 // ??
	bpAdd      = 6 // + -
	bpNeg      = 7 // unary minus, present
)

// assoc is how an operator groups with a neighbor of the same power.
type assoc uint8

const (
	left  assoc = iota // a - b - c is (a - b) - c
	right              // a ?? b ?? c is a ?? (b ?? c)
	none               // a < b < c is an error
)

// infixOp describes an infix operator the parser found at the current
// token: what it is, how tightly it binds, how it groups and how many
// tokens spell it (`not in` is two).
type infixOp struct {
	bp    int
	ntok  int
	op    ast.Op
	assoc assoc
}

// infix classifies the current token as an infix operator, or reports
// false when it isn't one, which is how an expression ends: at `)`, `,`,
// `{`, a statement keyword or anything else that can't continue it. In a
// decision field's default, a keyword followed by `:` ends it too: that's
// the next field, named like a keyword.
func (p *parser) infix() (infixOp, bool) {
	cmp := func(op ast.Op) (infixOp, bool) { return infixOp{bpCmp, 1, op, none}, true }
	if p.inField && p.tok.Kind.IsKeyword() && p.peek().Kind == token.Colon {
		return infixOp{}, false
	}
	switch p.tok.Kind {
	case token.Pipe:
		p.errorTok(p.tok, "`|` can't join two conditions", "use `or`; `|` only separates enum values and reasons")
	case token.KwOr:
		return infixOp{lowest, 1, ast.OpOr, left}, true
	case token.KwXor:
		return infixOp{lowest, 1, ast.OpXor, none}, true
	case token.KwAnd:
		return infixOp{bpAnd, 1, ast.OpAnd, left}, true
	case token.Eq:
		return cmp(ast.OpEq)
	case token.NotEq:
		return cmp(ast.OpNotEq)
	case token.Lt:
		return cmp(ast.OpLt)
	case token.LtEq:
		return cmp(ast.OpLtEq)
	case token.Gt:
		return cmp(ast.OpGt)
	case token.GtEq:
		return cmp(ast.OpGtEq)
	case token.KwIn:
		return cmp(ast.OpIn)
	case token.KwHas:
		return cmp(ast.OpHas)
	case token.KwLike:
		return cmp(ast.OpLike)
	case token.KwMatches:
		return cmp(ast.OpMatches)
	case token.KwNot:
		return p.compound(ast.OpNotIn)
	case token.KwAll:
		return p.compound(ast.OpAllIn)
	case token.KwAny:
		return p.compound(ast.OpAnyIn)
	case token.KwOne:
		return p.compound(ast.OpOneIn)
	case token.KwExclusive:
		return p.compound(ast.OpExclusiveIn)
	case token.Coalesce:
		return infixOp{bpCoalesce, 1, ast.OpCoalesce, right}, true
	case token.Plus:
		return infixOp{bpAdd, 1, ast.OpAdd, left}, true
	case token.Minus:
		return infixOp{bpAdd, 1, ast.OpSub, left}, true
	}
	return infixOp{}, false
}

// compound handles `not`, `all`, `any`, `one` and `exclusive` at operator
// position, where each must be followed by `in`.
func (p *parser) compound(op ast.Op) (infixOp, bool) {
	if p.peek().Kind != token.KwIn {
		p.errorTok(p.tok, fmt.Sprintf("expected `in` after `%s`", p.tok.Text),
			fmt.Sprintf("after an operand, `%s` is only valid as part of `%s`", p.tok.Text, op))
	}
	return infixOp{bpCmp, 2, op, none}, true
}

// parseExpr parses an expression whose infix operators all bind at least
// as tightly as minBP.
func (p *parser) parseExpr(minBP int) ast.Expr {
	x := p.parsePrefix(minBP)
	for {
		op, ok := p.infix()
		if !ok || op.bp < minBP {
			return x
		}
		opPos := p.tok.Pos
		for range op.ntok {
			p.next()
		}

		// A left-associative operator's right operand must not contain the
		// same operator, so it's parsed one power up; a right-associative
		// one's may, so it's parsed at the same power. Non-associative
		// operators parse like left ones and then refuse a neighbor.
		rbp := op.bp + 1
		if op.assoc == right {
			rbp = op.bp
		}
		p.after, p.afterPos = op.op, opPos
		y := p.parseExpr(rbp)
		x = &ast.BinaryExpr{X: x, Y: y, OpPos: opPos, Op: op.op}
		p.checkNeighbor(op.op)
	}
}

// checkNeighbor rejects an operator that may not follow the one just
// parsed at the same level: a second comparison, a second `xor`, or `or`
// and `xor` mixed.
func (p *parser) checkNeighbor(prev ast.Op) {
	next, ok := p.infix()
	if !ok {
		return
	}
	switch {
	case prev.IsComparison() && next.op.IsComparison():
		p.errorTok(p.tok, fmt.Sprintf("`%s` can't follow `%s`: comparisons don't chain", next.op, prev),
			"add parentheses to say which comparison happens first, or join two comparisons with `and`")
	case prev == ast.OpXor && next.op == ast.OpXor:
		p.errorTok(p.tok, "`xor` can't be chained",
			"add parentheses to say which pair is compared first, or use `one in` for exactly one of several")
	case prev == ast.OpXor && next.op == ast.OpOr, prev == ast.OpOr && next.op == ast.OpXor:
		p.errorTok(p.tok, "`or` and `xor` can't be mixed without parentheses",
			"add parentheses to say which grouping you mean")
	}
}

// parsePrefix parses the prefix forms (`not`, unary minus, `present`,
// quantifiers, filters) or an operand. minBP says how tightly the surrounding operator binds: a
// prefix form at a looser level than that can't appear here without
// parentheses, because its operand would have to extend past the operator
// that's waiting for its own.
func (p *parser) parsePrefix(minBP int) ast.Expr {
	switch p.tok.Kind {
	case token.KwNot:
		p.requireLevel(minBP, bpNot, "`not`")
		pos := p.tok.Pos
		p.next()
		p.after, p.afterPos = ast.OpNot, pos
		return &ast.UnaryExpr{Op: ast.OpNot, OpPos: pos, X: p.parseExpr(bpNot)}
	case token.Minus:
		pos := p.tok.Pos
		p.next()
		p.after, p.afterPos = ast.OpNeg, pos
		return &ast.UnaryExpr{Op: ast.OpNeg, OpPos: pos, X: p.parseExpr(bpNeg)}
	case token.KwPresent:
		pos := p.tok.Pos
		p.next()
		p.after, p.afterPos = ast.OpPresent, pos
		return &ast.UnaryExpr{Op: ast.OpPresent, OpPos: pos, X: p.parseExpr(bpNeg)}
	case token.KwAny, token.KwAll:
		p.requireLevel(minBP, bpNot, "a quantifier")
		return p.parseQuantifier()
	case token.KwFilter:
		p.requireLevel(minBP, bpNot, "a filter")
		return p.parseFilter()
	case token.KwOne, token.KwExclusive:
		p.errorTok(p.tok, fmt.Sprintf("`%s` is an operator, not a quantifier", p.tok.Text),
			fmt.Sprintf("write `a %s in b`; only `any` and `all` start a quantifier", p.tok.Text))
	}
	return p.parsePostfix(p.parsePrimary())
}

// requireLevel fails when a prefix form of the given power appears where
// the surrounding operator demands something tighter.
func (p *parser) requireLevel(minBP, level int, what string) {
	if minBP <= level {
		return
	}
	msg := what + " can't appear here"
	if p.after != ast.OpInvalid {
		msg = fmt.Sprintf("%s can't be an operand of `%s`", what, p.after)
	}
	p.errorTok(p.tok, msg, "wrap it in parentheses")
}

// parseQuantifier parses `any x in xs: body` or `all x in xs: body` from
// the keyword.
func (p *parser) parseQuantifier() ast.Expr {
	kw := p.tok
	q := &ast.QuantExpr{QuantPos: kw.Pos, Op: ast.OpAny}
	if kw.Kind == token.KwAll {
		q.Op = ast.OpAll
	}
	q.Var, q.Range, q.Body = p.parseBinder(fmt.Sprintf("a quantifier is written `%s x in xs: condition`", kw.Text))
	return q
}

// parseFilter parses `filter x in xs: body` from the keyword.
func (p *parser) parseFilter() ast.Expr {
	f := &ast.FilterExpr{FilterPos: p.tok.Pos}
	f.Var, f.Range, f.Body = p.parseBinder("a filter is written `filter x in xs: condition`")
	return f
}

// parseBinder parses the `x in xs: body` that follows a quantifier's or
// a filter's keyword, which is the current token. shape is the hint for
// a malformed one.
func (p *parser) parseBinder(shape string) (v *ast.Ident, rng, body ast.Expr) {
	kw := p.tok
	p.next()

	if p.tok.Kind != token.Ident {
		p.unexpected(fmt.Sprintf("a variable name after `%s`", kw.Text), shape)
	}
	v = &ast.Ident{Name: p.tok.Text, Span: span(p.tok)}
	p.next()

	if p.tok.Kind != token.KwIn {
		p.unexpected(fmt.Sprintf("`in` after the variable `%s`", v.Name), shape)
	}
	p.next()

	p.after = ast.OpIn
	rng = p.parseExpr(bpCoalesce)
	p.expect(token.Colon, shape)
	p.after = ast.OpInvalid
	body = p.parseExpr(lowest)
	return v, rng, body
}

// parsePostfix applies any run of `.name`, `?.name`, `[index]` and `(args)` to x.
// They bind tighter than every operator, so they're consumed before the
// infix loop looks at the next token.
func (p *parser) parsePostfix(x ast.Expr) ast.Expr {
	for {
		switch p.tok.Kind {
		case token.Dot:
			p.next()
			x = &ast.SelectorExpr{X: x, Sel: p.parseName("a field name after `.`")}
		case token.OptDot:
			p.next()
			x = &ast.SelectorExpr{X: x, Sel: p.parseName("a field name after `?.`"), Optional: true}
		case token.LBracket:
			open := p.tok
			p.next()
			p.after = ast.OpInvalid
			index := p.parseExpr(lowest)
			closing := p.expectClosing(token.RBracket, open)
			x = &ast.IndexExpr{X: x, Index: index, Rbrack: closing.End}
		case token.LParen:
			open := p.tok
			p.next()
			args := p.parseList(token.RParen, open)
			x = &ast.CallExpr{Fun: x, Args: args, Rparen: p.tok.End}
			p.next()
		default:
			return x
		}
	}
}

// parseName parses a field or payload name, which may be spelled like a
// keyword: `resource.kind` and `service.type` are valid.
func (p *parser) parseName(want string) *ast.Ident {
	if p.tok.Kind != token.Ident && !p.tok.Kind.IsKeyword() {
		p.unexpected(want, "")
	}
	name := &ast.Ident{Name: p.tok.Text, Span: span(p.tok)}
	p.next()
	return name
}

// parseList parses comma-separated expressions up to, but not including,
// the closing delimiter, allowing a trailing comma. It leaves the parser on
// the closing token, which it has checked is there.
func (p *parser) parseList(closing token.Kind, open token.Token) []ast.Expr {
	var xs []ast.Expr
	for p.tok.Kind != closing {
		p.after = ast.OpInvalid
		xs = append(xs, p.parseExpr(lowest))
		if p.tok.Kind != token.Comma {
			break
		}
		p.next()
	}
	if p.tok.Kind != closing {
		p.expectClosing(closing, open)
	}
	return xs
}

// parsePrimary parses an operand: a name, a literal, or a parenthesized,
// list or map expression.
func (p *parser) parsePrimary() ast.Expr {
	t := p.tok
	switch t.Kind {
	case token.Ident:
		p.next()
		return &ast.Ident{Name: t.Text, Span: span(t)}
	case token.Int:
		p.next()
		v, _ := lexer.ParseInt(t.Text)
		return &ast.IntLit{Text: t.Text, Value: v, Span: span(t)}
	case token.Float:
		p.next()
		v, _ := lexer.ParseFloat(t.Text)
		return &ast.FloatLit{Text: t.Text, Value: v, Span: span(t)}
	case token.Duration:
		p.next()
		v, _ := lexer.ParseDuration(t.Text)
		return &ast.DurationLit{Text: t.Text, Value: v, Span: span(t)}
	case token.String:
		p.next()
		v, _ := lexer.Unquote(t.Text)
		return &ast.StringLit{Text: t.Text, Value: v, Span: span(t)}
	case token.RawString:
		p.next()
		return &ast.StringLit{Text: t.Text, Value: t.Text[1 : len(t.Text)-1], Raw: true, Span: span(t)}
	case token.KwTrue, token.KwFalse:
		p.next()
		return &ast.BoolLit{Value: t.Kind == token.KwTrue, Span: span(t)}
	case token.KwOutcome:
		p.next()
		return &ast.Outcome{Span: span(t)}
	case token.LParen:
		p.next()
		p.after = ast.OpInvalid
		x := p.parseExpr(lowest)
		closing := p.expectClosing(token.RParen, t)
		return &ast.ParenExpr{X: x, From: t.Pos, To: closing.End}
	case token.LBracket:
		p.next()
		elems := p.parseList(token.RBracket, t)
		lit := &ast.ListLit{Elems: elems, From: t.Pos, To: p.tok.End}
		p.next()
		return lit
	case token.LBrace:
		return p.parseMap()
	case token.Separator:
		p.errorTok(t, "`---` separates documents and can't appear inside an expression",
			"if you meant arithmetic, put spaces between the minus signs")
	}
	p.unexpected("an expression", "")
	return nil
}

// parseMap parses `{key: value, ...}` from the opening brace.
func (p *parser) parseMap() ast.Expr {
	open := p.tok
	op, opPos := p.after, p.afterPos
	p.next()
	var entries []ast.MapEntry
	for p.tok.Kind != token.RBrace {
		if len(entries) == 0 && (startsBodyStmt(p.tok.Kind) || p.atConstructor()) {
			p.missingOperand(op, opPos, open)
		}
		p.after = ast.OpInvalid
		key := p.parseExpr(bpCoalesce)
		if _, call := key.(*ast.CallExpr); call && len(entries) == 0 && p.tok.Kind != token.Colon {
			p.missingOperand(op, opPos, open)
		}
		p.expect(token.Colon, "a map entry is written `key: value`")
		p.after = ast.OpInvalid
		value := p.parseExpr(lowest)
		entries = append(entries, ast.MapEntry{Key: key, Value: value})
		if p.tok.Kind != token.Comma {
			break
		}
		p.next()
	}
	closing := p.expectClosing(token.RBrace, open)
	return &ast.MapLit{Entries: entries, From: open.Pos, To: closing.End}
}

// missingOperand fails at the operator op, whose right operand is a map
// literal that looks like a rule's body instead: it starts with a
// statement keyword, or with a constructor that isn't a key, as in
// `when release.soak < { deny(x) }`. At the start of an expression, with
// no operator, it does nothing and the map's own error stands. The brace
// is kept, so a `when` can parse the rest of its body.
func (p *parser) missingOperand(op ast.Op, pos token.Pos, brace token.Token) {
	if op == ast.OpInvalid {
		return
	}
	p.lostBody = &brace
	end := token.Pos{Offset: pos.Offset + len(op.String()), Line: pos.Line, Column: pos.Column + len(op.String())}
	p.errorAt(pos, end, fmt.Sprintf("`%s` has no right operand", op),
		"the `{` after it was read as a map literal; finish the condition before the rule's `{`")
}

// atConstructor reports whether the current token starts a call whose
// first argument is named, `deny(reason: a)`, which is a statement: an
// expression's call takes no named arguments.
func (p *parser) atConstructor() bool {
	if p.tok.Kind != token.Ident || p.peekAt(1).Kind != token.LParen {
		return false
	}
	name := p.peekAt(2).Kind
	return (name == token.Ident || name.IsKeyword()) && p.peekAt(3).Kind == token.Colon
}

// startsBodyStmt reports whether k starts a statement of a `when` body
// other than a call.
func startsBodyStmt(k token.Kind) bool {
	switch k {
	case token.KwWhen, token.KwLet, token.KwPub, token.KwAssert:
		return true
	}
	return false
}
