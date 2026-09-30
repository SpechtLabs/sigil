package project

import (
	"fmt"
	"sort"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
)

// kinds is every kind a project knows, by name, from the first source
// that provided it.
type kinds struct {
	byName map[string]*known
}

// known is one kind and where it came from. A kind document that doesn't
// check leaves kind nil: its name is known, so its documents aren't
// reported as written against a missing kind, but they aren't checked.
type known struct {
	kind   *Kind
	file   string       // the file of its first document; empty for a linked kind
	doc    *ast.KindDoc // its first document; nil for a linked kind
	linked bool
}

// newKinds starts with the kinds linked into the binary.
func newKinds(linked []Linked) *kinds {
	ks := &kinds{byName: map[string]*known{}}
	for _, l := range linked {
		if _, dup := ks.byName[l.Model.Name]; !dup {
			ks.byName[l.Model.Name] = &known{kind: &Kind{Model: l.Model, Binding: l.Binding, Host: true}, linked: true}
		}
	}
	return ks
}

// add checks a kind document from file and records it, or checks that it
// matches the kind of the same name already known. Its diagnostics are
// returned for the project to report. A kind file named on the command
// line (flag) that doesn't match a linked kind fails outright, as a stale
// export the host has to regenerate.
func (ks *kinds) add(file string, doc *ast.KindDoc, flag bool) (diag.ErrorList, humane.Error) {
	c := check.New(file)
	k := c.Kind(doc)
	prev := ks.byName[doc.Name.Name]
	switch {
	case prev == nil:
		known := &known{file: file, doc: doc}
		if k != nil {
			known.kind = &Kind{Model: k, Binding: gokind.Synthesize(k)}
		}
		ks.byName[doc.Name.Name] = known
		return c.Errors(), nil
	case k == nil || prev.kind == nil || prev.kind.Model.Source() == k.Source():
		return c.Errors(), nil
	case prev.linked && flag:
		return nil, humane.New(
			fmt.Sprintf("%s doesn't match the kind %s linked into this binary", file, k.Name),
			"regenerate the kind file with this binary's `export "+k.Name+" --out "+file+"`, or rebuild the binary from the host's current code",
		)
	case prev.linked:
		return diag.ErrorList{{
			File: file, Pos: doc.Name.Pos(), End: doc.Name.End(),
			Msg:  fmt.Sprintf("kind document %s doesn't match the kind %s linked into this binary", k.Name, k.Name),
			Help: "regenerate the kind file with this binary's `export " + k.Name + " --out " + file + "`, or rebuild the binary from the host's current code",
		}}, nil
	}
	return diag.ErrorList{{
		File: file, Pos: doc.Name.Pos(), End: doc.Name.End(),
		Msg:  fmt.Sprintf("kind document %s differs from the one at %s", k.Name, at(prev.file, prev.doc.Name.Pos())),
		Help: "a kind has one definition, and the first source wins: the kind files named with --kind or in the configuration file, then the paths, then the trusted paths; regenerate this document from the host's Schema(), or remove it",
	}}, nil
}

// lookup returns the kind called name and whether any source provided
// it; the kind is nil when its document doesn't check.
func (ks *kinds) lookup(name string) (*Kind, bool) {
	k, ok := ks.byName[name]
	if !ok {
		return nil, false
	}
	return k.kind, true
}

// missing is the diagnostic for doc, in file, whose header names a kind
// no source provides, with a "did you mean" when a known kind is close.
func (ks *kinds) missing(file string, doc ast.Doc, name string, ref *ast.Ident) *diag.Error {
	names := make([]string, 0, len(ks.byName))
	for n := range ks.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	help := "add its kind file to the paths or name it with --kind"
	if near, ok := diag.Nearest(ref.Name, names); ok {
		help = fmt.Sprintf("did you mean `%s`? %s", near, help)
	}
	return &diag.Error{
		File: file, Pos: ref.Pos(), End: ref.End(),
		Msg:  fmt.Sprintf("%s %s is written against kind %s, but no kind %s was found", describe(doc), name, ref.Name, ref.Name),
		Help: help,
	}
}
