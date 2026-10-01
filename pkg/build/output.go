package build

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing/fstest"

	"github.com/aymanbagabas/go-udiff"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// DriftError is what [Diff] returns when the files on disk aren't what
// the documents render: the paths of the stale and the missing files.
// Its message holds a unified diff of every stale file.
type DriftError struct {
	Stale   []string // files whose content differs from the rendered source
	Missing []string // files that don't exist
	diffs   []string
}

// CheckError is what [Check] returns when a rendered document doesn't
// type-check: every diagnostic, rendered the way the CLI renders them,
// with the Go call site of the statement each one falls in.
type CheckError struct {
	rendered    string
	Diagnostics []Diagnostic
}

// Diagnostic is one problem [Check] found.
type Diagnostic struct {
	// Site is the builder call of the innermost statement the diagnostic
	// falls in; nil for one in a document written by hand.
	Site     *Site
	Message  string          // what's wrong, on one line
	Help     string          // how to fix it; empty when there's no obvious fix
	Position policy.Position // start of the offending source
	End      policy.Position // just after it
}

// rendered is a document's rendered file.
type rendered struct {
	doc   *document
	src   []byte
	spans []span
}

// hiding is a file system with some files left out: a base bundle whose
// files a rendered document replaces.
type hiding struct {
	fsys fs.FS
	hide map[string]bool
}

// FS renders the documents into an in-memory file system, each at its
// [Doc.Path], for [policy.Kind.Load], [policy.From] and [policy.Trusted].
// The error is [Errors] when a document doesn't render, or two documents
// share a name or a path.
func FS(docs ...Doc) (fs.FS, error) {
	files, err := renderAll(callSite("build.FS"), docs)
	if err != nil {
		return nil, err
	}
	m := fstest.MapFS{}
	for _, f := range files {
		m[f.doc.path] = &fstest.MapFile{Data: f.src, Mode: 0o644}
	}
	return m, nil
}

// Write renders the documents and writes each to its [Doc.Path] under
// dir, creating directories as needed. Nothing is written when a document
// doesn't render.
func Write(dir string, docs ...Doc) error {
	files, err := renderAll(callSite("build.Write"), docs)
	if err != nil {
		return err
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f.doc.path))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			return fmt.Errorf("build: creating the directory for %s: %w", f.doc.path, err)
		}
		if err := os.WriteFile(p, f.src, 0o644); err != nil { //nolint:gosec // policy files are read by the tools that check them, like any source file
			return fmt.Errorf("build: writing %s: %w", f.doc.path, err)
		}
	}
	return nil
}

// Diff compares the files in fsys, at each document's [Doc.Path], with
// what the documents render. It returns nil when every file holds exactly
// the rendered bytes, and otherwise a [*DriftError] that names each stale
// and missing file. A test calls it to catch a rendered file nobody
// regenerated after the Go code changed.
func Diff(fsys fs.FS, docs ...Doc) error {
	files, err := renderAll(callSite("build.Diff"), docs)
	if err != nil {
		return err
	}
	drift := &DriftError{}
	for _, f := range files {
		got, err := fs.ReadFile(fsys, f.doc.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			drift.Missing = append(drift.Missing, f.doc.path)
		case err != nil:
			return fmt.Errorf("build: reading %s: %w", f.doc.path, err)
		case !bytes.Equal(got, f.src):
			drift.Stale = append(drift.Stale, f.doc.path)
			drift.diffs = append(drift.diffs, udiff.Unified(f.doc.path+" (on disk)", f.doc.path+" (rendered)", string(got), string(f.src)))
		}
	}
	if len(drift.Stale) == 0 && len(drift.Missing) == 0 {
		return nil
	}
	return drift
}

// Check type-checks every document against kind k, in one bundle with
// the `.sigil` files of base, the documents written by hand, which may be
// nil. A rendered document replaces a file of base at the same path.
// Modules are checked like policies, so a module needs no policy that
// imports it.
//
// The error is [Errors] when a document doesn't render, and a
// [*CheckError] when the bundle doesn't check; its diagnostics in a
// rendered document name the builder call of the statement they fall
// in, as the line `= go: freeze.go:31: build.Pub`.
func Check[In any](k *policy.Kind[In], base fs.FS, docs ...Doc) error {
	s := callSite("build.Check")
	if k == nil {
		return Errors{s.errorf("the kind is nil")}
	}
	files, err := renderAll(s, docs)
	if err != nil {
		return err
	}
	c := k.Contract()
	var errs Errors
	for _, f := range files {
		if name := f.doc.contract.Model.Name; name != c.Model.Name {
			errs = append(errs, f.doc.site.errorf("%s is built for kind %s, not %s", f.doc.name, name, c.Model.Name))
		}
	}
	if errs != nil {
		return errs
	}

	b := bundle.New(c.Model)
	byPath := map[string]rendered{}
	for _, f := range files {
		byPath[f.doc.path] = f
	}
	if base != nil {
		hide := map[string]bool{}
		for p := range byPath {
			hide[p] = true
		}
		if err := b.Load(hiding{fsys: base, hide: hide}); err != nil {
			return fmt.Errorf("build: reading the base documents: %w", err)
		}
	}
	for _, f := range files {
		b.Add(f.doc.path, f.src)
	}
	b.Check()
	if b.Errors() == nil {
		return nil
	}
	return checkError(b, byPath)
}

// Error lists the stale and the missing files, with a diff of each stale
// one.
func (e *DriftError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "build: %d rendered file(s) out of date; regenerate them from the Go code", len(e.Stale)+len(e.Missing))
	for i, p := range e.Stale {
		fmt.Fprintf(&b, "\n%s is stale:\n%s", p, strings.TrimRight(e.diffs[i], "\n"))
	}
	for _, p := range e.Missing {
		fmt.Fprintf(&b, "\n%s is missing", p)
	}
	return b.String()
}

// Error returns the rendered diagnostics.
func (e *CheckError) Error() string { return e.rendered }

// Open opens name unless it's hidden.
func (h hiding) Open(name string) (fs.File, error) {
	if h.hide[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return h.fsys.Open(name)
}

// ReadDir lists a directory without the hidden files.
func (h hiding) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, err := fs.ReadDir(h.fsys, name)
	out := entries[:0]
	for _, e := range entries {
		if !h.hide[path.Join(name, e.Name())] {
			out = append(out, e)
		}
	}
	return out, err
}

// renderAll renders every document, for the output function called at s.
// It reports every error, and two documents with the same name or path.
func renderAll(s Site, docs []Doc) ([]rendered, error) {
	var errs Errors
	var out []rendered
	paths, names := map[string]string{}, map[string]bool{}
	for i, d := range docs {
		if isNil(d) {
			errs = append(errs, s.errorf("document %d is nil", i+1))
			continue
		}
		doc := d.document()
		src, spans, e := doc.render()
		errs = append(errs, e...)
		if prev, ok := paths[doc.path]; ok {
			errs = append(errs, doc.site.errorf("%s and %s both render to %s; give one another path with build.WithPath", prev, doc.name, doc.path))
		}
		if names[doc.name] {
			errs = append(errs, doc.site.errorf("two documents are named %s; a name has one definition in a bundle", doc.name))
		}
		paths[doc.path], names[doc.name] = doc.name, true
		out = append(out, rendered{doc: doc, src: src, spans: spans})
	}
	if errs != nil {
		return nil, errs
	}
	return out, nil
}

// checkError renders the bundle's diagnostics, adding the Go call site
// to those in a rendered document.
func checkError(b *bundle.Bundle, byPath map[string]rendered) *CheckError {
	e := &CheckError{}
	errs := b.Resolve(b.Errors())
	parts := make([]string, 0, len(errs))
	for _, d := range errs {
		text := strings.TrimRight(diag.Render(d, b.SourceOf(d.File)), "\n")
		var site *Site
		if f, ok := byPath[d.File]; ok {
			s, found := at(f.spans, d.Pos.Line)
			if !found {
				s = f.doc.site
			}
			site = &s
			text += "\n" + strings.Repeat(" ", len(strconv.Itoa(max(d.Pos.Line, 1)))) + " = go: " + s.String()
		}
		parts = append(parts, text)
		e.Diagnostics = append(e.Diagnostics, Diagnostic{
			Site:     site,
			Message:  d.Msg,
			Help:     d.Help,
			Position: policy.Position{File: d.File, Document: d.Doc, Line: d.Pos.Line, Column: d.Pos.Column},
			End:      policy.Position{File: d.File, Document: d.Doc, Line: d.End.Line, Column: d.End.Column},
		})
	}
	e.rendered = strings.Join(parts, "\n\n")
	return e
}
