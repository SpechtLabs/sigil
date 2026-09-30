package project

import (
	"io"
	"os"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/parser"
)

// stdinName names what a "-" path reads, in diagnostics.
const stdinName = "<stdin>"

// loader is one [Load].
type loader struct {
	p      *Project
	kinds  *kinds
	stdin  io.Reader
	parsed map[string]*file // every file read, by name, so none is parsed twice
	order  []*file          // the same, in read order
}

// file is one file read and parsed.
type file struct {
	name string
	src  []byte
	docs []ast.Doc
}

// kindFile reads a kind file named outside the paths and records its kind
// documents. Its other documents are left alone: they count only when
// the file is also among the paths. A file that parses but holds no kind
// document is an error, since it can't be what was meant.
func (l *loader) kindFile(name string) humane.Error {
	src, err := os.ReadFile(name) //nolint:gosec // the path comes from the command line, which is the point
	if err != nil {
		return humane.Wrap(err, "the kind file couldn't be read", "pass the exported kind file with --kind")
	}
	f, herr := l.parse(clean(name), src, true)
	if herr != nil {
		return herr
	}
	for _, doc := range f.docs {
		if _, ok := doc.(*ast.KindDoc); ok {
			return nil
		}
	}
	for _, e := range l.p.errs {
		if e.File == f.name {
			return nil // it doesn't parse, which is reported with the rest
		}
	}
	return humane.New(f.name+" holds no kind document", "name the kind file the host exports, which starts with `kind Name version N`")
}

// readAll reads and parses the files, "-" from stdin, in order.
func (l *loader) readAll(names []string) ([]*file, humane.Error) {
	out := make([]*file, 0, len(names))
	for _, name := range names {
		f, err := l.read(name)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// read reads and parses one file, or returns the one already read under
// that name.
func (l *loader) read(name string) (*file, humane.Error) {
	if name == "-" {
		name = stdinName
	}
	if f, ok := l.parsed[name]; ok {
		return f, nil
	}
	var src []byte
	var err error
	if name == stdinName {
		src, err = io.ReadAll(l.stdin)
		if err != nil {
			return nil, humane.Wrap(err, "stdin couldn't be read", "pipe a policy bundle in, or name files instead of `-`")
		}
	} else if src, err = os.ReadFile(name); err != nil { //nolint:gosec // the path comes from the command line, which is the point
		return nil, humane.Wrap(err, name+" couldn't be read", "check the file's permissions")
	}
	return l.parse(name, src, false)
}

// parse parses src as name, records its parse errors and its kind
// documents, and keeps it so it's never parsed again. flag marks a kind
// file named on the command line.
func (l *loader) parse(name string, src []byte, flag bool) (*file, humane.Error) {
	if f, ok := l.parsed[name]; ok {
		return f, nil
	}
	parsed, errs := parser.ParseFile(name, src)
	f := &file{name: name, src: src, docs: parsed.Docs}
	l.parsed[name] = f
	l.order = append(l.order, f)
	l.p.sources[name] = src
	l.p.errs = append(l.p.errs, errs...)
	for _, doc := range f.docs {
		k, ok := doc.(*ast.KindDoc)
		if !ok || k.Name == nil {
			continue
		}
		l.p.kindDocs[name] = append(l.p.kindDocs[name], k)
		kerrs, err := l.kinds.add(name, k, flag)
		if err != nil {
			return nil, err
		}
		l.p.errs = append(l.p.errs, kerrs...)
	}
	return f, nil
}

// group gives every policy and module of the files to the group of its
// kind, trusted or not. The first definition of a name wins; a later one
// is reported as defined twice, across kinds too, and so is a document
// whose kind no source provides.
func (l *loader) group(files []*file, trusted bool) {
	for _, f := range files {
		byGroup := map[*Group][]ast.Doc{}
		var groups []*Group
		for _, doc := range f.docs {
			name, ref, ok := header(doc)
			if !ok {
				continue
			}
			if prev := l.p.names[name]; prev != nil {
				// The diagnostic names the document it's in, which the
				// project doesn't index, so check --policy keeps it
				// when the name is one the policies use.
				e := bundle.Redefined(f.name, doc, prev)
				e.Doc = name
				l.p.errs = append(l.p.errs, e)
				continue
			}
			d := &bundle.Document{Node: doc, Name: name, File: f.name, Trusted: trusted}
			l.p.names[name] = d
			l.p.docs = append(l.p.docs, d)
			k, found := l.kinds.lookup(ref.Name)
			switch {
			case !found:
				l.p.errs = append(l.p.errs, l.kinds.missing(f.name, doc, name, ref))
				continue
			case k == nil:
				continue // its kind document doesn't check, which is reported
			}
			g := l.groupOf(k)
			if byGroup[g] == nil {
				groups = append(groups, g)
			}
			byGroup[g] = append(byGroup[g], doc)
			l.p.owners[name] = g
		}
		for _, g := range groups {
			into := g.Bundle
			if trusted {
				into = g.trusted
			}
			into.Index(f.name, f.src, byGroup[g])
		}
	}
}

// groupOf returns k's group, creating it on first use.
func (l *loader) groupOf(k *Kind) *Group {
	if g, ok := l.p.groups[k.Model.Name]; ok {
		return g
	}
	g := &Group{Kind: k, Bundle: bundle.New(k.Model), trusted: bundle.New(k.Model)}
	l.p.groups[k.Model.Name] = g
	return g
}

// indexKinds gives every group the kind documents of every file read, so
// a `use` of a kind's name says it's a kind rather than an unknown
// document.
func (l *loader) indexKinds() {
	for _, f := range l.order {
		var docs []ast.Doc
		for _, doc := range f.docs {
			if k, ok := doc.(*ast.KindDoc); ok && k.Name != nil {
				docs = append(docs, doc)
			}
		}
		if docs == nil {
			continue
		}
		for _, g := range l.p.groups {
			g.Bundle.Index(f.name, f.src, docs)
		}
	}
}
