package policy

import (
	"io/fs"
	"reflect"
	"slices"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/bundle"
)

// Load reads every `.sigil` file in fsys into one bundle, indexes the
// documents by the names in their headers, and compiles the policy
// called name as the root. A file may hold several documents separated
// by `---`.
//
// Files are containers: one document per file, one per team or everything
// in one file all resolve the same way, and [embed.FS], [os.DirFS], [MapFS]
// and a mounted Kubernetes ConfigMap all work. Load reads every directory
// of fsys, in lexical order. It skips entries whose names start with `.`, and decides what's a
// file with [io/fs.Stat], which follows symbolic links, so a ConfigMap's
// symlinked keys load once each and kubelet's `..data` directory is
// skipped.
//
// Every document is checked, so a broken one fails the load even when the
// root never uses it. A name defined twice is an error, and so is a
// document for another kind. A kind document with the kind's name must
// match [Kind.Schema]; kind documents for other kinds are ignored. The root
// must be a policy, not a module.
//
// The error is a [*CompileError] with every problem, its position and a
// fix hint, or an error from reading fsys.
func (k *Kind[In]) Load(fsys fs.FS, name string, opts ...LoadOption) (*Policy[In], error) {
	o := &loadOptions{params: Params{}}
	for _, opt := range opts {
		opt.apply(o)
	}
	b := bundle.New(k.kind)
	if err := k.trust(b, o); err != nil {
		return nil, err
	}
	if err := b.Load(fsys); err != nil {
		return nil, err
	}
	return k.compile(b, name, o)
}

// Compile compiles the policy called name from src, a one-file bundle
// that may hold several documents separated by `---`. It is [Kind.Load]
// for a single source string, and suits tests and policies stored in a
// database. Every document is checked against the kind, so a broken
// document fails the compile even when the root never uses it, and a kind
// document with the kind's name must match [Kind.Schema].
//
// The error is a [*CompileError] listing every problem with a position and
// a fix hint. The source has no file name, so positions in it carry only
// the line, the column and the document's name.
func (k *Kind[In]) Compile(src, name string, opts ...LoadOption) (*Policy[In], error) {
	o := &loadOptions{params: Params{}}
	for _, opt := range opts {
		opt.apply(o)
	}
	b := bundle.New(k.kind)
	if err := k.trust(b, o); err != nil {
		return nil, err
	}
	b.Add("", []byte(src))
	return k.compile(b, name, o)
}

// trust loads the sources From named into a trusted bundle. Several
// requirements may share a source; each source is read once.
func (k *Kind[In]) trust(b *bundle.Bundle, o *loadOptions) error {
	var trusted *bundle.Bundle
	var seen []fs.FS
	for _, r := range o.requires {
		if r.from == nil || slices.ContainsFunc(seen, func(prev fs.FS) bool { return sameSource(prev, r.from) }) {
			continue
		}
		seen = append(seen, r.from)
		if trusted == nil {
			trusted = bundle.New(k.kind)
		}
		if err := trusted.Load(r.from); err != nil {
			return err
		}
	}
	if trusted != nil {
		b.Trust(trusted)
	}
	return nil
}

// Filesystems can be map-backed values, such as fstest.MapFS. They can't
// be interface map keys. Compare those by map identity, and comparable
// implementations by value. Other value implementations have no identity;
// callers can pass a pointer to share one across several requirements.
func sameSource(a, b fs.FS) bool {
	x, y := reflect.ValueOf(a), reflect.ValueOf(b)
	if x.Type() != y.Type() {
		return false
	}
	if x.Comparable() && y.Comparable() {
		return a == b
	}
	return x.Kind() == reflect.Map && x.Pointer() == y.Pointer()
}

// MapFS turns a map of file names to contents, such as the data of a
// Kubernetes ConfigMap read through the API, into an [io/fs.FS] that
// [Kind.Load] reads. Keys need the `.sigil` extension to be loaded, like
// any other file. MapFS copies the contents, so later changes to files
// don't affect the result.
func MapFS(files map[string]string) fs.FS {
	m := fstest.MapFS{}
	for name, src := range files {
		m[name] = &fstest.MapFile{Data: []byte(src)}
	}
	return m
}
