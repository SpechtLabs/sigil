package bench

import (
	"bytes"
	"context"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/gotool"
)

// majorVersion matches the /vN element that ends a module path from v2 on.
var majorVersion = regexp.MustCompile(`^v[0-9]+$`)

// declared returns the benchmarks the base revision declares in its own
// *_bench_test.go files, keyed by package directory in the form
// filepath.Dir gives a workload file, before the checkout's files replace
// them. A directory the base revision has no Go package in is left out:
// every benchmark in it is new.
func declared(ctx context.Context, base string, dirs []string) (map[string]map[string]bool, humane.Error) {
	out := map[string]map[string]bool{}
	var patterns []string
	for _, dir := range dirs {
		if hasGoFiles(filepath.Join(base, dir)) {
			out[dir] = map[string]bool{}
			patterns = append(patterns, "./"+filepath.ToSlash(dir))
		}
	}
	if len(patterns) == 0 {
		return out, nil
	}
	targets, err := gotool.Discover(ctx, base, patterns, "Benchmark")
	if err != nil {
		return nil, err
	}
	for _, t := range targets {
		if strings.HasSuffix(t.File, benchSuffix) {
			out[filepath.Clean(t.Dir)][t.Name] = true
		}
	}
	return out, nil
}

// stripNew removes the Benchmark functions of the file at name that keep
// doesn't list, which the base revision can't have, and the imports only
// they used, and returns the rest of the file. A file without such a
// function comes back unchanged. A new benchmark's helpers therefore
// belong in an ordinary _test.go file, which the comparison doesn't copy.
func stripNew(name string, src []byte, keep map[string]bool) ([]byte, humane.Error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, humane.Wrap(err, "can't parse "+name, "fix the benchmark file so it compiles")
	}
	kept := make([]ast.Decl, 0, len(f.Decls))
	var gone []*ast.FuncDecl
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && strings.HasPrefix(fn.Name.Name, "Benchmark") && !keep[fn.Name.Name] {
			gone = append(gone, fn)
			continue
		}
		kept = append(kept, d)
	}
	if len(gone) == 0 {
		return src, nil
	}
	f.Decls = kept
	f.Comments = commentsOutside(f.Comments, gone)
	dropImports(f, gone)

	var b bytes.Buffer
	if err := format.Node(&b, fset, f); err != nil {
		return nil, humane.Wrap(err, "can't print "+name+" without its new benchmarks", "report this as a devtool bug")
	}
	return b.Bytes(), nil
}

// hasGoFiles reports whether dir holds a Go source file.
func hasGoFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			return true
		}
	}
	return false
}

// commentsOutside returns the comment groups that aren't part of one of
// the removed functions, doc comment included.
func commentsOutside(groups []*ast.CommentGroup, gone []*ast.FuncDecl) []*ast.CommentGroup {
	out := groups[:0]
	for _, g := range groups {
		inside := false
		for _, fn := range gone {
			from := fn.Pos()
			if fn.Doc != nil {
				from = fn.Doc.Pos()
			}
			if g.Pos() >= from && g.End() <= fn.End() {
				inside = true
				break
			}
		}
		if !inside {
			out = append(out, g)
		}
	}
	return out
}

// dropImports removes the imports that only the removed functions used.
// An import's name is read from the file, or guessed from its path the way
// Go names most packages; an import whose name can't be matched stays.
func dropImports(f *ast.File, gone []*ast.FuncDecl) {
	removed := map[string]bool{}
	for _, fn := range gone {
		qualifiers(fn, removed)
	}
	used := map[string]bool{}
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			continue
		}
		qualifiers(d, used)
	}

	decls := f.Decls[:0]
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.IMPORT {
			decls = append(decls, d)
			continue
		}
		specs := g.Specs[:0]
		for _, s := range g.Specs {
			name := importName(s.(*ast.ImportSpec))
			if name != "" && removed[name] && !used[name] {
				continue
			}
			specs = append(specs, s)
		}
		if g.Specs = specs; len(specs) > 0 {
			decls = append(decls, g)
		}
	}
	f.Decls = decls

	var imports []*ast.ImportSpec
	for _, d := range f.Decls {
		if g, ok := d.(*ast.GenDecl); ok && g.Tok == token.IMPORT {
			for _, s := range g.Specs {
				imports = append(imports, s.(*ast.ImportSpec))
			}
		}
	}
	f.Imports = imports
}

// qualifiers records the identifiers n qualifies a selector with, which
// are the package names it uses, among others.
func qualifiers(n ast.Node, into map[string]bool) {
	ast.Inspect(n, func(n ast.Node) bool {
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if id, ok := sel.X.(*ast.Ident); ok {
				into[id.Name] = true
			}
		}
		return true
	})
}

// importName returns the name an import binds: its explicit name, or the
// last element of its path without a major version. Blank and dot imports
// bind none that a selector could show, so they report "".
func importName(s *ast.ImportSpec) string {
	if s.Name != nil {
		if s.Name.Name == "_" || s.Name.Name == "." {
			return ""
		}
		return s.Name.Name
	}
	p, err := strconv.Unquote(s.Path.Value)
	if err != nil {
		return ""
	}
	name := path.Base(p)
	if majorVersion.MatchString(name) {
		name = path.Base(path.Dir(p))
	}
	return name
}
