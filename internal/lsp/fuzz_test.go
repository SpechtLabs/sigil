package lsp

import (
	"os"
	"path/filepath"
	"testing"
)

// maxFuzzSource is the longest source FuzzQueries asks about at every
// offset; a longer one is cut, so one input stays quick.
const maxFuzzSource = 2048

// FuzzQueries puts arbitrary source in the place of production.sigil in
// the test workspace, then completes and hovers at every offset of it, as
// an editor does while someone types. Nothing may panic, a completion
// replaces text that ends at the cursor, and a target and its definitions
// are spans of the files the project read.
func FuzzQueries(f *testing.F) {
	for _, pattern := range []string{"testdata/workspace/*.sigil", "../parser/testdata/*.sigil", "../../cmd/sigil/command/lsp/testdata/editor/*/*.sigil"} {
		files, err := filepath.Glob(pattern)
		if err != nil {
			f.Fatal(err)
		}
		for _, file := range files {
			src, err := os.ReadFile(file)
			if err != nil {
				f.Fatal(err)
			}
			f.Add(src)
		}
	}
	f.Add([]byte(policy))
	f.Add([]byte(head + "when any c in changes: c.\nassert(\"x\", outcome.review."))
	f.Add([]byte(head + "use deploy.common.{cleared, \nparam p: list<map<string, \nwhen x == "))
	files := testWorkspace(f)
	f.Fuzz(func(t *testing.T, src []byte) {
		if len(src) > maxFuzzSource {
			src = src[:maxFuzzSource]
		}
		l := &memLoader{files: files}
		name := root + "/production.sigil"
		snap := l.Load(root, map[string][]byte{name: src})
		v := newView(snap.Project, name, src)
		for offset := 0; offset <= len(src); offset++ {
			if _, from, to := v.complete(offset); from < 0 || from > offset || to < offset || to > len(src) {
				t.Fatalf("a completion at %d replaces %d to %d", offset, from, to)
			}
			target := v.targetAt(offset)
			if target == nil {
				continue
			}
			if target.from < 0 || target.from > target.to || target.to > len(src) {
				t.Fatalf("a target at %d spans %d to %d of %d bytes", offset, target.from, target.to, len(src))
			}
			for _, d := range target.defs {
				if size := len(snap.Project.SourceOf(d.file)); d.from < 0 || d.from > d.to || d.to > size {
					t.Fatalf("a definition spans %d to %d of %s, %d bytes", d.from, d.to, d.file, size)
				}
			}
		}
	})
}
