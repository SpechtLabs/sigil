package lsp

import (
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/spechtlabs/sigil/internal/workspace"
)

// marker marks the cursor in a test source; it's never valid Sigil.
const marker = "<|>"

// root is where the test workspace pretends to live. The names are
// absolute, as a Loader's must be.
const root = "/ws"

// memLoader loads a project from files held in memory, by absolute path,
// without a configuration file: every file is a path, as `sigil check`
// reads a directory with no sigil.yaml. It counts its loads, which a test
// reads while a server runs.
type memLoader struct {
	files      map[string][]byte
	err        error    // what every load reports as stopping the check
	undeclared bool     // no configuration file declares the project
	changed    []string // the files Changed was told about
	mu         sync.Mutex
	loads      int
}

// Root returns the workspace's root for every file, declared unless the
// loader says it isn't.
func (l *memLoader) Root(string, []string) Root { return Root{Path: root, Declared: !l.undeclared} }

// Changed records the files created or deleted on disk.
func (l *memLoader) Changed(paths []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.changed = append(l.changed, paths...)
}

// count returns how many loads there were.
func (l *memLoader) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loads
}

// Load loads every file, with the overlay over them, and diagnoses the
// project with the default lint levels.
func (l *memLoader) Load(_ string, overlay map[string][]byte) *Snapshot {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.loads++
	merged := map[string][]byte{}
	maps.Copy(merged, l.files)
	for name, src := range overlay {
		merged[filepath.ToSlash(name)] = src
	}
	names := make([]string, 0, len(merged))
	for name := range merged {
		names = append(names, name)
	}
	sort.Strings(names)
	files := make([]workspace.File, len(names))
	for i, name := range names {
		files[i] = workspace.File{Name: name, Source: merged[name]}
	}
	p := workspace.NewLoader(nil).Load(files, nil)
	errs, err := p.Diagnose(workspace.Checks{})
	if l.err != nil {
		return &Snapshot{Project: p, Err: l.err}
	}
	return &Snapshot{Project: p, Diagnostics: errs, Err: err}
}

// testWorkspace reads testdata/workspace into memory, under root.
func testWorkspace(t testing.TB) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join("testdata", "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, e := range entries {
		src, err := os.ReadFile(filepath.Join("testdata", "workspace", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		files[root+"/"+e.Name()] = src
	}
	return files
}

// viewOf loads the test workspace with file's source replaced by src,
// whose marker is the cursor, and returns a view of file and the cursor's
// offset. A src without a marker puts the cursor at its end.
func viewOf(t testing.TB, file, src string) (*view, int) {
	t.Helper()
	offset := strings.Index(src, marker)
	if offset < 0 {
		offset = len(src)
	}
	clean := []byte(strings.Replace(src, marker, "", 1))
	name := root + "/" + file
	l := &memLoader{files: testWorkspace(t)}
	snap := l.Load(root, map[string][]byte{name: clean})
	return newView(snap.Project, name, clean), offset
}
