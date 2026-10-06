package lsp

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// elsewhere is a policy with two errors, of another name than the
// workspace's.
const elsewhere = "policy other.p: DeployApproval@2\n\nwhen nope {\n  deny(reason: nope)\n}\n"

// TestUndeclaredProjects checks a project no configuration file declares:
// the server publishes the diagnostics of the open documents only, not
// those of the other files it read.
func TestUndeclaredProjects(t *testing.T) {
	files := testWorkspace(t)
	files[root+"/broken.sigil"] = []byte(elsewhere)
	s := start(t, &memLoader{files: files, undeclared: true})
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", fixed))
	for _, n := range s.settle("production.sigil", 1) {
		if strings.Contains(string(n.Params), "broken.sigil") {
			t.Errorf("published the diagnostics of a file that isn't open: %s", n.Params)
		}
	}
	s.shutdown()
}

// TestNestedProjects checks a declared project with another one below it,
// a directory with a configuration file of its own: each file's
// diagnostics come from the project nearest it, never from both, and
// dropping one project doesn't clear the other's.
func TestNestedProjects(t *testing.T) {
	files := testWorkspace(t)
	files[root+"/child/inner.sigil"] = []byte(elsewhere)
	s := start(t, &nestedLoader{memLoader{files: files}})
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", fixed))
	for _, n := range s.settle("production.sigil", 1) {
		if strings.Contains(string(n.Params), "child/inner.sigil") {
			t.Errorf("the outer project published the inner one's file: %s", n.Params)
		}
	}
	s.notify(protocol.MethodDidOpen, s.opening("child/inner.sigil", elsewhere))
	if got := s.settle("child/inner.sigil", 1); len(got) != 1 {
		t.Errorf("opening the inner file published %d lists, want its own", len(got))
	}
	s.notify(protocol.MethodDidClose, map[string]any{"textDocument": map[string]any{"uri": s.uri("production.sigil")}})
	s.sync("child/inner.sigil")
	if got := s.published(s.uri("child/inner.sigil")); len(got.Diagnostics) != 2 {
		t.Errorf("closing the outer project cleared the inner file's diagnostics: %v", render(got.Diagnostics))
	}
	s.shutdown()
}

// TestLoadsDontBlock checks that a load that takes long keeps nothing
// waiting but the requests about its project: the server answers
// shutdown, and exits, while it runs, and publishes nothing after the
// shutdown when it finishes.
func TestLoadsDontBlock(t *testing.T) {
	l := &slowLoader{files: testWorkspace(t), gate: make(chan struct{})}
	s := start(t, l)
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", broken))
	s.wantResult(s.call(protocol.MethodShutdown, nil), "null")
	l.release()
	time.Sleep(20 * time.Millisecond)
	s.exit()
	for _, n := range s.notes {
		if n.Method == protocol.MethodPublishDiagnostic {
			t.Errorf("published after shutdown: %s", n.Params)
		}
	}
	if err := <-s.done; err != nil {
		t.Errorf("Serve() = %v", err)
	}
}

// TestFirstLoadWaits checks that a request about a document whose
// project hasn't finished its first load waits for it, rather than
// answering from nothing, while other requests go on.
func TestFirstLoadWaits(t *testing.T) {
	l := &slowLoader{files: testWorkspace(t), gate: make(chan struct{})}
	s := start(t, l)
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", "policy p: DeployApproval@2\n\nwhen serv"))
	s.id++
	waiting := json.RawMessage(`"waiting"`)
	params, err := json.Marshal(s.at("production.sigil", 2, 9))
	if err != nil {
		t.Fatal(err)
	}
	s.send(&jsonrpc.Message{ID: waiting, Method: protocol.MethodCompletion, Params: params})
	s.wantError(s.call("textDocument/codeLens", map[string]any{}), jsonrpc.MethodNotFound)
	l.release()
	for {
		m := s.next()
		if s.keep(m) {
			continue
		}
		var list protocol.CompletionList
		if string(m.ID) != string(waiting) || json.Unmarshal(m.Result, &list) != nil || len(list.Items) != 1 || list.Items[0].Label != "service" {
			t.Errorf("answer = %s %s, want the completion of service", m.ID, m.Result)
		}
		break
	}
	s.shutdown()
}

// TestWatchers checks that the server asks the client to watch the
// project's files when the client lets it, and not otherwise.
func TestWatchers(t *testing.T) {
	tests := []struct {
		name    string
		dynamic bool
	}{
		{name: "dynamic registration", dynamic: true},
		{name: "none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := start(t, &memLoader{files: testWorkspace(t)})
			caps := map[string]any{"workspace": map[string]any{"didChangeWatchedFiles": map[string]any{"dynamicRegistration": tt.dynamic}}}
			s.wantNoError(s.call(protocol.MethodInitialize, map[string]any{"capabilities": caps}))
			s.notify(protocol.MethodInitialized, map[string]any{})
			s.shutdown()
			if !tt.dynamic {
				if len(s.asks) != 0 {
					t.Errorf("asked %v", s.asks)
				}
				return
			}
			if len(s.asks) != 1 || s.asks[0].Method != protocol.MethodRegisterCapability {
				t.Fatalf("asked %v, want one registration", s.asks)
			}
			var p protocol.RegistrationParams
			if err := json.Unmarshal(s.asks[0].Params, &p); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(p.Registrations[0].RegisterOptions)
			want := `{"watchers":[{"globPattern":"**/*.sigil"},{"globPattern":"**/sigil.yaml"},{"globPattern":"**/sigil.json"},{"globPattern":"**/sigil.toml"},{"globPattern":"**/.sigil.yaml"},{"globPattern":"**/.sigil.json"},{"globPattern":"**/.sigil.toml"}]}`
			if p.Registrations[0].Method != protocol.MethodDidChangeWatched || string(raw) != want {
				t.Errorf("registered %s for %s", raw, p.Registrations[0].Method)
			}
		})
	}
}

// TestRootsMove checks that a configuration file created on disk moves
// an open document to the project it declares: the server tells the
// loader which files were created or deleted, finds the root again, and
// the new project's diagnostics replace the old one's without an empty
// list in between.
func TestRootsMove(t *testing.T) {
	l := &movingLoader{files: testWorkspace(t), at: root}
	s := start(t, l)
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", broken))
	s.settle("production.sigil", 1)
	l.move(root + "/elsewhere")
	s.notify(protocol.MethodDidChangeWatched, map[string]any{"changes": []any{
		map[string]any{"uri": "file:///ws/elsewhere/sigil.yaml", "type": 1},
		map[string]any{"uri": "file:///ws/notes.txt", "type": 2},
	}})
	noFlicker(t, s, "production.sigil")
	if got := l.changes(); len(got) != 1 || got[0] != "/ws/elsewhere/sigil.yaml" {
		t.Errorf("told the loader about %v, want the configuration created", got)
	}
	s.shutdown()
}

// noFlicker waits for the next diagnostics of the open document file,
// and checks that none of the lists before them was empty: a document
// whose project changes keeps its problems until the new project
// replaces them.
func noFlicker(t *testing.T, s *session, file string) {
	t.Helper()
	notes := s.settle(file, 1)
	for _, n := range notes {
		var p protocol.PublishDiagnosticsParams
		if n.Method == protocol.MethodPublishDiagnostic && json.Unmarshal(n.Params, &p) == nil && p.URI == s.uri(file) && len(p.Diagnostics) == 0 {
			t.Errorf("published an empty list for %s while its project moved", file)
		}
	}
}

// TestCancellationsAreBounded checks that cancellations of requests that
// were answered already don't pile up.
func TestCancellationsAreBounded(t *testing.T) {
	c := &cancellations{ids: map[string]bool{}}
	for i := range 3 * maxCanceled {
		c.cancel(json.RawMessage(`{"id":` + strings.Repeat("1", 1) + string(rune('0'+i%10)) + `}`))
		c.cancel(json.RawMessage(`{"id":"` + strings.Repeat("x", i) + `"}`))
	}
	if len(c.ids) > maxCanceled {
		t.Errorf("kept %d cancellations, want at most %d", len(c.ids), maxCanceled)
	}
}

// nestedLoader roots the files below root/child at that directory, as a
// configuration file there would, and everything else at root.
type nestedLoader struct{ memLoader }

// Root returns root/child for the files below it, and root otherwise.
func (l *nestedLoader) Root(path string, _ []string) Root {
	if strings.HasPrefix(path, root+"/child/") {
		return Root{Path: root + "/child", Declared: true}
	}
	return Root{Path: root, Declared: true}
}

// slowLoader loads only once the test releases it.
type slowLoader struct {
	gate chan struct{}
	memLoader
	once sync.Once
}

// Load waits for the release, then loads.
func (l *slowLoader) Load(r string, overlay map[string][]byte) *Snapshot {
	<-l.gate
	return l.memLoader.Load(r, overlay)
}

// release lets every load through.
func (l *slowLoader) release() { l.once.Do(func() { close(l.gate) }) }

// movingLoader roots every file where the test says.
type movingLoader struct {
	at string
	memLoader
	mu sync.Mutex
}

// Root returns where the files are rooted now.
func (l *movingLoader) Root(string, []string) Root {
	l.mu.Lock()
	defer l.mu.Unlock()
	return Root{Path: l.at, Declared: true}
}

// changes returns the files the loader was told about.
func (l *movingLoader) changes() []string {
	l.memLoader.mu.Lock()
	defer l.memLoader.mu.Unlock()
	return l.changed
}

// move roots the files at path from now on.
func (l *movingLoader) move(path string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.at = path
}

// TestRootNotes checks that the note a root comes with, such as a folder
// too large to read whole, is shown once, however many documents it
// comes up for.
func TestRootNotes(t *testing.T) {
	s := start(t, &notingLoader{memLoader{files: testWorkspace(t)}})
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", fixed))
	s.notify(protocol.MethodDidOpen, s.opening("common.sigil", "module deploy.common: DeployApproval@2\n"))
	s.settle("common.sigil", 1)
	shown := 0
	for _, n := range s.notes {
		if n.Method == protocol.MethodShowMessage && strings.Contains(string(n.Params), "too large") {
			shown++
		}
	}
	if shown != 1 {
		t.Errorf("showed the note %d times, want once", shown)
	}
	s.shutdown()
}

// notingLoader roots every file at root with a note.
type notingLoader struct{ memLoader }

// Root returns root, with a note.
func (*notingLoader) Root(string, []string) Root {
	return Root{Path: root, Note: "the folder is too large"}
}

// TestStaleLoads checks that a load started for a project that was
// dropped and created again at the same root, as closing a project's last
// document and opening it again does, never overwrites what the newer
// project's own load found, however late it finishes.
func TestStaleLoads(t *testing.T) {
	l := &stagedLoader{files: testWorkspace(t), gate: make(chan struct{}), returned: make(chan struct{})}
	s := start(t, l)
	s.initialize()
	stale := strings.Repeat("// padding\n", 50) + "policy payments.production: DeployApproval@2\n\nlet stale = nope\n"
	fresh := "policy payments.production: DeployApproval@2\n\nlet fresh = nope\n"
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", stale))
	s.notify(protocol.MethodDidClose, map[string]any{"textDocument": map[string]any{"uri": s.uri("production.sigil")}})
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", fresh))
	s.settle("production.sigil", 1)
	close(l.gate)
	<-l.returned
	time.Sleep(50 * time.Millisecond) // let the stale result reach the message loop
	var hover protocol.Hover
	s.decode(s.call(protocol.MethodHover, s.at("production.sigil", 2, 6)), &hover)
	if !strings.Contains(hover.Contents.Value, "let fresh") {
		t.Errorf("hover = %q, want the fresh buffer's let", hover.Contents.Value)
	}
	got := s.published(s.uri("production.sigil"))
	if len(got.Diagnostics) == 0 || got.Diagnostics[0].Range.Start.Line != 2 {
		t.Errorf("the last diagnostics are %v, want the fresh buffer's, on line 2", render(got.Diagnostics))
	}
	s.shutdown()
}

// stagedLoader holds its first load until the test opens the gate, and
// lets every later one through at once.
type stagedLoader struct {
	gate     chan struct{}
	returned chan struct{} // closed when the first load has returned
	memLoader
	calls int
	mu    sync.Mutex
}

// Load holds the first load, then loads.
func (l *stagedLoader) Load(r string, overlay map[string][]byte) *Snapshot {
	l.mu.Lock()
	l.calls++
	first := l.calls == 1
	l.mu.Unlock()
	if !first {
		return l.memLoader.Load(r, overlay)
	}
	<-l.gate
	defer close(l.returned)
	return l.memLoader.Load(r, overlay)
}
