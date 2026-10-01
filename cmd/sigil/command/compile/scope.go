package compile

import (
	"slices"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// contents is what goes into the binary: whole files, and the policies
// and modules they hold.
type contents struct {
	files *project.Files
	docs  map[string]bool // by name
	scope map[string]bool // with --policy, the documents in the root's scope, by name; nil for every document
}

// everything returns every file the check read, and every document.
func everything(p *project.Project, f *project.Files) *contents {
	docs := map[string]bool{}
	for _, name := range p.Names() {
		docs[name] = true
	}
	return &contents{files: f, docs: docs}
}

// scoped returns the files the root needs, as `check --policy` scopes it:
// those that hold the root or a document it uses, directly or through
// others, trusted ones included, and those that hold a kind document of
// a kind their documents are written against. A file goes in whole, so
// the documents it holds besides the ones in scope go in too, and so do
// the kinds those are written against. Every other file stays out.
func scoped(p *project.Project, f *project.Files, root string) *contents {
	s := p.ScopeOf([]string{root})
	byFile := map[string][]string{} // every document, by the file it's in
	keep := map[string]bool{}
	in := map[string]bool{}
	for _, name := range p.Names() {
		g := p.Group(name)
		if g == nil {
			continue // its kind wasn't found, which is an error outside the scope
		}
		file := g.Bundle.Document(name).File
		byFile[file] = append(byFile[file], name)
		if s.Bundle(g).Document(name) != nil {
			keep[file] = true
			in[name] = true
		}
	}
	all := slices.Concat(f.Kinds, f.Paths, f.Trusted)
	kindsIn := map[string][]string{}
	for _, file := range all {
		kindsIn[file.Name] = kindDocs(file)
	}
	// A kind file can hold documents too, which bring their own kinds
	// in, so this goes on until no file is added.
	for {
		used := map[string]bool{}
		for file := range keep {
			for _, name := range byFile[file] {
				used[p.Group(name).Kind.Model.Name] = true
			}
		}
		added := false
		for _, file := range all {
			if !keep[file.Name] && slices.ContainsFunc(kindsIn[file.Name], func(k string) bool { return used[k] }) {
				keep[file.Name] = true
				added = true
			}
		}
		if !added {
			break
		}
	}
	docs := map[string]bool{}
	for file := range keep {
		for _, name := range byFile[file] {
			docs[name] = true
		}
	}
	kept := func(files []workspace.File) []workspace.File {
		return slices.DeleteFunc(slices.Clone(files), func(file workspace.File) bool { return !keep[file.Name] })
	}
	return &contents{files: &project.Files{Kinds: kept(f.Kinds), Paths: kept(f.Paths), Trusted: kept(f.Trusted)}, docs: docs, scope: in}
}

// kindDocs returns the names of the kinds a file's kind documents define.
func kindDocs(f workspace.File) []string {
	tree, _ := parser.ParseFile(f.Name, f.Source)
	var out []string
	for _, doc := range tree.Docs {
		if k, ok := doc.(*ast.KindDoc); ok && k.Name != nil {
			out = append(out, k.Name.Name)
		}
	}
	return out
}
