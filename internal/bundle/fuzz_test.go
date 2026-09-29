package bundle_test

import (
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/lint"
)

func FuzzBundle(f *testing.F) {
	k, errs := check.LoadKind("kind.sigil", []byte(kindSrc))
	if errs != nil {
		f.Fatal(errs)
	}
	for _, src := range []string{library, library + broken, "policy good: K@1\nallow(ok)", "policy good: K@1\nuse other\nother()\n---\npolicy other: K@1\nuse good\ngood()", kindSrc} {
		f.Add(src, "good")
	}
	f.Add(kindSrc, "K")
	f.Fuzz(func(t *testing.T, src, root string) {
		b := bundle.New(k)
		b.Add("fuzz.sigil", []byte(src))
		_, errs := b.Compile(root, bundle.Options{Static: true})
		first := b.Render(errs)
		_, again := b.Compile(root, bundle.Options{Static: true})
		if first != b.Render(again) {
			t.Fatal("compiling twice changed diagnostics")
		}
		a := lint.Run(b, lint.Options{Kind: k})
		if !reflect.DeepEqual(a, lint.Run(b, lint.Options{Kind: k})) { //nolint:govet // deepequalerrors: diagnostics compare field by field, and a lint finding has no Cause
			t.Fatal("linting is not deterministic")
		}
		loaded := bundle.New(k)
		if err := loaded.Load(fstest.MapFS{"fuzz.sigil": &fstest.MapFile{Data: []byte(src)}}); err != nil {
			t.Fatal(err)
		}
		_, fromFS := loaded.Compile(root, bundle.Options{Static: true})
		if first != loaded.Render(fromFS) {
			t.Fatal("filesystem and source compilation disagree")
		}
	})
}
