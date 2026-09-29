package check

import (
	"fmt"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// LoadKind parses src as a kind file, which holds exactly one kind
// document, and returns the kind. file names the source in diagnostics.
// On any error the kind is nil and the list holds every diagnostic; a
// parse error stops before the kind is checked.
func LoadKind(file string, src []byte) (*kind.Kind, diag.ErrorList) {
	f, errs := parser.ParseFile(file, src)
	if errs != nil {
		return nil, errs
	}
	c := New(file)
	k := c.KindFile(f)
	return k, c.Errors()
}

// KindFile builds the kind from an already parsed kind file, which must
// hold exactly one document, a kind. The kind is nil when anything is
// wrong, and [Checker.Errors] says what.
func (c *Checker) KindFile(f *ast.File) *kind.Kind {
	if f == nil {
		return nil
	}
	const shape = "a kind file starts with `kind Name version N`"
	switch {
	case len(f.Docs) == 0:
		start := token.Pos{Offset: 0, Line: 1, Column: 1}
		c.errs = append(c.errs, &diag.Error{File: c.file, Msg: "file has no kind document", Help: shape, Pos: start, End: start})
		return nil
	case len(f.Docs) > 1:
		c.errorf(f.Docs[1], "move the other documents to their own files", "a kind file holds exactly one document")
		return nil
	}
	doc, ok := f.Docs[0].(*ast.KindDoc)
	if !ok {
		c.errorf(f.Docs[0], shape, "expected a kind document, found %s", describeDoc(f.Docs[0]))
		return nil
	}
	return c.Kind(doc)
}

// Kind builds the kind a kind document declares. It resolves type names,
// evaluates the constant defaults and checks the rules only source can
// break: `precedence`, `collect` and `default` declared at most once, a
// scoped `precedence` that names a declared decision and is its only one,
// and a default whose reason is a bare name and whose arguments each name
// a payload field once. Everything else is [kind.Kind.Validate]'s job; the checker records
// where each declaration is so those diagnostics point at the right line,
// and drops one at a place it has already reported. The kind is nil when
// anything is wrong, and [Checker.Errors] says what.
func (c *Checker) Kind(doc *ast.KindDoc) *kind.Kind {
	if doc == nil {
		return nil
	}
	before := len(c.errs)
	l := &kindLoader{
		c:     c,
		kind:  &kind.Kind{Name: doc.Name.Name, Version: int(doc.Version.Value), Accepts: 1},
		decls: map[*ast.TypeDecl]*types.Struct{},
		loc:   map[string]span{},
	}
	l.set("kind", doc.Name)
	l.set("kind.version", doc.Version)
	if doc.Accepts != nil {
		l.kind.Accepts = int(doc.Accepts.Value)
		l.set("kind.accepts", doc.Accepts)
	}

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
		case *ast.ExclusiveDecl:
			l.exclusive(d)
		case *ast.CollectDecl:
			l.collect(d)
		case *ast.DefaultDecl:
			l.defaultDecl(d)
		}
	}

	l.validate(doc)
	if len(c.errs) > before {
		return nil
	}
	return l.kind
}

// KindMismatch reports a kind document that doesn't match the host's
// contract for the kind called name: a stale export. It only records the
// diagnostic, at the document's name; the caller decides that the two
// differ, as the bundle does by comparing their [kind.Kind.Source].
func (c *Checker) KindMismatch(doc *ast.KindDoc, name string) {
	c.errorf(doc.Name, "the host's Go definition is the contract; regenerate this file from Schema()",
		"kind document %s doesn't match the host's kind", name)
}

// describeDoc names a document that isn't a kind, for the error saying so.
func describeDoc(d ast.Doc) string {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		return "policy `" + d.Name.String() + "`"
	case *ast.ModuleDoc:
		return "module `" + d.Name.String() + "`"
	}
	return fmt.Sprintf("%T", d)
}
