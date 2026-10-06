package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	server "github.com/spechtlabs/sigil/internal/lsp"
	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

var update = flag.Bool("update", false, "rewrite the golden files under testdata")

// client drives a server over in-memory pipes, the way an editor drives
// `sigil lsp`, and keeps a transcript of what it sent and what it got.
type client struct {
	t        *testing.T
	w        *jsonrpc.Writer
	in       chan *jsonrpc.Message // what the server sent, in order
	root     string                // the workspace's directory
	rootURI  string
	versions map[string]int32
	shown    map[string][]protocol.Diagnostic // the diagnostics published last, by URI, for a code action to send back
	log      strings.Builder
	id       int
}

// step is one thing the session does: a notification, or a request whose
// answer goes into the transcript.
type step struct {
	say    string            // what the transcript calls the step
	doc    string            // the document a notification is about, which the request after it asks about
	method string            // the method
	params func(*client) any // its params
	notify bool              // a notification; the server's notifications before the next answer are kept
}

// TestSession runs a whole session against a workspace that holds only
// an exported kind file and the policies written against it, as a policy
// repository does, with no host code: initialize, open a policy, edit it
// into a broken state and get its diagnostics, complete fields, reasons
// and payload keys, hover, go to definitions in other files, fix and
// format it, close it and shut down. The transcript is
// testdata/session.golden; run with -update to accept a change.
func TestSession(t *testing.T) {
	c := start(t)
	prod := "payments/production.sigil"
	original := c.read(prod)
	broken := original + `
let unused = 1

when service.
`
	editing := original + `
when service.tier == critical {
  deny(reason: )
  review(reason: service_owner, )
}
`
	misspelled := original + `
when servce.tier == critical {
  deny(reason: not_eligible)
}
`
	unformatted := strings.Replace(original, "guardrails(min_soak: 4h)", "guardrails(  min_soak :4h )", 1) + "\nlet unused = 1\n"
	steps := []step{
		{say: "initialize", method: protocol.MethodInitialize, params: func(c *client) any {
			return map[string]any{
				"processId":        nil,
				"rootUri":          c.rootURI,
				"workspaceFolders": []any{map[string]any{"uri": c.rootURI, "name": "editor"}},
				"capabilities": map[string]any{
					"general":      map[string]any{"positionEncodings": []string{"utf-16"}},
					"textDocument": map[string]any{"completion": map[string]any{"completionItem": map[string]any{"snippetSupport": true}}},
				},
			}
		}},
		{say: "initialized", method: protocol.MethodInitialized, notify: true, params: func(*client) any { return map[string]any{} }},
		{say: "open " + prod, doc: prod, method: protocol.MethodDidOpen, notify: true, params: func(c *client) any { return c.open(prod, original) }},
		{say: "edit " + prod + " so it doesn't parse", doc: prod, method: protocol.MethodDidChange, notify: true, params: func(c *client) any { return c.change(prod, broken) }},
		{say: "complete after `service.`", method: protocol.MethodCompletion, params: func(c *client) any { return c.position(prod, broken, "when service.") }},
		{say: "complete the inputs after `when `", method: protocol.MethodCompletion, params: func(c *client) any { return c.position(prod, broken, "\nwhen ") }},
		{say: "edit " + prod + " to construct decisions", doc: prod, method: protocol.MethodDidChange, notify: true, params: func(c *client) any { return c.change(prod, editing) }},
		{say: "complete a reason", method: protocol.MethodCompletion, params: func(c *client) any { return c.position(prod, editing, "deny(reason: ") }},
		{say: "complete a payload key", method: protocol.MethodCompletion, params: func(c *client) any { return c.position(prod, editing, "service_owner, ") }},
		{say: "complete the tier's values", method: protocol.MethodCompletion, params: func(c *client) any { return c.position(prod, editing, "service.tier == ") }},
		{say: "signature help in the review constructor", method: protocol.MethodSignatureHelp, params: func(c *client) any { return c.position(prod, editing, "service_owner, ") }},
		{say: "complete the decisions to construct", method: protocol.MethodCompletion, params: func(c *client) any { return c.position(prod, editing, "service_owner, )\n") }},
		{say: "hover over the review constructor", method: protocol.MethodHover, params: func(c *client) any { return c.position(prod, editing, "  revi") }},
		{say: "hover over the tier field", method: protocol.MethodHover, params: func(c *client) any { return c.position(prod, editing, "service.ti") }},
		{say: "go to the imported let cleared", method: protocol.MethodDefinition, params: func(c *client) any { return c.position(prod, editing, "when clea") }},
		{say: "go to the invoked policy", method: protocol.MethodDefinition, params: func(c *client) any { return c.position(prod, editing, "\nguardr") }},
		{say: "go to the input service", method: protocol.MethodDefinition, params: func(c *client) any { return c.position(prod, editing, "when serv") }},
		{say: "edit " + prod + " to misspell an input", doc: prod, method: protocol.MethodDidChange, notify: true, params: func(c *client) any { return c.change(prod, misspelled) }},
		{say: "quick fixes for the misspelled input", method: protocol.MethodCodeAction, params: func(c *client) any { return c.actions(prod) }},
		{say: "edit " + prod + " back, unformatted and with a let nothing reads", doc: prod, method: protocol.MethodDidChange, notify: true, params: func(c *client) any { return c.change(prod, unformatted) }},
		{say: "inlay hints of " + prod, method: protocol.MethodInlayHint, params: func(c *client) any {
			return map[string]any{"textDocument": map[string]any{"uri": c.uri(prod)}, "range": map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 99, "character": 0}}}
		}},
		{say: "format " + prod, method: protocol.MethodFormatting, params: func(c *client) any {
			return map[string]any{"textDocument": map[string]any{"uri": c.uri(prod)}, "options": map[string]any{"tabSize": 2, "insertSpaces": true}}
		}},
		{say: "close " + prod, doc: prod, method: protocol.MethodDidClose, notify: true, params: func(c *client) any {
			return map[string]any{"textDocument": map[string]any{"uri": c.uri(prod)}}
		}},
		{say: "shutdown", method: protocol.MethodShutdown},
	}
	for _, s := range steps {
		c.run(s)
	}
	c.exit()
	golden(t, filepath.Join("testdata", "session.golden"), c.log.String())
}

// start copies testdata/editor to a temporary directory, where no
// configuration file above it can change what the server reads, and starts
// a server on it.
func start(t *testing.T) *client {
	t.Helper()
	root := t.TempDir()
	if real, err := filepath.EvalSymlinks(root); err == nil {
		root = real
	}
	copyTree(t, filepath.Join("testdata", "editor"), root)
	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()
	c := &client{t: t, w: jsonrpc.NewWriter(fromClient), in: make(chan *jsonrpc.Message, 64), root: root, rootURI: fileURI(root), versions: map[string]int32{}, shown: map[string][]protocol.Diagnostic{}}
	s := server.New(loader{}, server.WithDelay(time.Millisecond), server.WithVersion("test"))
	done := make(chan error, 1)
	go func() {
		done <- s.Serve(context.Background(), toServer, fromServer)
		_ = fromServer.Close()
	}()
	go func() {
		r := jsonrpc.NewReader(toClient)
		for {
			body, err := r.Read()
			if err != nil {
				close(c.in)
				return
			}
			m, bad := jsonrpc.Decode(body)
			if bad != nil {
				t.Errorf("the server sent something that isn't a message: %s", body)
				continue
			}
			c.in <- m
		}
	}()
	t.Cleanup(func() {
		_ = fromClient.Close()
		select {
		case err := <-done:
			if err != nil && !c.exited() {
				t.Errorf("Serve() = %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("the server didn't stop")
		}
	})
	return c
}

// exited reports whether the session ended with exit.
func (c *client) exited() bool { return strings.Contains(c.log.String(), "--> exit") }

// run runs one step: it sends the step, then for a request writes the
// answer to the transcript, after every notification the server sent
// before it. After an open or a change, it waits for the diagnostics the
// load publishes for the document, at its new version; after any other
// notification it sends a request of its own that isn't written down, so
// what the notification made the server do is in the transcript at that
// point, whatever the server's timing.
func (c *client) run(s step) {
	c.t.Helper()
	var params any
	if s.params != nil {
		params = s.params(c)
	}
	fmt.Fprintf(&c.log, "--> %s\n", s.say)
	if !s.notify {
		c.request(s.method, params, true)
		return
	}
	c.send(jsonrpc.Notification(s.method, params))
	switch s.method {
	case protocol.MethodDidOpen, protocol.MethodDidChange:
		c.published(s.doc)
	default:
		c.request(protocol.MethodHover, c.sync(s.doc), false)
	}
}

// published waits for the server to publish the diagnostics of doc at
// the version the client sent last, which it does after every load of
// doc's project, keeping what the server sent meanwhile in the
// transcript.
func (c *client) published(doc string) {
	c.t.Helper()
	for {
		select {
		case m, ok := <-c.in:
			if !ok {
				c.t.Fatal("the server closed the connection waiting for diagnostics")
			}
			if !m.IsNotification() {
				c.t.Fatalf("got the answer to %s, waiting for diagnostics", m.ID)
			}
			c.render(m.Method, m.Params)
			var p protocol.PublishDiagnosticsParams
			if m.Method == protocol.MethodPublishDiagnostic && json.Unmarshal(m.Params, &p) == nil && p.URI == c.uri(doc) && p.Version != nil && *p.Version == c.versions[doc] {
				return
			}
		case <-time.After(10 * time.Second):
			c.t.Fatalf("no diagnostics for %s", doc)
		}
	}
}

// request sends a request and waits for its answer, keeping the server's
// notifications in the transcript, and the answer when show is set.
func (c *client) request(method string, params any, show bool) {
	c.t.Helper()
	c.id++
	id := json.RawMessage(fmt.Sprint(c.id))
	raw, err := json.Marshal(params)
	if err != nil {
		c.t.Fatal(err)
	}
	c.send(&jsonrpc.Message{ID: id, Method: method, Params: raw})
	for {
		select {
		case m, ok := <-c.in:
			if !ok {
				c.t.Fatalf("the server closed the connection waiting for %s", method)
			}
			if m.IsNotification() {
				c.render(m.Method, m.Params)
				continue
			}
			if string(m.ID) != string(id) {
				c.t.Fatalf("got the answer to %s, waiting for %s", m.ID, id)
			}
			switch {
			case !show:
			case m.Error != nil:
				fmt.Fprintf(&c.log, "<-- error %d: %s\n", m.Error.Code, m.Error.Message)
			default:
				c.render(method, m.Result)
			}
			return
		case <-time.After(10 * time.Second):
			c.t.Fatalf("no answer to %s", method)
		}
	}
}

// sync returns params for the request that follows a notification about
// doc: a hover at its start, which loads the document's project first if
// it changed, so what that load publishes comes before the answer,
// whatever the server's timing. After a close the server has loaded the
// project already, and the hover fails unseen; without a document it
// asks about the kind file, which isn't open.
func (c *client) sync(doc string) any {
	if doc == "" {
		doc = "deploy_approval.sigil"
	}
	return map[string]any{"textDocument": map[string]any{"uri": c.uri(doc)}, "position": map[string]any{"line": 0, "character": 0}}
}

// exit sends exit, which ends the session.
func (c *client) exit() {
	fmt.Fprintf(&c.log, "--> exit\n")
	c.send(jsonrpc.Notification(protocol.MethodExit, nil))
	select {
	case _, ok := <-c.in:
		if ok {
			c.t.Error("the server sent something after exit")
		}
	case <-time.After(10 * time.Second):
		c.t.Error("the server didn't close the connection after exit")
	}
}

// send writes a message to the server.
func (c *client) send(m *jsonrpc.Message) {
	c.t.Helper()
	if err := c.w.Write(m); err != nil {
		c.t.Fatal(err)
	}
}

// json writes a message from the server to the transcript as indented
// JSON, with the workspace's directory written as /workspace.
func (c *client) json(raw json.RawMessage) {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		c.t.Fatal(err)
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		c.t.Fatal(err)
	}
	fmt.Fprintf(&c.log, "%s\n", c.relative(string(out)))
}

// relative writes the workspace's directory in s as /workspace.
func (c *client) relative(s string) string {
	return strings.ReplaceAll(s, c.rootURI, "file:///workspace")
}

// read returns a file of the workspace.
func (c *client) read(name string) string {
	src, err := os.ReadFile(filepath.Join(c.root, filepath.FromSlash(name)))
	if err != nil {
		c.t.Fatal(err)
	}
	return string(src)
}

// uri returns the URI of a file of the workspace.
func (c *client) uri(name string) string {
	return fileURI(filepath.Join(c.root, filepath.FromSlash(name)))
}

// open returns the params of didOpen for a file with text.
func (c *client) open(name, text string) any {
	c.versions[name] = 1
	return map[string]any{"textDocument": map[string]any{"uri": c.uri(name), "languageId": "sigil", "version": 1, "text": text}}
}

// change returns the params of a full didChange of a file to text.
func (c *client) change(name, text string) any {
	c.versions[name]++
	return map[string]any{
		"textDocument":   map[string]any{"uri": c.uri(name), "version": c.versions[name]},
		"contentChanges": []any{map[string]any{"text": text}},
	}
}

// position returns the params of a request at the position just after
// the first occurrence of after in text, the text of the file name.
func (c *client) position(name, text, after string) any {
	c.t.Helper()
	i := strings.LastIndex(text, after)
	if i < 0 {
		c.t.Fatalf("%q isn't in the text", after)
	}
	before := text[:i+len(after)]
	line := strings.Count(before, "\n")
	char := len(before) - strings.LastIndex(before, "\n") - 1
	return map[string]any{"textDocument": map[string]any{"uri": c.uri(name)}, "position": map[string]any{"line": line, "character": char}}
}

// fileURI returns the file URI of an absolute path.
func fileURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// copyTree copies the files below from into to.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), src, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// golden compares got with the golden file at path, or rewrites it with
// -update.
func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal([]byte(got), want) {
		t.Errorf("the session differs from %s (run with -update to accept):\n%s", path, got)
	}
}

// actions returns the params of a code action request for the whole
// file name, with the diagnostics the server published for it last.
func (c *client) actions(name string) any {
	diagnostics := c.shown[c.uri(name)]
	if diagnostics == nil {
		diagnostics = []protocol.Diagnostic{}
	}
	return map[string]any{
		"textDocument": map[string]any{"uri": c.uri(name)},
		"range":        map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 99, "character": 0}},
		"context":      map[string]any{"diagnostics": diagnostics},
	}
}
