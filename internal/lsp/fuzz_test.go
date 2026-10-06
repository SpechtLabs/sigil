package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// maxFuzzSource is the longest source FuzzQueries asks about at every
// offset; a longer one is cut, so one input stays quick.
const maxFuzzSource = 2048

// FuzzQueries puts arbitrary source in the place of production.sigil in
// the test workspace, then completes, hovers and asks for signature help
// at every offset of it, as an editor does while someone types, and for
// its inlay hints and quick fixes. Nothing may panic, a completion
// replaces text that ends at the cursor, a target and its definitions
// are spans of the files the project read, a signature's parameters are
// spans of its label, and a fix replaces what it says it does.
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
		checkFixes(t, v, snap, name, src)
		for offset := 0; offset <= len(src); offset++ {
			if _, from, to := v.complete(offset); from < 0 || from > offset || to < offset || to > len(src) {
				t.Fatalf("a completion at %d replaces %d to %d", offset, from, to)
			}
			checkSignature(t, v.signatureAt(offset))
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

// dataFixes returns the fixes data holds, none for nil.
func dataFixes(data *protocol.DiagnosticData) []protocol.Fix {
	if data == nil {
		return nil
	}
	return data.Fixes
}

// checkFixes checks the inlay hints of src, the source of the file called
// name in snap, and its quick fixes: every hint is in src, and every fix
// replaces what it says it does.
func checkFixes(t *testing.T, v *view, snap *Snapshot, name string, src []byte) {
	t.Helper()
	for _, h := range v.hints(0, len(src)) {
		if h.offset < 0 || h.offset > len(src) {
			t.Fatalf("a hint at %d of %d bytes", h.offset, len(src))
		}
	}
	lines := newLines(src, "utf-8")
	var k *kind.Kind
	if known := v.kindNamed("DeployApproval"); known != nil {
		k = known.Model
	}
	for _, e := range snap.Diagnostics {
		if e.File != name {
			continue
		}
		for _, fix := range dataFixes(fixesOf(e, src, k, lines)) {
			if from, to := lines.offset(fix.Edit.Range.Start), lines.offset(fix.Edit.Range.End); string(src[from:to]) != fix.Replaces {
				t.Fatalf("%s replaces %q, not %q", fix.Title, src[from:to], fix.Replaces)
			}
		}
	}
}

// checkSignature checks a signature, if there is one: its parameters are
// spans of its label, and the active one is one of them.
func checkSignature(t *testing.T, sig *signature) {
	t.Helper()
	if sig == nil {
		return
	}
	for _, p := range sig.params {
		if p.from < 0 || p.from > p.to || p.to > len(sig.label) {
			t.Fatalf("a parameter spans %d to %d of %q", p.from, p.to, sig.label)
		}
	}
	if sig.active >= len(sig.params) {
		t.Fatalf("the active parameter is %d of %d", sig.active, len(sig.params))
	}
}
