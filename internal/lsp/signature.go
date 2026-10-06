package lsp

import (
	"fmt"
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// signature is the signature of the call the cursor is in, as signature
// help shows it: its label, its parameters as spans of the label, and the
// one the cursor is on.
type signature struct {
	doc    string // markdown about the whole call
	label  string
	params []parameter
	active int // the parameter the cursor is on, or -1 for none
}

// parameter is one parameter of a signature: its span of the label, as
// byte offsets, and markdown about it.
type parameter struct {
	doc      string
	from, to int
}

// signatureAt returns the signature of the call the cursor at offset is
// in: a host function's, with its parameters by position, a decision
// constructor's, with its reason and payload fields, or an invoked
// policy's, with its params. It returns nil outside every call, and in a
// call of anything else.
func (v *view) signatureAt(offset int) *signature {
	c := scan(v.src, offset)
	if c.site == nil {
		return nil
	}
	env := v.env(c)
	if env == nil {
		return nil
	}
	s := c.site
	b, _ := env.Lookup(s.name)
	switch {
	case s.fn && b.Entity == check.Function:
		return v.funcSignature(b.Func, s, env.Kind())
	case !s.fn && b.Entity == check.DecisionName:
		return v.decisionSignature(env.Kind(), env.Kind().Decision(s.name), s, c.prefix)
	case !s.fn && b.Entity == check.Invocable:
		return invocationSignature(s.name, b.Doc, s, c.prefix)
	}
	return nil
}

// funcSignature is a host function's signature, `split(string, string)
// -> list<string>`, on the argument at the cursor's position.
func (v *view) funcSignature(f *kind.Func, s *site, k *kind.Kind) *signature {
	var b builder
	b.write(f.Name + "(")
	for i, p := range f.Params {
		if i > 0 {
			b.write(", ")
		}
		b.param(p.String(), "")
	}
	b.write(") -> " + f.Result.String())
	sig := b.signature(v.kindComment(k.Name, func(d ast.Decl) *ast.Ident { return fnDecl(d, f.Name) }))
	sig.active = -1
	if s.index < len(f.Params) {
		sig.active = s.index
	}
	return sig
}

// decisionSignature is a constructor's signature, `review(reason:
// service_owner, approvers: list<string>, tier: Tier = standard)`, on the
// argument the cursor is in, or between arguments on the next one to
// give.
func (v *view) decisionSignature(k *kind.Kind, d *kind.Decision, s *site, prefix string) *signature {
	var b builder
	names := make([]string, 0, 1+len(d.Fields))
	names = append(names, reasonArg)
	b.write(d.Name + "(")
	b.param(reasonArg+": "+strings.Join(d.Reasons, " | "), "The reason, one of the decision's: "+strings.Join(d.Reasons, ", ")+".")
	for _, f := range d.Fields {
		b.write(", ")
		doc := v.kindComment(k.Name, func(decl ast.Decl) *ast.Ident { return decisionField(decl, d.Name, f.Name) })
		if !f.HasDefault {
			doc = joinLines("Required.", doc)
		}
		b.param(fieldSource(f), doc)
		names = append(names, f.Name)
	}
	b.write(")")
	sig := b.signature(v.kindComment(k.Name, func(decl ast.Decl) *ast.Ident { return decisionDecl(decl, d.Name) }))
	sig.active = activeArg(names, s, prefix)
	return sig
}

// invocationSignature is an invoked policy's signature, `guardrails(
// min_soak: duration = 24h)`, with each param's bounds in its
// documentation.
func invocationSignature(name string, e *check.Exported, s *site, prefix string) *signature {
	var b builder
	names := make([]string, len(e.Params))
	b.write(name + "(")
	for i, p := range e.Params {
		if i > 0 {
			b.write(", ")
		}
		label := typed(p.Name, p.Type)
		var notes []string
		if p.Decl != nil && p.Decl.Default != nil {
			label += " = " + ast.Sprint(p.Decl.Default)
		}
		if p.Required {
			notes = append(notes, "Required.")
		}
		if p.Decl != nil && p.Decl.Min != nil {
			notes = append(notes, fmt.Sprintf("At least `%s`.", ast.Sprint(p.Decl.Min)))
		}
		if p.Decl != nil && p.Decl.Max != nil {
			notes = append(notes, fmt.Sprintf("At most `%s`.", ast.Sprint(p.Decl.Max)))
		}
		b.param(label, strings.Join(notes, " "))
		names[i] = p.Name
	}
	b.write(")")
	sig := b.signature("The policy `" + e.Name + "`.")
	sig.active = activeArg(names, s, prefix)
	return sig
}

// activeArg returns which of the named arguments names the cursor is on:
// the one whose value it's in, or between arguments the first not given
// yet that starts with what's typed, or -1.
func activeArg(names []string, s *site, prefix string) int {
	if s.arg != "" {
		return slices.Index(names, s.arg)
	}
	return slices.IndexFunc(names, func(n string) bool { return !slices.Contains(s.given, n) && strings.HasPrefix(n, prefix) })
}

// builder writes a signature's label, keeping each parameter's span.
type builder struct {
	label  strings.Builder
	params []parameter
}

// write writes s to the label.
func (b *builder) write(s string) { b.label.WriteString(s) }

// param writes a parameter's label, with doc about it.
func (b *builder) param(label, doc string) {
	from := b.label.Len()
	b.write(label)
	b.params = append(b.params, parameter{from: from, to: b.label.Len(), doc: doc})
}

// signature returns what b wrote, with doc about the whole call.
func (b *builder) signature(doc string) *signature {
	return &signature{label: b.label.String(), params: b.params, doc: doc}
}

// protocol converts the signature into the protocol's help: the
// parameter spans in UTF-16 code units, as the protocol counts a label.
func (sig *signature) protocol() *protocol.SignatureHelp {
	info := protocol.SignatureInformation{Label: sig.label, Parameters: make([]protocol.ParameterInformation, len(sig.params))}
	if sig.doc != "" {
		info.Documentation = &protocol.MarkupContent{Kind: protocol.Markdown, Value: sig.doc}
	}
	for i, p := range sig.params {
		info.Parameters[i].Label = [2]uint32{unitsOf(sig.label[:p.from]), unitsOf(sig.label[:p.to])}
		if p.doc != "" {
			info.Parameters[i].Documentation = &protocol.MarkupContent{Kind: protocol.Markdown, Value: p.doc}
		}
	}
	// An active parameter past the last one highlights none, where an
	// absent one would highlight the first.
	active := len(sig.params)
	if sig.active >= 0 {
		active = sig.active
	}
	at := uint32(active) //nolint:gosec // an index into the parameters, which are few
	return &protocol.SignatureHelp{Signatures: []protocol.SignatureInformation{info}, ActiveParameter: &at}
}

// unitsOf returns how many UTF-16 code units s is.
func unitsOf(s string) uint32 {
	n := 0
	for _, r := range s {
		n += utf16Len(r)
	}
	return uint32(n)
}
