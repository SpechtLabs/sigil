// Package project loads what the policy commands work on from the files
// the command line names: every document, read and parsed once, and
// grouped by the kind each one's header names.
//
// [Load] reads the paths from disk and hands them to package workspace,
// which finds the kinds among the linked ones, the --kind files and the
// inputs themselves, and returns a [Project] with one [Group] per kind.
// It's [Read] and [LoadFiles] in one: Read reads the files, and
// LoadFiles parses and groups them, so files that come from somewhere
// else, such as the bundle compiled into the binary ([FromBundle]), load
// the same way. [Expand] holds the path rules every command shares, and
// [Match] and [Root] pick the policies a command works on, by name or by
// pattern. The types are workspace's, which the WebAssembly module loads
// from virtual files instead.
package project

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/workspace"
)

// stdinName names what a "-" path reads, in diagnostics.
const stdinName = "<stdin>"

type (
	// Kind is the contract a command checks and evaluates against.
	Kind = workspace.Kind
	// Linked is a kind a host linked into its own sigil binary, with
	// github.com/spechtlabs/sigil/pkg/cli.WithKind.
	Linked = workspace.Linked
	// Project is every document a command read, grouped by kind.
	Project = workspace.Project
	// Group is one kind's share of a project.
	Group = workspace.Group
	// Scope is what one command covers: every document, or some policies
	// and every document they use.
	Scope = workspace.Scope
)

// Sources are what a command reads: the paths it was given, the trusted
// paths, and kind files from outside the paths.
type Sources struct {
	Stdin   io.Reader // what a "-" path reads
	Paths   []string  // files, directories, or "-" for stdin, as [Expand] reads them
	Trusted []string  // read into each kind's trusted bundle, as policy.From does
	Kinds   []string  // kind files the paths don't hold, such as --kind names
	// Overlay holds sources that replace a file's on disk, by the file's
	// path, such as the unsaved buffers of an editor. A `.sigil` file only
	// the overlay holds counts as below a directory among the paths, the
	// way it will once it's saved.
	Overlay map[string][]byte
}

// Files are a project's files as [Read] read them, before parsing. A file
// named more than one way is the same [workspace.File] each time, with the
// same ID, so it's parsed once, and a file among the trusted paths isn't
// among Paths.
type Files struct {
	Kinds   []workspace.File // kind files named outside the paths, each once
	Paths   []workspace.File // the documents, in the order [Expand] lists them
	Trusted []workspace.File // read into each kind's trusted bundle
}

// reader reads each file once, however many ways it's named.
type reader struct {
	stdin   io.Reader
	read    map[string]workspace.File // by identity
	overlay map[string][]byte         // the overlay's sources, by identity
}

// Load reads the sources and groups their documents by kind: it's
// [Read], then [LoadFiles].
//
// Parse errors, kind documents that don't check or don't agree, documents
// naming a kind nobody provides and names defined twice are diagnostics,
// for [Project.Errors]; only a path that can't be read, or a kind file that
// doesn't match the kind linked into this binary, fails Load.
func Load(s Sources, linked []Linked) (*Project, humane.Error) {
	f, err := Read(s)
	if err != nil {
		return nil, err
	}
	return LoadFiles(f, linked)
}

// Read reads the sources' files: the kind files in s.Kinds, then the
// paths, then the trusted paths. The paths are expanded by [Expand]'s
// rules, each directory contributing the `.sigil` files below it, and a
// file under a trusted path is read as trusted only. Only a path that
// can't be read fails Read; it doesn't parse anything.
func Read(s Sources) (*Files, humane.Error) {
	r := &reader{stdin: s.Stdin, read: map[string]workspace.File{}, overlay: map[string][]byte{}}
	for name, src := range s.Overlay {
		r.overlay[identity(name)] = src
	}
	f := &Files{}
	seen := map[string]bool{}
	for _, k := range s.Kinds {
		kf, err := r.kindFile(k)
		if err != nil {
			return nil, err
		}
		if !seen[kf.ID] {
			seen[kf.ID] = true
			f.Kinds = append(f.Kinds, kf)
		}
	}
	var err humane.Error
	if f.Paths, f.Trusted, err = r.sources(s); err != nil {
		return nil, err
	}
	return f, nil
}

// LoadFiles parses the files and groups their documents by kind, as
// [workspace.Loader] does: the kind files first, then the paths, then the
// trusted paths. It fails only on a kind file that doesn't match the kind
// linked into this binary, or that holds no kind document; everything
// else is a diagnostic, as for [Load].
func LoadFiles(f *Files, linked []Linked) (*Project, humane.Error) {
	l := workspace.NewLoader(linked)
	for _, k := range f.Kinds {
		if err := l.Kind(k); err != nil {
			return nil, err
		}
	}
	return l.Load(f.Paths, f.Trusted), nil
}

// Match returns the policies matching any of the patterns, in the order
// given, as [workspace.Match] does.
func Match(policies, patterns []string) ([]string, humane.Error) {
	return workspace.Match(policies, patterns)
}

// Root picks the one policy eval evaluates, as [workspace.Root] does.
func Root(policies []string, name string) (string, humane.Error) {
	return workspace.Root(policies, name)
}

// sources expands the paths and the trusted paths and reads their files,
// the paths first. A file among both is read as trusted only. The
// overlay's new files count below both.
func (r *reader) sources(s Sources) (regular, trusted []workspace.File, err humane.Error) {
	tnames, err := Expand(s.Trusted, IsSigil)
	if err != nil {
		return nil, nil, err
	}
	tnames = append(tnames, unsaved(s.Overlay, s.Trusted, tnames)...)
	rnames, err := Expand(s.Paths, IsSigil)
	if err != nil {
		return nil, nil, err
	}
	rnames = append(rnames, unsaved(s.Overlay, s.Paths, rnames)...)
	if regular, err = r.readAll(without(rnames, tnames)); err != nil {
		return nil, nil, err
	}
	if trusted, err = r.readAll(tnames); err != nil {
		return nil, nil, err
	}
	return regular, trusted, nil
}

// kindFile reads a kind file named outside the paths.
func (r *reader) kindFile(name string) (workspace.File, humane.Error) {
	name = clean(name)
	if f, ok := r.read[identity(name)]; ok {
		return f, nil
	}
	if src, ok := r.overlay[identity(name)]; ok {
		return r.keep(name, src), nil
	}
	src, err := os.ReadFile(name) //nolint:gosec // the path comes from the command line, which is the point
	if err != nil {
		return workspace.File{}, humane.Wrap(err, "the kind file couldn't be read", "pass the exported kind file with --kind")
	}
	return r.keep(name, src), nil
}

// readAll reads the files, "-" from stdin, in order.
func (r *reader) readAll(names []string) ([]workspace.File, humane.Error) {
	out := make([]workspace.File, 0, len(names))
	for _, name := range names {
		f, err := r.file(name)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

// file reads one file, or returns the one already read under that name.
func (r *reader) file(name string) (workspace.File, humane.Error) {
	if name == "-" {
		name = stdinName
	}
	if f, ok := r.read[identity(name)]; ok {
		return f, nil
	}
	var src []byte
	var err error
	if over, ok := r.overlay[identity(name)]; ok {
		return r.keep(name, over), nil
	}
	if name == stdinName {
		src, err = io.ReadAll(r.stdin)
		if err != nil {
			return workspace.File{}, humane.Wrap(err, "stdin couldn't be read", "pipe a policy bundle in, or name files instead of `-`")
		}
	} else if src, err = os.ReadFile(name); err != nil { //nolint:gosec // the path comes from the command line, which is the point
		return workspace.File{}, humane.Wrap(err, name+" couldn't be read", "check the file's permissions")
	}
	return r.keep(name, src), nil
}

// unsaved returns the `.sigil` files of the overlay that aren't on disk,
// below one of the directories among paths and not among have, sorted,
// so an editor's new buffer is checked with the files around it before
// it's saved. A file whose name, or the name of a directory between it
// and the path, starts with `.` is left out, as [Expand] leaves it out.
func unsaved(overlay map[string][]byte, paths, have []string) []string {
	held := map[string]bool{}
	for _, name := range have {
		held[identity(name)] = true
	}
	var out []string
	for name := range overlay {
		if !IsSigil(name) || held[identity(name)] {
			continue
		}
		if _, err := os.Stat(name); err == nil {
			continue
		}
		for _, p := range paths {
			if p != "-" && below(p, name) {
				out = append(out, clean(name))
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// below reports whether name is a file below the directory dir that a
// walk of dir would reach: no part of the path between them starts with
// `.`.
func below(dir, name string) bool {
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return false
	}
	absName, err := filepath.Abs(name)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absDir, absName)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	for part := range strings.SplitSeq(filepath.ToSlash(rel), "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

// keep records a file read, by its identity, so it's never read again.
func (r *reader) keep(name string, src []byte) workspace.File {
	f := workspace.File{Name: name, ID: identity(name), Source: src}
	r.read[f.ID] = f
	return f
}
