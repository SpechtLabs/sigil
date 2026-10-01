package policy

import (
	"fmt"
	"io/fs"
	"reflect"
	"slices"
	"testing/fstest"

	"github.com/sierrasoftworks/humane-errors-go"

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

// trust loads the sources Trusted and From named into a trusted bundle,
// Trusted's first. Several options may share a source; each source is
// read once. A nil source, or one that holds no policy or module, is an
// error: it would protect nothing, and the likely cause is an ignored
// error or a wrong directory.
func (k *Kind[In]) trust(b *bundle.Bundle, o *loadOptions) error {
	type source struct {
		fsys  fs.FS
		named string // how the options name it, for the error
	}
	var sources []source
	for i, fsys := range o.trusted {
		named := "passed to Trusted"
		if len(o.trusted) > 1 {
			named = fmt.Sprintf("of Trusted option %d of %d", i+1, len(o.trusted))
		}
		sources = append(sources, source{fsys, named})
	}
	for _, r := range o.requires {
		if r.trusting {
			sources = append(sources, source{r.from, "From names for " + r.name})
		}
	}
	var trusted *bundle.Bundle
	var seen []fs.FS
	for _, src := range sources {
		if src.fsys == nil {
			return humane.New("the trusted source "+src.named+" is nil",
				"pass the fs.FS that holds the trusted documents; when building it returned an error, handle that error rather than passing nil")
		}
		if slices.ContainsFunc(seen, func(prev fs.FS) bool { return sameSource(prev, src.fsys) }) {
			continue
		}
		seen = append(seen, src.fsys)
		if trusted == nil {
			trusted = bundle.New(k.kind)
		}
		before := trusted.Read()
		if err := trusted.Load(src.fsys); err != nil {
			return humane.Wrap(err, "the trusted source "+src.named+" couldn't be read", "check the directory passed to fs.Sub, or the //go:embed pattern")
		}
		if trusted.Read() == before {
			return humane.New("the trusted source "+src.named+" holds no policies or modules",
				"a trusted source is read like the bundle: every .sigil file in every directory, skipping names that start with `.`; check the directory passed to fs.Sub, or the //go:embed pattern")
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
