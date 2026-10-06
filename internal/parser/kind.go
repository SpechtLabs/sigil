package parser

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/token"
)

// parseKind parses a kind document from its `kind` keyword to the end of
// the document. Only syntax is checked here; that the reason doesn't name
// a type, that precedence names every decision and the other validity
// rules from docs/reference/kind-files.md are the checker's.
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
	case token.KwEnum:
		return p.parseEnum()
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
	case token.KwConflict:
		return p.parseConflict()
	}
	p.unexpected("a declaration (`type`, `enum`, `input`, `fn`, `decision`, `precedence`, `exclusive`, `collect`, `default` or `conflict`)", "")
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
	case token.KwType, token.KwEnum, token.KwInput, token.KwFn, token.KwDecision, token.KwPrecedence, token.KwExclusive, token.KwCollect, token.KwDefault, token.KwConflict:
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

// parseEnum parses `enum Name: a | b | c`.
func (p *parser) parseEnum() *ast.EnumDecl {
	const shape = "an enum is written `enum Name: a | b | c`"
	kw := p.tok
	p.next()
	d := &ast.EnumDecl{Name: p.expectIdent("a name after `enum`", shape)}
	p.expect(token.Colon, shape)
	d.Values = p.parseAlternatives("an enum value", shape)
	d.Span = ast.Span{From: kw.Pos, To: d.Values[len(d.Values)-1].End()}
	return d
}

// parseAlternatives parses `a | b | c`, the values of an enum or the
// reasons of a decision. Line breaks may fall on either side of a `|`.
// want names one value for errors, like "an enum value".
func (p *parser) parseAlternatives(want, shape string) []*ast.Ident {
	names := []*ast.Ident{p.expectAlternative(want, shape)}
	for {
		switch p.tok.Kind {
		case token.Pipe:
			p.next()
			names = append(names, p.expectAlternative(want, shape))
			continue
		case token.Comma:
			p.unexpected("`|` between two values", shape)
		}
		return names
	}
}

// expectAlternative consumes one name of an enum's values or a decision's
// reasons. A keyword gets its own hint, since `one` or `all` are easy
// names to reach for and read like identifiers. A keyword that starts a
// declaration or a document more likely follows a trailing `|`.
func (p *parser) expectAlternative(want, shape string) *ast.Ident {
	if p.tok.Kind.IsKeyword() && !isDeclKeyword(p.tok.Kind) && !isHeaderKeyword(p.tok.Kind) {
		p.errorTok(p.tok, fmt.Sprintf("expected %s, found the keyword `%s`", want, p.tok.Text),
			"a keyword can't be a value; pick another name")
	}
	return p.expectIdent(want, shape)
}

// decisionShape is the hint for a malformed decision.
const decisionShape = "a decision is written `decision name { reason: a | b  field: type = default }`, with the payload fields optional"

// parseDecision parses `decision name { reason: a | b  field: type = default }`,
// or the legacy form `decision name(field: type = default) { a b }`, which
// it marks Legacy. A block of bare names is legacy too, with or without
// the parentheses.
func (p *parser) parseDecision() *ast.DecisionDecl {
	kw := p.tok
	p.next()
	d := &ast.DecisionDecl{Name: p.expectIdent("a name after `decision`", decisionShape)}
	if p.tok.Kind == token.LParen {
		d.Legacy = true
		d.Fields = p.parsePayloadFields()
	}
	open := p.expect(token.LBrace, decisionShape)
	switch {
	case d.Legacy && p.atField():
		p.errorTok(p.tok, "the payload fields go inside the braces, next to `reason:`",
			"move them out of the parentheses: `decision approve { reason: release_manager  bake: duration = 1h }`")
	case d.Legacy, p.tok.Kind == token.Ident && !p.atField():
		d.Legacy = true
		p.parseLegacyReasons(d, open)
	default:
		p.parseDecisionFields(d, open)
	}
	closing := p.expectClosing(token.RBrace, open)
	d.Span = ast.Span{From: kw.Pos, To: closing.End}
	return d
}

// atField reports whether the current token starts `name:`. A field may
// be named like a keyword.
func (p *parser) atField() bool {
	return (p.tok.Kind == token.Ident || p.tok.Kind.IsKeyword()) && p.peek().Kind == token.Colon
}

// parseDecisionFields parses the body of a decision in the current
// syntax: the `reason:` field and the payload fields, in any order, up to
// the closing brace. Like the fields of a type, each ends where the next
// `name:` begins.
func (p *parser) parseDecisionFields(d *ast.DecisionDecl, open token.Token) {
	if d == nil {
		return
	}
	const field = "a payload field is written `name: type` or `name: type = default`"
	for p.tok.Kind != token.RBrace {
		// As in a type body, a declaration or header keyword that isn't a
		// field name means the brace is missing.
		if p.tok.Kind == token.EOF || p.tok.Kind == token.Separator ||
			((isDeclKeyword(p.tok.Kind) || isHeaderKeyword(p.tok.Kind)) && p.peek().Kind != token.Colon) {
			p.expectClosing(token.RBrace, open)
		}
		name := p.parseName("a field name or `reason:`")
		p.expect(token.Colon, field)
		if name.Name == "reason" {
			if d.ReasonName != nil {
				p.errorAt(name.Pos(), name.End(), "decision "+d.Name.Name+" declares `reason` twice",
					"list every reason in one field, like `reason: not_eligible | soak_too_short`")
			}
			d.ReasonName = name
			d.Reasons = p.parseReasons()
			continue
		}
		f := &ast.Field{Name: name, Type: p.parseType()}
		if p.tok.Kind == token.Assign {
			p.next()
			p.after = ast.OpInvalid
			f.Default = p.parseFieldDefault()
		}
		d.Fields = append(d.Fields, f)
	}
	if d.ReasonName == nil {
		p.errorAt(open.Pos, open.End, "decision "+d.Name.Name+" declares no reason",
			"list its reasons first, like `reason: not_eligible | soak_too_short`")
	}
}

// parseReasons parses the values after `reason:`. The reason is an inline
// list of names, never a type, so a type's syntax gets a hint toward the
// list; a bare name that happens to be a type is the checker's to reject.
func (p *parser) parseReasons() []*ast.Ident {
	const (
		shape  = "the reasons are listed inline, like `reason: not_eligible | soak_too_short`"
		asType = "the reason can't name a type"
	)
	start := p.tok
	if start.Kind == token.Question || start.Kind == token.Coalesce {
		p.errorTok(start, asType, shape)
	}
	reasons := p.parseAlternatives("a reason", shape)
	switch p.tok.Kind {
	case token.Lt:
		if len(reasons) == 1 {
			p.errorAt(start.Pos, p.tok.End, asType, shape)
		}
	case token.Assign:
		p.errorTok(p.tok, "the reason can't have a default",
			"every constructor names its reason, like `deny(reason: soak_too_short)`")
	}
	return reasons
}

// parseLegacyReasons parses the reason block of the legacy syntax, bare
// names up to the closing brace.
func (p *parser) parseLegacyReasons(d *ast.DecisionDecl, open token.Token) {
	if d == nil {
		return
	}
	for p.tok.Kind != token.RBrace && p.tok.Kind != token.EOF && !p.atDeclEnd() {
		d.Reasons = append(d.Reasons, p.expectIdent("a reason name", "reasons are bare identifiers, one per line, like `soak_too_short`"))
	}
	if len(d.Reasons) == 0 {
		p.errorAt(open.Pos, open.End, "decision "+d.Name.Name+" declares no reasons", "every decision needs at least one reason in its block")
	}
}

// parsePayloadFields parses `(name: type = default, ...)` after a
// decision's name, in the legacy syntax. A first field called `reason` is
// an older form still, where the reason was a string parameter, and gets
// a hint toward the current syntax.
func (p *parser) parsePayloadFields() []*ast.Field {
	open := p.expect(token.LParen, decisionShape)
	var fields []*ast.Field
	for p.tok.Kind != token.RParen && p.tok.Kind != token.EOF {
		f := &ast.Field{Name: p.parseName("a field name")}
		if len(fields) == 0 && f.Name.Name == "reason" {
			p.errorAt(f.Name.Pos(), f.Name.End(), "the reason isn't a string parameter",
				"list the reasons inside the braces: `decision deny { reason: not_eligible | soak_too_short }`")
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

// parseDefault parses `default deny(reason: no_rule_matched, field: value)`.
func (p *parser) parseDefault() *ast.DefaultDecl {
	kw := p.tok
	p.next()
	if p.tok.Kind != token.Ident || p.peek().Kind != token.LParen {
		p.unexpected("a decision constructor", "the default is written `default deny(reason: no_rule_matched)`")
	}
	d := &ast.DefaultDecl{Call: p.parseCall()}
	d.Span = ast.Span{From: kw.Pos, To: d.Call.End()}
	return d
}

// parseConflict parses `conflict deny(reason: conflicting_rules, field: value)`.
func (p *parser) parseConflict() *ast.ConflictDecl {
	kw := p.tok
	p.next()
	if p.tok.Kind != token.Ident || p.peek().Kind != token.LParen {
		p.unexpected("a decision constructor", "the conflict outcome is written `conflict deny(reason: conflicting_rules)`")
	}
	d := &ast.ConflictDecl{Call: p.parseCall()}
	d.Span = ast.Span{From: kw.Pos, To: d.Call.End()}
	return d
}

// syncKind skips to the next kind declaration or the end of the document.
func (p *parser) syncKind() {
	p.sync(func(t token.Token) bool {
		return t.Kind == token.Separator || p.atHeader(t) || isDeclKeyword(t.Kind)
	})
}

// parseFieldDefault parses the default of a field in a decision body, where
// a keyword followed by `:` starts the next field. The flag is cleared even
// when a parse error unwinds through here.
func (p *parser) parseFieldDefault() ast.Expr {
	p.inField = true
	defer func() { p.inField = false }()
	return p.parseExpr(lowest)
}
