package ast

import (
	"fmt"
	"strings"
)

// Dump renders a file one statement per line, indented by nesting, with
// expressions fully parenthesized as Sprint prints them and the span of
// every document, statement and declaration in brackets. Golden tests
// compare it, so a change to what the parser builds shows up as a diff.
func Dump(f *File) string {
	if f == nil {
		return ""
	}
	d := &dumper{}
	for i, doc := range f.Docs {
		if i > 0 {
			d.linef("---")
		}
		d.doc(doc)
	}
	return d.b.String()
}

type dumper struct {
	b      strings.Builder
	indent int
}

func (d *dumper) linef(format string, args ...any) {
	d.b.WriteString(strings.Repeat("  ", d.indent))
	fmt.Fprintf(&d.b, format, args...)
	d.b.WriteByte('\n')
}

// spanOf formats a node's range as [line:col-line:col].
func spanOf(n Node) string {
	if n == nil {
		return "[?]"
	}
	return "[" + n.Pos().String() + "-" + n.End().String() + "]"
}

func (d *dumper) doc(doc Doc) {
	if doc == nil {
		return
	}
	switch doc := doc.(type) {
	case *PolicyDoc:
		d.linef("policy %s: %s%s %s", doc.Name, doc.Kind.Name, pin(doc.Pin), spanOf(doc))
		d.indent++
		for _, u := range doc.Uses {
			d.use(u)
		}
		for _, s := range doc.Stmts {
			d.stmt(s)
		}
		d.indent--
	case *ModuleDoc:
		d.linef("module %s: %s%s %s", doc.Name, doc.Kind.Name, pin(doc.Pin), spanOf(doc))
		d.indent++
		for _, u := range doc.Uses {
			d.use(u)
		}
		for _, l := range doc.Lets {
			d.stmt(l)
		}
		d.indent--
	case *KindDoc:
		accepts := ""
		if doc.Accepts != nil {
			accepts = ", accepts: " + doc.Accepts.Text
		}
		d.linef("kind %s version %s%s %s", doc.Name.Name, doc.Version.Text, accepts, spanOf(doc))
		d.indent++
		for _, decl := range doc.Decls {
			d.decl(decl)
		}
		d.indent--
	default:
		d.linef("<%T>", doc)
	}
}

func (d *dumper) use(u *UseStmt) {
	if u == nil {
		return
	}
	var b strings.Builder
	b.WriteString("use " + u.Path.String())
	switch {
	case u.Alias != nil:
		b.WriteString(" as " + u.Alias.Name)
	case u.Items != nil:
		b.WriteString(".{")
		for i, it := range u.Items {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(it.Name.Name)
			if it.Alias != nil {
				b.WriteString(" as " + it.Alias.Name)
			}
		}
		b.WriteString("}")
	}
	d.linef("%s %s", b.String(), spanOf(u))
}

func (d *dumper) stmt(s Stmt) {
	if s == nil {
		return
	}
	switch s := s.(type) {
	case *UseStmt:
		d.use(s)
	case *ParamStmt:
		line := "param " + s.Name.Name + ": " + TypeString(s.Type)
		if s.Default != nil {
			line += " = " + Sprint(s.Default)
		}
		if s.Min != nil {
			line += ", min: " + Sprint(s.Min)
		}
		if s.Max != nil {
			line += ", max: " + Sprint(s.Max)
		}
		d.linef("%s %s", line, spanOf(s))
	case *LetStmt:
		pub := ""
		if s.Pub {
			pub = "pub "
		}
		d.linef("%slet %s = %s %s", pub, s.Name.Name, Sprint(s.Value), spanOf(s))
	case *WhenStmt:
		d.linef("when %s { %s", Sprint(s.Cond), spanOf(s))
		d.indent++
		for _, inner := range s.Body {
			d.stmt(inner)
		}
		d.indent--
		d.linef("}")
	case *AssertStmt:
		d.linef("assert(%s, %s) %s", s.Reason.Text, Sprint(s.Cond), spanOf(s))
	case *CallStmt:
		d.linef("%s %s", callString(s), spanOf(s))
	default:
		d.linef("<%T>", s)
	}
}

func callString(c *CallStmt) string {
	var b strings.Builder
	b.WriteString(c.Name.Name)
	b.WriteByte('(')
	if c.Positional != nil {
		b.WriteString(Sprint(c.Positional))
	}
	for i, a := range c.Args {
		if i > 0 || c.Positional != nil {
			b.WriteString(", ")
		}
		b.WriteString(a.Name.Name + ": " + Sprint(a.Value))
	}
	b.WriteByte(')')
	return b.String()
}

func (d *dumper) decl(decl Decl) {
	if decl == nil {
		return
	}
	switch decl := decl.(type) {
	case *TypeDecl:
		d.linef("type %s { %s", decl.Name.Name, spanOf(decl))
		d.indent++
		for _, f := range decl.Fields {
			d.linef("%s: %s", f.Name.Name, TypeString(f.Type))
		}
		d.indent--
		d.linef("}")
	case *InputDecl:
		d.linef("input %s: %s %s", decl.Name.Name, TypeString(decl.Type), spanOf(decl))
	case *FnDecl:
		params := make([]string, len(decl.Params))
		for i, p := range decl.Params {
			params[i] = TypeString(p)
		}
		d.linef("fn %s(%s) -> %s %s", decl.Name.Name, strings.Join(params, ", "), TypeString(decl.Result), spanOf(decl))
	case *DecisionDecl:
		head := "decision " + decl.Name.Name
		if len(decl.Fields) > 0 {
			head += "(" + fieldsString(decl.Fields) + ")"
		}
		reasons := make([]string, len(decl.Reasons))
		for i, r := range decl.Reasons {
			reasons[i] = r.Name
		}
		d.linef("%s { %s } %s", head, strings.Join(reasons, " "), spanOf(decl))
	case *PrecedenceDecl:
		names := make([]string, len(decl.Names))
		for i, n := range decl.Names {
			names[i] = n.Name
		}
		scope := ""
		if decl.Scope != nil {
			scope = decl.Scope.Name + ": "
		}
		d.linef("precedence %s%s %s", scope, strings.Join(names, " > "), spanOf(decl))
	case *ExclusiveDecl:
		outcomes := make([]string, len(decl.Outcomes))
		for i, o := range decl.Outcomes {
			outcomes[i] = o.String()
		}
		d.linef("exclusive %s %s", strings.Join(outcomes, ", "), spanOf(decl))
	case *CollectDecl:
		mode := "one"
		if decl.All {
			mode = "all"
		}
		d.linef("collect %s %s", mode, spanOf(decl))
	case *DefaultDecl:
		d.linef("default %s %s", callString(decl.Call), spanOf(decl))
	default:
		d.linef("<%T>", decl)
	}
}

func fieldsString(fields []*Field) string {
	parts := make([]string, len(fields))
	for i, f := range fields {
		parts[i] = f.Name.Name + ": " + TypeString(f.Type)
		if f.Default != nil {
			parts[i] += " = " + Sprint(f.Default)
		}
	}
	return strings.Join(parts, ", ")
}

// TypeString renders a type as written in source.
func TypeString(t Type) string {
	switch t := t.(type) {
	case *NamedType:
		return t.Name.Name
	case *OptionalType:
		return "?" + TypeString(t.Elem)
	case *ListType:
		return "list<" + TypeString(t.Elem) + ">"
	case *MapType:
		return "map<" + TypeString(t.Key) + ", " + TypeString(t.Value) + ">"
	case nil:
		return "<nil>"
	}
	return fmt.Sprintf("<%T>", t)
}

// pin renders a document header's `@N`, or nothing when it has none.
func pin(n *IntLit) string {
	if n == nil {
		return ""
	}
	return "@" + n.Text
}
