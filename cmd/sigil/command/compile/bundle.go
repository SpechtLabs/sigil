package compile

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/config"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/diagnose"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/payload"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// stdinName names what a "-" path reads, as package project names it.
const stdinName = "<stdin>"

// namer names a file of the bundle the way the binary keeps it: relative
// to the directory of the configuration file, or to the working
// directory without one, so the same repository compiles to the same
// digest from any directory in it, and no build machine's absolute paths
// end up in the binary.
type namer struct {
	wd   string // the working directory, which relative names are relative to
	base string // the directory names are made relative to
}

// newNamer returns the namer for a run that read cfg. Without a working
// directory, it keeps every name as it is.
func newNamer(cfg *config.Config) namer {
	wd, err := os.Getwd()
	if err != nil {
		return namer{}
	}
	n := namer{wd: wd, base: wd}
	if cfg.File != "" {
		if dir, err := filepath.Abs(filepath.Dir(cfg.File)); err == nil {
			n.base = dir
		}
	}
	return n
}

// name returns name relative to the base, slash-separated, with `..` in
// it for a file outside the base. Only an absolute name outside both the
// base and the working directory stays as it is, since a relative name
// for it would depend on where compile ran; so does stdin's. Two names
// of two files never come out the same.
func (n namer) name(name string) string {
	if n.wd == "" || name == stdinName {
		return name
	}
	path := filepath.FromSlash(name)
	abs := filepath.IsAbs(path)
	if !abs {
		path = filepath.Join(n.wd, path)
	}
	rel, err := filepath.Rel(n.base, path)
	if err != nil || abs && !within(n.base, path) && !within(n.wd, path) {
		return name
	}
	return filepath.ToSlash(rel)
}

// within reports whether path is dir or below it.
func within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// seal names c's files the way the binary keeps them, adds the root and
// the requirements, and checks the bundle exactly as the binary will load
// it: on its own, every policy in it, with the lint levels and the
// requirements of the check before, the ones whose policies it holds.
// With --policy, the root's files go in whole, so this is what catches a
// document beside the root that doesn't check, or doesn't invoke what's
// required of it. The diagnostics name the files as they were read.
func seal(w io.Writer, o *options, r *diagnose.Result, c *contents, root string) (*checked, humane.Error) {
	n := newNamer(r.Config)
	b, read := newBundle(c, r.Checks.Require, root, n)
	p, err := project.LoadFiles(project.FromBundle(b), o.kinds)
	if err != nil {
		return nil, err
	}
	errs, err := p.Diagnose(workspace.Checks{Lints: r.Checks.Lints, Require: requirements(r.Checks.Require, c.docs, n), Strict: r.Checks.Strict && c.scope == nil})
	if err != nil {
		return nil, err
	}
	resolved := p.Resolve(errs)
	src := func(file string) []byte { return p.SourceOf(n.name(file)) }
	diags := make(diag.ErrorList, len(resolved))
	for i, d := range resolved {
		named := *d
		if as, ok := read[d.File]; ok {
			named.File = as
		}
		diags[i] = &named
	}
	if failed(diags) {
		advice := diagnose.Advice(p, resolved, "each diagnostic above says where the problem is and how to fix it")
		if c.scope != nil {
			advice = append(advice, fmt.Sprintf("the files that hold %s and what it uses go into the binary whole, so the other documents in them are checked too: move those to their own files, or fix them", root))
		}
		return nil, fail(w, *o.output, diags, src, advice)
	}
	return &checked{warnings: diags, source: src, bundle: b, policies: len(p.Policies())}, nil
}

// newBundle returns the bundle of c's files, named by n, with the root
// and the requirements whose policies c holds, and the names the files
// were read under, by the names they have in the bundle. A file read
// under one name keeps one name.
func newBundle(c *contents, reqs []workspace.Requirement, root string, n namer) (*payload.Bundle, map[string]string) {
	b := c.files.Bundle()
	b.Root = root
	read := map[string]string{}
	for _, files := range [][]payload.File{b.Kinds, b.Paths, b.Trusted} {
		for i := range files {
			as := files[i].Name
			files[i].Name = n.name(as)
			read[files[i].Name] = as
		}
	}
	for _, r := range requirements(reqs, c.docs, n) {
		b.Require = append(b.Require, payload.Requirement{Policy: r.Policy, Trusted: r.Trusted, Roots: r.Roots})
	}
	return b, read
}

// requirements returns the requirements whose policies are among docs,
// their trusted paths named by n, as the bundle's files are.
func requirements(reqs []workspace.Requirement, docs map[string]bool, n namer) []workspace.Requirement {
	var out []workspace.Requirement
	for _, r := range reqs {
		if !docs[r.Policy] {
			continue
		}
		trusted := make([]string, len(r.Trusted))
		for i, t := range r.Trusted {
			trusted[i] = n.name(filepath.ToSlash(t))
		}
		r.Trusted = trusted
		out = append(out, r)
	}
	return out
}

// files counts the bundle's files, each once, however many of its lists
// hold it.
func files(b *payload.Bundle) int {
	names := map[string]bool{}
	for _, files := range [][]payload.File{b.Kinds, b.Paths, b.Trusted} {
		for _, f := range files {
			names[f.Name] = true
		}
	}
	return len(names)
}
