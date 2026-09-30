package parser

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/token"
)

// expectIdent consumes an identifier and returns it, or fails with
// "expected want, found ...".
func (p *parser) expectIdent(want, help string) *ast.Ident {
	if p.tok.Kind != token.Ident {
		p.unexpected(want, help)
	}
	id := &ast.Ident{Name: p.tok.Text, Span: span(p.tok)}
	p.next()
	return id
}

// parsePolicyName parses a dotted name such as `deploy.common`. The parts
// must touch the dots: `deploy . common` is an error. It stops before a
// `.{` so the caller can parse a selective import.
func (p *parser) parsePolicyName(want string) *ast.PolicyName {
	name := &ast.PolicyName{Parts: []*ast.Ident{p.expectIdent(want, "")}}
	for p.tok.Kind == token.Dot {
		dot := p.tok
		last := name.Parts[len(name.Parts)-1]
		if dot.Pos.Offset != last.End().Offset {
			p.errorTok(dot, "a name can't have spaces around `.`", "remove the space before the `.`")
		}
		next := p.peek()
		switch {
		case next.Kind == token.LBrace:
			return name
		case next.Kind != token.Ident:
			p.next()
			p.unexpected("a name after `.`", "")
		case next.Pos.Offset != dot.End.Offset:
			p.errorTok(dot, "a name can't have spaces around `.`", "remove the space after the `.`")
		}
		p.next()
		name.Parts = append(name.Parts, p.expectIdent("a name after `.`", ""))
	}
	return name
}

// parseUse parses `use a.b`, `use a.b as c` or `use a.b.{x, y as z}`.
func (p *parser) parseUse() *ast.UseStmt {
	kw := p.tok
	p.next()
	u := &ast.UseStmt{Path: p.parsePolicyName("a policy or module name after `use`")}
	end := u.Path.End()

	switch p.tok.Kind {
	case token.KwAs:
		p.next()
		u.Alias = p.expectIdent("a name after `as`", "")
		end = u.Alias.End()
	case token.Dot:
		p.next()
		open := p.expect(token.LBrace, "")
		u.Items = p.parseImportItems(open)
		end = p.tok.End
		p.next()
	}
	u.Span = ast.Span{From: kw.Pos, To: end}
	return u
}

// parseImportItems parses the names in `.{...}`, leaving the parser on the
// closing brace.
func (p *parser) parseImportItems(open token.Token) []*ast.ImportItem {
	const help = "write `use deploy.common.{cleared}` to import a name, or `use deploy.common` for the whole module"
	var items []*ast.ImportItem
	for p.tok.Kind != token.RBrace {
		item := &ast.ImportItem{Name: p.expectIdent("a name to import", help)}
		if p.tok.Kind == token.KwAs {
			p.next()
			item.Alias = p.expectIdent("a name after `as`", "")
		}
		items = append(items, item)
		if p.tok.Kind != token.Comma {
			break
		}
		p.next()
	}
	if p.tok.Kind != token.RBrace {
		p.expectClosing(token.RBrace, open)
	}
	if len(items) == 0 {
		p.errorAt(open.Pos, p.tok.End, "a selective import needs at least one name", help)
	}
	return items
}

// parsePolicyStmt parses one top-level statement of a policy: param, let,
// when, assert or a call. `use` is handled by the document parser because
// of its ordering rule.
func (p *parser) parsePolicyStmt() ast.Stmt {
	switch p.tok.Kind {
	case token.KwParam:
		return p.parseParam()
	case token.KwLet, token.KwPub:
		return p.parseLet()
	case token.KwWhen:
		return p.parseWhen()
	case token.KwAssert:
		return p.parseAssert()
	case token.KwEnum:
		p.errorTok(p.tok, "an enum can only be declared in a kind",
			"declare it in the kind file, like `enum Tier: critical | standard`; policies and modules use its values by name")
	case token.Ident:
		if p.peek().Kind == token.LParen {
			return p.parseCall()
		}
		p.errorTok(p.tok, fmt.Sprintf("expected a statement, found `%s`", p.tok.Text),
			fmt.Sprintf("a bare name isn't a statement; an invocation needs parentheses, like `%s()`", p.tok.Text))
	}
	p.unexpected("`param`, `let`, `when`, `assert` or an invocation", "")
	return nil
}

// parseParam parses `param name: type` with an optional `= default`.
func (p *parser) parseParam() *ast.ParamStmt {
	const shape = "a param is written `param name: type` or `param name: type = default`"
	const bounds = "a param's bounds are written `, min: value` and `, max: value`, like `param min_soak: duration = 24h, min: 1h`"
	kw := p.tok
	p.next()
	s := &ast.ParamStmt{Name: p.expectIdent("a name after `param`", shape)}
	p.expect(token.Colon, shape)
	s.Type = p.parseType()
	end := s.Type.End()
	if p.tok.Kind == token.Assign {
		p.next()
		p.after = ast.OpInvalid
		s.Default = p.parseExpr(lowest)
		end = s.Default.End()
	}
	for p.tok.Kind == token.Comma {
		p.next()
		name := p.expectIdent("`min` or `max` after `,`", bounds)
		var slot *ast.Expr
		switch name.Name {
		case "min":
			slot = &s.Min
		case "max":
			slot = &s.Max
		default:
			p.errorAt(name.Pos(), name.End(), fmt.Sprintf("a param has no option `%s`", name.Name), bounds)
		}
		p.expect(token.Colon, bounds)
		p.after = ast.OpInvalid
		v := p.parseExpr(lowest)
		if *slot != nil {
			p.report(name.Pos(), name.End(), fmt.Sprintf("`%s` is given twice", name.Name), "give each bound once")
		}
		*slot = v
		end = v.End()
	}
	s.Span = ast.Span{From: kw.Pos, To: end}
	return s
}

// parseLet parses `let name = value`, with `pub` in front for a let other
// documents may import.
func (p *parser) parseLet() *ast.LetStmt {
	const shape = "a let is written `let name = expression`"
	start := p.tok
	pub := p.tok.Kind == token.KwPub
	if pub {
		p.next()
		if p.tok.Kind != token.KwLet {
			p.unexpected("`let` after `pub`", "only a let can be exported: `pub let name = expression`")
		}
	}
	p.next()
	s := &ast.LetStmt{Name: p.expectIdent("a name after `let`", shape), Pub: pub}
	p.expect(token.Assign, shape)
	p.after = ast.OpInvalid
	s.Value = p.parseExpr(lowest)
	s.Span = ast.Span{From: start.Pos, To: s.Value.End()}
	return s
}

// parseWhen parses `when cond { body }`. The condition ends at the `{`,
// which can't continue an expression.
func (p *parser) parseWhen() *ast.WhenStmt {
	defer p.unnest()
	p.nest("this `when`", p.skipWhen)
	kw := p.tok
	p.next()
	s := &ast.WhenStmt{}
	start := p.tok.Pos
	p.lostBody = nil
	ok := p.try(func() {
		p.after = ast.OpInvalid
		s.Cond = p.parseExpr(lowest)
		if p.tok.Kind != token.LBrace {
			p.unexpected("`{` after the condition", "a rule is written `when condition { ... }`")
		}
	})
	if open := p.lostBody; !ok && open != nil {
		// The condition swallowed the body's `{`; the body resumes here.
		p.lostBody = nil
		s.Cond = &ast.BadExpr{From: start, To: open.Pos}
		s.Body = p.parseBody(*open)
		s.Span = ast.Span{From: kw.Pos, To: p.tok.End}
		p.next()
		return s
	}
	if !ok {
		// The condition is broken, but if its `{` is still ahead the body
		// can be parsed as usual, so its rules and their errors aren't lost
		// with it. Otherwise give up on the whole statement.
		p.skipToBrace()
		if p.tok.Kind != token.LBrace {
			p.bail()
		}
		s.Cond = &ast.BadExpr{From: start, To: p.tok.Pos}
	}
	open := p.tok
	p.next()
	s.Body = p.parseBody(open)
	s.Span = ast.Span{From: kw.Pos, To: p.tok.End}
	p.next()
	return s
}

// skipToBrace skips ahead to a `{`, stopping early at anything that shows
// there isn't one for this statement: a statement keyword, a closing
// brace, or the end of the document.
func (p *parser) skipToBrace() {
	for {
		switch p.tok.Kind {
		case token.LBrace, token.RBrace, token.EOF, token.Separator,
			token.KwUse, token.KwParam, token.KwLet, token.KwPub, token.KwWhen, token.KwAssert:
			return
		}
		if p.atHeader(p.tok) {
			return
		}
		p.next()
	}
}

// parseBody parses the statements of a `when` body up to the closing
// brace, which it leaves as the current token. Each statement is parsed
// on its own, so an error in one doesn't lose the others.
func (p *parser) parseBody(open token.Token) []ast.Stmt {
	var body []ast.Stmt
	for {
		switch p.tok.Kind {
		case token.RBrace:
			return body
		case token.EOF, token.Separator, token.KwPolicy, token.KwModule, token.KwKind:
			p.expectClosing(token.RBrace, open)
		}
		p.recover(func() {
			if s := p.parseBodyStmt(); s != nil {
				body = append(body, s)
			}
		}, p.syncBody)
	}
}

// parseBodyStmt parses one statement inside a `when` body. A top-level
// statement found here is parsed and dropped, with an error saying where
// it belongs, so the rest of the body still parses.
func (p *parser) parseBodyStmt() ast.Stmt {
	const want = "a decision constructor, invocation, `when`, `let` or `assert`"
	switch p.tok.Kind {
	case token.KwWhen:
		return p.parseWhen()
	case token.KwLet:
		return p.parseLet()
	case token.KwPub:
		// A scoped let is kept, so its uses in the body still resolve.
		kw := p.tok
		p.report(kw.Pos, kw.End, "a let inside a `when` body can't be `pub`",
			"only top-level lets can be exported; remove `pub`")
		return p.parseLet()
	case token.KwAssert:
		return p.parseAssert()
	case token.Ident:
		if p.peek().Kind == token.LParen {
			return p.parseCall()
		}
	case token.KwParam:
		kw := p.tok
		p.report(kw.Pos, kw.End, fmt.Sprintf("expected %s, found `param`", want),
			"`param` is only allowed at the top level; move it outside the `when` block")
		p.parsePolicyStmt()
		return nil
	case token.KwUse:
		kw := p.tok
		p.report(kw.Pos, kw.End, fmt.Sprintf("expected %s, found `use`", want),
			"`use` is only allowed right after the header; move it up")
		p.parseUse()
		return nil
	}
	p.unexpected(want, "")
	return nil
}

// parseAssert parses `assert("reason", cond)`. The reason comes first, as
// in a decision constructor, and `)` ends the condition, including a
// quantifier body that would otherwise run on.
func (p *parser) parseAssert() *ast.AssertStmt {
	const shape = "an assert is written `assert(\"reason\", condition)`"
	kw := p.tok
	p.next()
	open := p.expect(token.LParen, shape)
	s := &ast.AssertStmt{}
	switch p.tok.Kind {
	case token.String:
		s.Reason = p.parsePrimary().(*ast.StringLit)
	case token.RawString:
		p.errorTok(p.tok, "the reason must be a double-quoted string", "raw strings are for patterns; write the reason as \"...\"")
	default:
		p.unexpected("a string literal reason", "the reason comes first: `assert(\"reason\", condition)`")
	}
	p.expect(token.Comma, shape)
	p.after = ast.OpInvalid
	s.Cond = p.parseExpr(lowest)
	if p.tok.Kind == token.Comma {
		p.next()
		if p.tok.Kind != token.RParen {
			p.unexpected("`)`", "an assert takes a reason and a condition, nothing else")
		}
	}
	closing := p.expectClosing(token.RParen, open)
	s.Span = ast.Span{From: kw.Pos, To: closing.End}
	return s
}

// parseCall parses `name(args)`: a decision constructor or a policy
// invocation. Every argument is `name: value`, except that the first may
// be positional, the old form of a constructor's reason, kept so that
// `sigil fmt` can rewrite it. The parser tells the two apart by whether a
// name is followed by `:`, the one place it looks two tokens ahead.
func (p *parser) parseCall() *ast.CallStmt {
	c := &ast.CallStmt{Name: p.expectIdent("a name", "")}
	open := p.expect(token.LParen, "")

	if p.tok.Kind != token.RParen {
		if p.atNamedArg() {
			c.Args = append(c.Args, p.parseNamedArg())
		} else {
			p.after = ast.OpInvalid
			c.Positional = p.parseExpr(lowest)
		}
		for p.tok.Kind == token.Comma {
			p.next()
			if p.tok.Kind == token.RParen {
				break
			}
			if !p.atNamedArg() {
				p.unexpected("a named argument", "every argument is written `name: value`, like `approvers: [\"payments-leads\"]`")
			}
			c.Args = append(c.Args, p.parseNamedArg())
		}
	}

	closing := p.expectClosing(token.RParen, open)
	c.Span = ast.Span{From: c.Name.Pos(), To: closing.End}
	return c
}

// atNamedArg reports whether the current token starts `name: value`.
func (p *parser) atNamedArg() bool {
	return (p.tok.Kind == token.Ident || p.tok.Kind.IsKeyword()) && p.peek().Kind == token.Colon
}

func (p *parser) parseNamedArg() *ast.NamedArg {
	a := &ast.NamedArg{Name: p.parseName("an argument name")}
	p.expect(token.Colon, "")
	p.after = ast.OpInvalid
	a.Value = p.parseExpr(lowest)
	return a
}
