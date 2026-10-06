package workspace

import (
	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/parser"
)

// File is one file a project reads.
type File struct {
	Name   string // as diagnostics name it
	ID     string // what tells two files apart, such as the file's real path; Name when empty
	Source []byte
}

// Loader builds a project from files it parses once each: first the kind
// files named outside the paths, with [Loader.Kind], then the paths and
// the trusted paths, with [Loader.Load].
type Loader struct {
	p      *Project
	kinds  *kinds
	parsed map[string]*file // every file read, by its ID, so none is parsed twice
	order  []*file          // the same, in read order
}

// file is one file read and parsed.
type file struct {
	name string
	src  []byte
	docs []ast.Doc
}

// NewLoader starts a project with the kinds linked into the binary.
func NewLoader(linked []Linked) *Loader {
	return &Loader{
		p:      &Project{groups: map[string]*Group{}, owners: map[string]*Group{}, names: map[string]*bundle.Document{}, sources: map[string][]byte{}, kindDocs: map[string][]ast.Node{}},
		kinds:  newKinds(linked),
		parsed: map[string]*file{},
	}
}

// Kind reads a kind file named outside the paths and records its kind
// documents. Its other documents are left alone: they count only when
// the file is also among the paths. A file that parses but holds no kind
// document is an error, since it can't be what was meant, and so is a
// kind that doesn't match the one of the same name linked into the
// binary, as a stale export the host has to regenerate.
func (l *Loader) Kind(f File) humane.Error {
	parsed, err := l.parse(f, true)
	if err != nil {
		return err
	}
	for _, doc := range parsed.docs {
		if _, ok := doc.(*ast.KindDoc); ok {
			return nil
		}
	}
	for _, e := range l.p.errs {
		if e.File == parsed.name {
			return nil // it doesn't parse, which is reported with the rest
		}
	}
	return humane.New(parsed.name+" holds no kind document", "name the kind file the host exports, which starts with `kind Name version N`")
}

// Load reads the paths and the trusted paths, which must not hold the
// same file twice, and groups their documents by kind. Kinds come from,
// in this order: the kinds linked into the binary, the kind files given
// to [Loader.Kind], the kind documents among the paths, and those among
// the trusted paths. The same kind from two sources must be identical,
// and the later of two that differ is the error. A file read before,
// by its ID, isn't parsed again.
//
// Parse errors, kind documents that don't check or don't agree, documents
// naming a kind nobody provides and names defined twice are diagnostics,
// for [Project.Errors], never a failure. The Loader is spent afterwards.
func (l *Loader) Load(paths, trusted []File) *Project {
	// The paths are parsed before the trusted paths, so where a kind
	// document of each disagrees, the trusted one is reported.
	rfiles, tfiles := l.parseAll(paths), l.parseAll(trusted)
	l.p.files = len(rfiles)
	l.group(tfiles, true)
	for _, g := range l.p.groups {
		if len(g.trusted.Documents()) > 0 {
			g.Bundle.Trust(g.trusted)
		}
	}
	l.group(rfiles, false)
	l.indexKinds()
	l.p.known = l.kinds
	return l.p
}

// parseAll parses the files, in order. Only a kind file named outside
// the paths can fail a load, so a file among them never does.
func (l *Loader) parseAll(files []File) []*file {
	out := make([]*file, 0, len(files))
	for _, f := range files {
		parsed, _ := l.parse(f, false)
		out = append(out, parsed)
	}
	return out
}

// parse parses f, records its parse errors and its kind documents, and
// keeps it so it's never parsed again; a file parsed before is returned
// as it was. flag marks a kind file named outside the paths.
func (l *Loader) parse(f File, flag bool) (*file, humane.Error) {
	id := f.ID
	if id == "" {
		id = f.Name
	}
	if parsed, ok := l.parsed[id]; ok {
		return parsed, nil
	}
	tree, errs := parser.ParseFile(f.Name, f.Source)
	parsed := &file{name: f.Name, src: f.Source, docs: tree.Docs}
	l.parsed[id] = parsed
	l.order = append(l.order, parsed)
	l.p.sources[f.Name] = f.Source
	l.p.errs = append(l.p.errs, errs...)
	for _, doc := range parsed.docs {
		k, ok := doc.(*ast.KindDoc)
		if !ok || k.Name == nil {
			continue
		}
		l.p.kindDocs[f.Name] = append(l.p.kindDocs[f.Name], k)
		kerrs, err := l.kinds.add(f.Name, k, flag)
		if err != nil {
			return nil, err
		}
		l.p.errs = append(l.p.errs, kerrs...)
	}
	return parsed, nil
}

// group gives every policy and module of the files to the group of its
// kind, trusted or not. The first definition of a name wins; a later one
// is reported as defined twice, across kinds too, and so is a document
// whose kind no source provides.
func (l *Loader) group(files []*file, trusted bool) {
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
func (l *Loader) groupOf(k *Kind) *Group {
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
func (l *Loader) indexKinds() {
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
