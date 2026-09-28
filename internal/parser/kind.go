package parser

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/token"
)

// parseKind parses a kind document from its `kind` keyword to the end of
// the document. Only syntax is checked here; that `reason: string` comes
// first, that precedence names every decision and the other validity rules
// from docs/reference/kind-files.md are the checker's.
func (p *parser) parseKind() *ast.KindDoc {
	const shape = "a kind starts with `kind Name version N`, optionally followed by `, accepts: M`"
	kw := p.tok
	p.next()
	doc := &ast.KindDoc{Name: p.expectIdent("the kind's name after `kind`", shape)}
	p.expect(token.KwVersion, shape)
	if p.tok.Kind != token.Int {
		p.unexpected("a version number", shape)
	}
	doc.Version = p.parsePrimary().(*ast.IntLit)
	end := doc.Version.End()
	if p.tok.Kind == token.Comma {
		p.next()
		name := p.expectIdent("`accepts` after `,`", shape)
		if name.Name != "accepts" {
			p.errorAt(name.Pos(), name.End(), fmt.Sprintf("a kind header has no option `%s`", name.Name), shape)
		}
		p.expect(token.Colon, shape)
		if p.tok.Kind != token.Int {
			p.unexpected("the oldest version policies may pin", shape)
		}
		doc.Accepts = p.parsePrimary().(*ast.IntLit)
		end = doc.Accepts.End()
	}

	for !p.atDocEnd() {
		p.recover(func() {
			d := p.parseDecl()
			doc.Decls = append(doc.Decls, d)
			end = d.End()
		}, p.syncKind)
	}
	doc.Span = ast.Span{From: kw.Pos, To: end}
	return doc
}

func (p *parser) parseDecl() ast.Decl {
	switch p.tok.Kind {
	case token.KwType:
		return p.parseTypeDecl()
	case token.KwInput:
		return p.parseInput()
	case token.KwFn:
		return p.parseFn()
	case token.KwDecision:
		return p.parseDecision()
	case token.KwPrecedence:
		return p.parsePrecedence()
	case token.KwExclusive:
		return p.parseExclusive()
	case token.KwCollect:
		return p.parseCollect()
	case token.KwDefault:
		return p.parseDefault()
	}
	p.unexpected("a declaration (`type`, `input`, `fn`, `decision`, `precedence`, `exclusive`, `collect` or `default`)", "")
	return nil
}

// parseTypeDecl parses `type Name { field: type ... }`. Fields have no
// separator; each ends where the next `name:` begins.
func (p *parser) parseTypeDecl() *ast.TypeDecl {
	kw := p.tok
	p.next()
	d := &ast.TypeDecl{Name: p.expectIdent("a type name after `type`", "a type is written `type Name { field: type }`")}
	open := p.expect(token.LBrace, "a type is written `type Name { field: type }`")

	for p.tok.Kind != token.RBrace {
		// A keyword followed by `:` is a field named like a keyword. Any
		// other declaration or header keyword means the brace is missing.
		if p.tok.Kind == token.EOF || p.tok.Kind == token.Separator ||
			((isDeclKeyword(p.tok.Kind) || isHeaderKeyword(p.tok.Kind)) && p.peek().Kind != token.Colon) {
			p.expectClosing(token.RBrace, open)
		}
		f := &ast.Field{Name: p.parseName("a field name")}
		p.expect(token.Colon, "a field is written `name: type`")
		f.Type = p.parseType()
		if p.tok.Kind == token.Assign {
			p.errorTok(p.tok, "a field in a `type` body can't have a default", "defaults belong to decision payload fields")
		}
		d.Fields = append(d.Fields, f)
	}
	d.Span = ast.Span{From: kw.Pos, To: p.tok.End}
	p.next()
	return d
}

// isDeclKeyword reports whether k starts a kind declaration.
func isDeclKeyword(k token.Kind) bool {
	switch k {
	case token.KwType, token.KwInput, token.KwFn, token.KwDecision, token.KwPrecedence, token.KwCollect, token.KwDefault:
		return true
	}
	return false
}

// parseInput parses `input name: type`.
func (p *parser) parseInput() *ast.InputDecl {
	const shape = "an input is written `input name: type`"
	kw := p.tok
	p.next()
	d := &ast.InputDecl{Name: p.expectIdent("a name after `input`", shape)}
	p.expect(token.Colon, shape)
	d.Type = p.parseType()
	d.Span = ast.Span{From: kw.Pos, To: d.Type.End()}
	return d
}

// parseFn parses `fn name(type, ...) -> type`. Parameters are types
// only: policies pass arguments positionally, so names would be
// documentation the exporter can't recover from a Go function anyway.
func (p *parser) parseFn() *ast.FnDecl {
	const shape = "a function is written `fn name(type, type) -> type`"
	kw := p.tok
	p.next()
	d := &ast.FnDecl{Name: p.expectIdent("a name after `fn`", shape)}
	open := p.expect(token.LParen, shape)
	for p.tok.Kind != token.RParen {
		if p.tok.Kind == token.Ident && p.peek().Kind == token.Colon {
			p.errorTok(p.tok, "expected a parameter type, found a name", "parameters have types only; policies pass arguments by position")
		}
		d.Params = append(d.Params, p.parseType())
		if p.tok.Kind != token.Comma {
			break
		}
		p.next()
	}
	p.expectClosing(token.RParen, open)
	p.expect(token.Arrow, shape)
	d.Result = p.parseType()
	d.Span = ast.Span{From: kw.Pos, To: d.Result.End()}
	return d
}

// parseDecision parses `decision name(reason: string, field: type = default, ...)`.
func (p *parser) parseDecision() *ast.DecisionDecl {
	const shape = "a decision is written `decision name(field: type) { reason ... }`, with the payload fields optional"
	kw := p.tok
	p.next()
	d := &ast.DecisionDecl{Name: p.expectIdent("a name after `decision`", shape)}
	if p.tok.Kind == token.LParen {
		d.Fields = p.parsePayloadFields(shape)
	}
	open := p.expect(token.LBrace, shape)
	for p.tok.Kind != token.RBrace && p.tok.Kind != token.EOF && !p.atDeclEnd() {
		d.Reasons = append(d.Reasons, p.expectIdent("a reason name", "reasons are bare identifiers, one per line, like `soak_too_short`"))
	}
	if len(d.Reasons) == 0 {
		p.errorAt(open.Pos, open.End, "decision "+d.Name.Name+" declares no reasons", "every decision needs at least one reason in its block")
	}
	closing := p.expectClosing(token.RBrace, open)
	d.Span = ast.Span{From: kw.Pos, To: closing.End}
	return d
}

// parsePayloadFields parses `(name: type = default, ...)` after a
// decision's name. A first field called `reason` is the old form, where
// the reason was a string parameter, and gets a hint toward the block.
func (p *parser) parsePayloadFields(shape string) []*ast.Field {
	open := p.expect(token.LParen, shape)
	var fields []*ast.Field
	for p.tok.Kind != token.RParen && p.tok.Kind != token.EOF {
		f := &ast.Field{Name: p.parseName("a field name")}
		if len(fields) == 0 && f.Name.Name == "reason" {
			p.errorAt(f.Name.Pos(), f.Name.End(), "the reason isn't a payload field",
				"reasons are declared in a block after the fields: `decision deny { not_eligible soak_too_short }`")
		}
		p.expect(token.Colon, "a field is written `name: type` or `name: type = default`")
		f.Type = p.parseType()
		if p.tok.Kind == token.Assign {
			p.next()
			p.after = ast.OpInvalid
			f.Default = p.parseExpr(lowest)
		}
		fields = append(fields, f)
		if p.tok.Kind != token.Comma {
			break
		}
		p.next()
	}
	p.expectClosing(token.RParen, open)
	return fields
}

// atDeclEnd reports whether the current token starts another declaration
// or ends the document, for a reason block that was never closed.
func (p *parser) atDeclEnd() bool {
	return isDeclKeyword(p.tok.Kind) || p.atDocEnd()
}

func (p *parser) parsePrecedence() *ast.PrecedenceDecl {
	const shape = "precedence is written `precedence deny > review > approve`, highest first, or `precedence approve: a > b` for one decision's reasons"
	kw := p.tok
	p.next()
	d := &ast.PrecedenceDecl{}
	first := p.expectIdent("a decision name after `precedence`", shape)
	if p.tok.Kind == token.Colon {
		p.next()
		d.Scope = first
		first = p.expectIdent("a reason name after `:`", shape)
	}
	d.Names = []*ast.Ident{first}
	for p.tok.Kind == token.Gt {
		p.next()
		d.Names = append(d.Names, p.expectIdent("a name after `>`", shape))
	}
	d.Span = ast.Span{From: kw.Pos, To: d.Names[len(d.Names)-1].End()}
	return d
}

// parseExclusive parses `exclusive a, b.x, c`: at least two outcomes.
func (p *parser) parseExclusive() *ast.ExclusiveDecl {
	const shape = "exclusive is written `exclusive grant_a, grant_b`, or `exclusive approve.release_manager, approve.lgtm` for reasons"
	kw := p.tok
	p.next()
	d := &ast.ExclusiveDecl{Outcomes: []*ast.OutcomeRef{p.parseOutcomeRef(shape)}}
	for p.tok.Kind == token.Comma {
		p.next()
		d.Outcomes = append(d.Outcomes, p.parseOutcomeRef(shape))
	}
	if len(d.Outcomes) < 2 {
		p.errorAt(kw.Pos, d.Outcomes[0].End(), "exclusive needs at least two outcomes", shape)
	}
	d.Span = ast.Span{From: kw.Pos, To: d.Outcomes[len(d.Outcomes)-1].End()}
	return d
}

// parseOutcomeRef parses a decision name with an optional `.reason`.
func (p *parser) parseOutcomeRef(shape string) *ast.OutcomeRef {
	o := &ast.OutcomeRef{Decision: p.expectIdent("a decision name", shape)}
	if p.tok.Kind == token.Dot {
		p.next()
		o.Reason = p.expectIdent("a reason name after `.`", shape)
	}
	return o
}

func (p *parser) parseCollect() *ast.CollectDecl {
	kw := p.tok
	p.next()
	if p.tok.Kind != token.KwOne && p.tok.Kind != token.KwAll {
		p.unexpected("`one` or `all`", "`collect one` returns the highest-ranked decision, `collect all` every decision that fired")
	}
	d := &ast.CollectDecl{All: p.tok.Kind == token.KwAll, From: kw.Pos, To: p.tok.End}
	p.next()
	return d
}

// parseDefault parses `default deny("reason", field: value)`.
func (p *parser) parseDefault() *ast.DefaultDecl {
	kw := p.tok
	p.next()
	if p.tok.Kind != token.Ident || p.peek().Kind != token.LParen {
		p.unexpected("a decision constructor", "the default is written `default deny(no_rule_matched)`")
	}
	d := &ast.DefaultDecl{Call: p.parseCall()}
	d.Span = ast.Span{From: kw.Pos, To: d.Call.End()}
	return d
}

// syncKind skips to the next kind declaration or the end of the document.
func (p *parser) syncKind() {
	p.sync(func(t token.Token) bool {
		return t.Kind == token.Separator || p.atHeader(t) || isDeclKeyword(t.Kind)
	})
}
