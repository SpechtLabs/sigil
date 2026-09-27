// Package kindfile turns a parsed kind document into a kind.Kind.
//
// The loader resolves type names, evaluates the constant defaults and
// checks the one rule that only source has, that `reason: string` comes
// first in every decision. Everything else is kind.Validate's job; the
// loader records where each declaration is so those diagnostics point at
// the right line.
package kindfile

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/suggest"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// Load parses src as a kind file, which holds exactly one kind document,
// and returns the kind. On any error the kind is nil and the list holds
// every diagnostic.
func Load(file string, src []byte) (*kind.Kind, diag.ErrorList) {
	f, errs := parser.ParseFile(file, src)
	if errs != nil {
		return nil, errs
	}
	return FromFile(f)
}

// FromFile loads the kind from an already parsed file.
func FromFile(f *ast.File) (*kind.Kind, diag.ErrorList) {
	if f == nil {
		return nil, nil
	}
	const shape = "a kind file starts with `kind Name version N`"
	switch {
	case len(f.Docs) == 0:
		start := token.Pos{Offset: 0, Line: 1, Column: 1}
		return nil, diag.ErrorList{{File: f.Name, Msg: "file has no kind document", Help: shape, Pos: start, End: start}}
	case len(f.Docs) > 1:
		second := f.Docs[1]
		return nil, diag.ErrorList{{File: f.Name, Msg: "a kind file holds exactly one document", Help: "move the other documents to their own files", Pos: second.Pos(), End: second.End()}}
	}
	doc, ok := f.Docs[0].(*ast.KindDoc)
	if !ok {
		d := f.Docs[0]
		return nil, diag.ErrorList{{File: f.Name, Msg: "expected a kind document, found " + describe(d), Help: shape, Pos: d.Pos(), End: d.End()}}
	}
	return FromDoc(f.Name, doc)
}

func describe(d ast.Doc) string {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return "policy `" + d.Name.String() + "`"
	case *ast.ModuleDoc:
		return "module `" + d.Name.String() + "`"
	}
	return fmt.Sprintf("%T", d)
}

// FromDoc loads the kind from one kind document.
func FromDoc(file string, doc *ast.KindDoc) (*kind.Kind, diag.ErrorList) {
	if doc == nil {
		return nil, nil
	}
	l := &loader{
		file:    file,
		kind:    &kind.Kind{Name: doc.Name.Name, Version: int(doc.Version.Value)},
		structs: map[string]*types.Struct{},
		decls:   map[*ast.TypeDecl]*types.Struct{},
		loc:     map[string]span{},
	}
	l.set("kind", doc.Name)
	l.set("kind.version", doc.Version)

	// Struct types can refer to each other in any order, so their shells
	// exist before any field type is resolved.
	for _, d := range doc.Decls {
		if t, ok := d.(*ast.TypeDecl); ok {
			l.declareType(t)
		}
	}
	for _, d := range doc.Decls {
		switch d := d.(type) {
		case *ast.TypeDecl:
			l.typeFields(d)
		case *ast.InputDecl:
			l.input(d)
		case *ast.FnDecl:
			l.fn(d)
		case *ast.DecisionDecl:
			l.decision(d)
		}
	}
	// The default names a decision and its fields, so it comes after
	// every decision is known, wherever it sits in the file.
	for _, d := range doc.Decls {
		switch d := d.(type) {
		case *ast.PrecedenceDecl:
			l.precedence(d)
		case *ast.CollectDecl:
			l.collect(d)
		case *ast.DefaultDecl:
			l.defaultDecl(d)
		}
	}

	l.validate(doc)
	if len(l.errs) > 0 {
		l.errs.Sort()
		return nil, l.errs
	}
	return l.kind, nil
}

type span struct {
	start, end token.Pos
}

type loader struct {
	kind    *kind.Kind
	structs map[string]*types.Struct        // by name, first declaration wins
	decls   map[*ast.TypeDecl]*types.Struct // the struct built for each declaration
	loc     map[string]span                 // validation key to source span
	// The declarations that may appear once.
	precedenceAt, collectAt, defaultAt ast.Node
	file                               string
	errs                               diag.ErrorList
}

// set records where the thing named by key is, for kind.Validate.
func (l *loader) set(key string, n ast.Node) {
	l.loc[key] = span{n.Pos(), n.End()}
}

func (l *loader) errorf(at ast.Node, help, format string, args ...any) {
	l.errs = append(l.errs, &diag.Error{File: l.file, Msg: fmt.Sprintf(format, args...), Help: help, Pos: at.Pos(), End: at.End()})
}

func (l *loader) declareType(d *ast.TypeDecl) {
	if d == nil {
		return
	}
	s := &types.Struct{Name: d.Name.Name}
	l.kind.Types = append(l.kind.Types, s)
	l.decls[d] = s
	if _, dup := l.structs[s.Name]; !dup {
		l.structs[s.Name] = s
	}
	l.set("type "+s.Name, d.Name)
}

func (l *loader) typeFields(d *ast.TypeDecl) {
	if d == nil {
		return
	}
	s := l.decls[d]
	for _, f := range d.Fields {
		key := "type " + s.Name + ".field " + f.Name.Name
		l.set(key, f.Name)
		l.set(key+".type", f.Type)
		s.Fields = append(s.Fields, &types.Field{Name: f.Name.Name, Type: l.resolve(f.Type)})
	}
}

func (l *loader) input(d *ast.InputDecl) {
	if d == nil {
		return
	}
	key := "input " + d.Name.Name
	l.set(key, d.Name)
	l.set(key+".type", d.Type)
	l.kind.Inputs = append(l.kind.Inputs, &kind.Input{Name: d.Name.Name, Type: l.resolve(d.Type)})
}

func (l *loader) fn(d *ast.FnDecl) {
	if d == nil {
		return
	}
	key := "fn " + d.Name.Name
	l.set(key, d.Name)
	l.set(key+".result", d.Result)
	f := &kind.Func{Name: d.Name.Name, Result: l.resolve(d.Result)}
	for i, p := range d.Params {
		l.set(fmt.Sprintf("%s.param %d", key, i+1), p)
		f.Params = append(f.Params, l.resolve(p))
	}
	l.kind.Funcs = append(l.kind.Funcs, f)
}

// decision loads a decision, taking `reason: string` off the front of its
// fields: the model implies it, and a decision without it is an error.
func (l *loader) decision(d *ast.DecisionDecl) {
	if d == nil {
		return
	}
	key := "decision " + d.Name.Name
	l.set(key, d.Name)
	dec := &kind.Decision{Name: d.Name.Name}

	fields := d.Fields
	if len(fields) > 0 && fields[0].Name.Name == "reason" {
		reason := fields[0]
		if named, ok := reason.Type.(*ast.NamedType); !ok || named.Name.Name != "string" {
			l.errorf(reason.Type, "the reason is a stable identifier, so it's always a string", "reason must be a string, not %s", ast.TypeString(reason.Type))
		}
		if reason.Default != nil {
			l.errorf(reason.Default, "every constructor passes a reason; there's nothing to default", "reason can't have a default")
		}
		fields = fields[1:]
	} else {
		l.errorf(d.Name, "every decision takes a literal reason first; payload fields follow it",
			"decision %s must declare `reason: string` as its first field", d.Name.Name)
	}

	for _, f := range fields {
		fkey := key + ".field " + f.Name.Name
		l.set(fkey, f.Name)
		l.set(fkey+".type", f.Type)
		pf := &kind.Field{Name: f.Name.Name, Type: l.resolve(f.Type)}
		if f.Default != nil {
			l.set(fkey+".default", f.Default)
			// A default that fails to evaluate still counts as one, so the
			// field isn't also reported as required by the default decision.
			pf.HasDefault = true
			if v, err := constant.Eval(f.Default, pf.Type); err != nil {
				err.File = l.file
				l.errs = append(l.errs, err)
			} else {
				pf.Default = v
			}
		}
		dec.Fields = append(dec.Fields, pf)
	}
	l.kind.Decisions = append(l.kind.Decisions, dec)
}

func (l *loader) precedence(d *ast.PrecedenceDecl) {
	if d == nil {
		return
	}
	if l.once(&l.precedenceAt, d, "precedence") {
		return
	}
	l.set("precedence", d)
	for _, n := range d.Names {
		l.set("precedence."+n.Name, n)
		l.kind.Precedence = append(l.kind.Precedence, n.Name)
	}
}

func (l *loader) collect(d *ast.CollectDecl) {
	if d == nil {
		return
	}
	if l.once(&l.collectAt, d, "collect all") {
		return
	}
	l.set("collect", d)
	l.kind.Collect = true
}

// once reports and skips a second declaration of something a kind has one
// of.
func (l *loader) once(at *ast.Node, d ast.Node, what string) bool {
	if at == nil {
		return false
	}
	if *at != nil {
		l.errorf(d, fmt.Sprintf("a kind declares `%s` once; remove one", what), "%s is declared twice", what)
		return true
	}
	*at = d
	return false
}

func (l *loader) defaultDecl(d *ast.DefaultDecl) {
	if d == nil {
		return
	}
	if l.once(&l.defaultAt, d, "default") {
		return
	}
	l.set("default", d)
	call := d.Call
	def := &kind.Default{Decision: call.Name.Name, Args: map[string]any{}}
	l.kind.Default = def

	const shape = "the default is written `default deny(\"no_rule_matched\")`"
	// The reason's key points at whatever is wrong with it, so the model's
	// "empty reason" finding lands on the loader's error and is dropped.
	switch lit := call.Positional.(type) {
	case nil:
		l.set("default.reason", call)
		l.errorf(call, shape, "the default needs a reason")
	case *ast.StringLit:
		l.set("default.reason", lit)
		if lit.Raw {
			l.errorf(lit, "raw strings are for patterns; write the reason as \"...\"", "the default's reason must be a double-quoted string")
		}
		def.Reason = lit.Value
	default:
		l.set("default.reason", call.Positional)
		l.errorf(call.Positional, shape, "the default's reason must be a string literal")
	}

	decl := l.kind.Decision(def.Decision)
	for _, arg := range call.Args {
		name := arg.Name.Name
		l.set("default.arg "+name, arg.Name)
		if _, dup := def.Args[name]; dup {
			l.errorf(arg.Name, "pass each field once", "field %q is given twice", name)
			continue
		}
		if decl == nil {
			continue // the unknown decision is reported by kind.Validate
		}
		f := decl.Field(name)
		if f == nil {
			l.errorf(arg.Name, decl.Name+" is declared as: "+decl.Signature(), "decision %s has no payload field %q", decl.Name, name)
			continue
		}
		v, err := constant.Eval(arg.Value, f.Type)
		if err != nil {
			err.File = l.file
			l.errs = append(l.errs, err)
			continue
		}
		def.Args[name] = v
	}
}

// resolve turns a type expression into a type. An unknown name is
// reported here, where the declared names are known for a "did you mean"
// hint, and becomes types.Invalid, which kind.Validate lets pass.
func (l *loader) resolve(t ast.Type) types.Type {
	switch t := t.(type) {
	case *ast.NamedType:
		name := t.Name.Name
		if b, ok := types.Lookup(name); ok {
			return b
		}
		if s, ok := l.structs[name]; ok {
			return s
		}
		l.unknownType(t.Name)
		return types.Invalid
	case *ast.OptionalType:
		return &types.Optional{Elem: l.resolve(t.Elem)}
	case *ast.ListType:
		return &types.List{Elem: l.resolve(t.Elem)}
	case *ast.MapType:
		return &types.Map{Key: l.resolve(t.Key), Value: l.resolve(t.Value)}
	}
	return types.Invalid
}

func (l *loader) unknownType(name *ast.Ident) {
	if name == nil {
		return
	}
	candidates := make([]string, 0, 6+len(l.kind.Types))
	candidates = append(candidates, "bool", "int", "float", "string", "duration", "timestamp")
	for _, s := range l.kind.Types {
		candidates = append(candidates, s.Name)
	}
	help := "declare it with `type " + name.Name + " { ... }`, or use a built-in type"
	if closest, ok := suggest.Closest(name.Name, candidates); ok {
		help = "did you mean `" + closest + "`?"
	}
	l.errorf(name, help, "unknown type `%s`", name.Name)
}

// validate runs the model's rules and anchors each finding: at the span
// the key names, or at the header when the key isn't in the file. A
// finding at the position of a loader error is dropped, since the loader
// already said what's wrong there.
func (l *loader) validate(doc *ast.KindDoc) {
	if doc == nil {
		return
	}
	reported := map[token.Pos]bool{}
	for _, e := range l.errs {
		reported[e.Pos] = true
	}
	locate := func(key string) (token.Pos, token.Pos, bool) {
		s, ok := l.loc[key]
		return s.start, s.end, ok
	}
	for _, e := range l.kind.Validate(locate) {
		e.File = l.file
		if !e.Pos.IsValid() {
			e.Pos, e.End = doc.Name.Pos(), doc.Version.End()
		}
		if reported[e.Pos] {
			continue
		}
		reported[e.Pos] = true
		l.errs = append(l.errs, e)
	}
}
