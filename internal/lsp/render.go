package lsp

import (
	"fmt"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// code renders Sigil source as a markdown code block, or "" for none.
func code(src string) string {
	if src == "" {
		return ""
	}
	return "```sigil\n" + src + "\n```"
}

// kindHeader renders a kind's header line.
func kindHeader(k *kind.Kind) string {
	s := fmt.Sprintf("kind %s version %d", k.Name, k.Version)
	if k.Accepts > 1 {
		s += fmt.Sprintf(", accepts: %d", k.Accepts)
	}
	return s
}

// describeDoc renders a policy's or module's header.
func describeDoc(d ast.Doc) string {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return "policy " + d.Name.String() + header(d.Kind, d.Pin)
	case *ast.ModuleDoc:
		return "module " + d.Name.String() + header(d.Kind, d.Pin)
	}
	return ""
}

// header renders the `: Kind@N` of a header.
func header(k *ast.Ident, pin *ast.IntLit) string {
	if k == nil {
		return ""
	}
	if pin == nil {
		return ": " + k.Name
	}
	return ": " + k.Name + "@" + pin.Text
}

// paramsOf renders a policy's params, one per line, or "" for none.
func paramsOf(e *check.Exported) string {
	lines := make([]string, len(e.Params))
	for i, p := range e.Params {
		lines[i] = "param " + paramSource(p)
	}
	return strings.Join(lines, "\n")
}

// paramSource renders a param as its declaration writes it, without the
// keyword: `min_soak: duration = 24h, min: 1h`.
func paramSource(p *check.ExportedParam) string {
	s := typed(p.Name, p.Type)
	if p.Decl == nil {
		return s
	}
	if p.Decl.Default != nil {
		s += " = " + ast.Sprint(p.Decl.Default)
	}
	if p.Decl.Min != nil {
		s += ", min: " + ast.Sprint(p.Decl.Min)
	}
	if p.Decl.Max != nil {
		s += ", max: " + ast.Sprint(p.Decl.Max)
	}
	return s
}

// letsOf renders a document's pub lets with their types, one per line.
func letsOf(e *check.Exported) string {
	names := e.LetNames()
	lines := make([]string, len(names))
	for i, name := range names {
		lines[i] = "pub let " + typed(name, e.Lets[name])
	}
	return strings.Join(lines, "\n")
}

// structSource renders a struct type as a kind file declares it.
func structSource(s *types.Struct) string {
	var b strings.Builder
	b.WriteString("type " + s.Name + " {")
	for _, f := range s.Fields {
		b.WriteString("\n  " + f.Name + ": " + f.Type.String())
	}
	b.WriteString("\n}")
	return b.String()
}

// enumSource renders an enum as a kind file declares it.
func enumSource(e *types.Enum) string {
	return "enum " + e.Name + ": " + strings.Join(e.Values, " | ")
}

// declsOf renders the struct types and enums t is built from, so a hover
// over a name of a struct type shows its fields: t itself, or what an
// optional, a list or a map holds.
func declsOf(t types.Type, k *kind.Kind) string {
	switch t := t.(type) {
	case *types.Optional:
		return declsOf(t.Elem, k)
	case *types.List:
		return declsOf(t.Elem, k)
	case *types.Map:
		return declsOf(t.Value, k)
	case *types.Struct:
		if k != nil {
			if decl := k.Type(t.Name); decl != nil {
				t = decl
			}
		}
		return structSource(t)
	case *types.Enum:
		return enumSource(t)
	}
	return ""
}

// joinLines joins the non-empty parts with blank lines between them.
func joinLines(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}

// typed renders a name with its type, `name: type`, or the name alone
// when its type couldn't be worked out.
func typed(name string, t types.Type) string {
	if typeName(t) == "" {
		return name
	}
	return name + ": " + typeName(t)
}

// unchecked says that a name's type couldn't be worked out, or is ""
// when it could.
func unchecked(t types.Type) string {
	if typeName(t) != "" {
		return ""
	}
	return "Its type couldn't be worked out: the value has an error."
}
