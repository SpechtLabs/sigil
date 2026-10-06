package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// session drives a Server over in-memory pipes, the way an editor does.
type session struct {
	t      *testing.T
	raw    io.WriteCloser // the server's stdin
	w      *jsonrpc.Writer
	in     chan *jsonrpc.Message
	done   chan error
	log    *syncBuffer
	notes  []*jsonrpc.Message // the notifications the server sent, in order
	id     int
	cancel context.CancelFunc
}

// syncBuffer is a buffer the server logs to while the test reads it.
type syncBuffer struct {
	b  bytes.Buffer
	mu sync.Mutex
}

// TestLifecycle drives sessions through the protocol's lifecycle, and
// sends what isn't a valid message: each case sends its steps and checks
// the answers, then how Serve ended.
func TestLifecycle(t *testing.T) {
	tests := []struct {
		name  string
		steps func(s *session)
		end   func(s *session)
		err   string // what Serve's error holds; empty for nil
	}{
		{
			name: "a request before initialize",
			steps: func(s *session) {
				s.wantError(s.call(protocol.MethodHover, s.at("production.sigil", 0, 0)), jsonrpc.ServerNotInitialized)
				s.initialize()
			},
			end: (*session).shutdown,
		},
		{
			name: "a notification before initialize",
			steps: func(s *session) {
				s.notify(protocol.MethodDidOpen, s.opening("production.sigil", "policy p: DeployApproval@2\n"))
				s.initialize()
				s.wantError(s.call(protocol.MethodHover, s.at("production.sigil", 0, 0)), jsonrpc.InvalidParams)
			},
			end: (*session).shutdown,
		},
		{
			name: "initialize twice",
			steps: func(s *session) {
				s.initialize()
				s.wantError(s.call(protocol.MethodInitialize, map[string]any{"capabilities": map[string]any{}}), jsonrpc.InvalidRequest)
			},
			end: (*session).shutdown,
		},
		{
			name: "initialize without params",
			steps: func(s *session) {
				s.wantError(s.call(protocol.MethodInitialize, nil), jsonrpc.InvalidParams)
				s.initialize()
			},
			end: (*session).shutdown,
		},
		{
			name: "an unknown request",
			steps: func(s *session) {
				s.initialize()
				s.wantError(s.call("textDocument/codeLens", map[string]any{}), jsonrpc.MethodNotFound)
			},
			end: (*session).shutdown,
		},
		{
			name: "an unknown notification",
			steps: func(s *session) {
				s.initialize()
				s.notify("$/setTrace", map[string]any{"value": "off"})
				s.notify(protocol.MethodInitialized, map[string]any{})
				s.wantResult(s.call(protocol.MethodShutdown, nil), "null")
			},
			end: (*session).exit,
		},
		{
			name: "params that don't fit",
			steps: func(s *session) {
				s.initialize()
				s.wantError(s.call(protocol.MethodCompletion, map[string]any{"position": "here"}), jsonrpc.InvalidParams)
				s.notify(protocol.MethodDidOpen, map[string]any{"textDocument": 1})
			},
			end: (*session).shutdown,
		},
		{
			name: "a request after shutdown",
			steps: func(s *session) {
				s.initialize()
				s.wantResult(s.call(protocol.MethodShutdown, nil), "null")
				s.wantError(s.call(protocol.MethodHover, s.at("production.sigil", 0, 0)), jsonrpc.InvalidRequest)
				s.notify(protocol.MethodDidOpen, s.opening("production.sigil", "x"))
			},
			end: (*session).exit,
		},
		{
			name:  "exit without shutdown",
			steps: (*session).initialize,
			end:   (*session).exit,
			err:   "ended the session without shutting the language server down",
		},
		{
			name:  "exit before initialize",
			steps: func(*session) {},
			end:   (*session).exit,
			err:   "without shutting the language server down",
		},
		{
			name: "the stream ends after shutdown",
			steps: func(s *session) {
				s.initialize()
				s.wantResult(s.call(protocol.MethodShutdown, nil), "null")
			},
			end: (*session).hangUp,
		},
		{
			name:  "the stream ends without shutdown",
			steps: (*session).initialize,
			end:   (*session).hangUp,
			err:   "closed the connection without shutting the language server down",
		},
		{
			name: "a broken header",
			steps: func(s *session) {
				s.initialize()
				s.sendRaw("Content-Length: x\r\n\r\n")
			},
			end: (*session).wait,
			err: "the stream from the editor broke",
		},
		{
			name: "a body that isn't JSON",
			steps: func(s *session) {
				s.initialize()
				s.sendRaw("Content-Length: 5\r\n\r\n{\"a\":")
				m := s.next()
				s.wantError(m, jsonrpc.ParseError)
				if string(m.ID) != "null" {
					t.Errorf("the error answers %s, want null", m.ID)
				}
			},
			end: (*session).shutdown,
		},
		{
			name: "a message of another version",
			steps: func(s *session) {
				s.initialize()
				s.sendRaw(framed(`{"jsonrpc":"1.0","id":99,"method":"shutdown"}`))
				m := s.next()
				s.wantError(m, jsonrpc.InvalidRequest)
				if string(m.ID) != "99" {
					t.Errorf("the error answers %s, want 99", m.ID)
				}
			},
			end: (*session).shutdown,
		},
		{
			name: "a response from the editor",
			steps: func(s *session) {
				s.initialize()
				s.sendRaw(framed(`{"jsonrpc":"2.0","id":7,"result":null}`))
			},
			end: (*session).shutdown,
		},
		{
			name: "a canceled request",
			steps: func(s *session) {
				s.initialize()
				s.notify(protocol.MethodCancelRequest, map[string]any{"id": s.id + 1})
				s.notify(protocol.MethodCancelRequest, map[string]any{})
				s.wantError(s.call(protocol.MethodHover, s.at("production.sigil", 0, 0)), jsonrpc.RequestCanceled)
				s.wantError(s.call(protocol.MethodHover, s.at("production.sigil", 0, 0)), jsonrpc.InvalidParams)
			},
			end: (*session).shutdown,
		},
		{
			name:  "the context ends",
			steps: (*session).initialize,
			end: func(s *session) {
				s.cancel()
				s.wait()
			},
			err: "the language server was stopped",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := start(t, &memLoader{files: testWorkspace(t)})
			tt.steps(s)
			tt.end(s)
			err := <-s.done
			switch {
			case tt.err == "" && err != nil:
				t.Errorf("Serve() = %v, want nil", err)
			case tt.err != "" && (err == nil || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("Serve() = %v, want an error holding %q", err, tt.err)
			}
		})
	}
}

// TestInitialize checks what initialize answers: the capabilities, and
// the position encoding the client and the server agree on.
func TestInitialize(t *testing.T) {
	tests := []struct {
		name      string
		encodings []string
		want      string
	}{
		{name: "none listed", want: protocol.EncodingUTF16},
		{name: "UTF-16 only", encodings: []string{protocol.EncodingUTF16}, want: protocol.EncodingUTF16},
		{name: "UTF-8 offered", encodings: []string{protocol.EncodingUTF16, protocol.EncodingUTF8}, want: protocol.EncodingUTF8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := start(t, &memLoader{files: testWorkspace(t)}, WithVersion("v1.2.3"))
			params := map[string]any{"rootUri": "file:///ws", "capabilities": map[string]any{}}
			if tt.encodings != nil {
				params["capabilities"] = map[string]any{"general": map[string]any{"positionEncodings": tt.encodings}}
			}
			var got protocol.InitializeResult
			s.decode(s.call(protocol.MethodInitialize, params), &got)
			if got.Capabilities.PositionEncoding != tt.want {
				t.Errorf("position encoding = %q, want %q", got.Capabilities.PositionEncoding, tt.want)
			}
			if got.ServerInfo == nil || got.ServerInfo.Version != "v1.2.3" {
				t.Errorf("server info = %+v", got.ServerInfo)
			}
			c := got.Capabilities
			if !c.HoverProvider || !c.DefinitionProvider || !c.DocumentFormattingProvider || c.CompletionProvider == nil || c.TextDocumentSync.Change != protocol.SyncFull {
				t.Errorf("capabilities = %+v", c)
			}
			s.shutdown()
		})
	}
}

// TestFolders checks which workspace folders initialize reads: the folders
// it lists, or else the root URI, or else the root path.
func TestFolders(t *testing.T) {
	root, other := "file:///ws", "file:///other"
	tests := []struct {
		name   string
		params protocol.InitializeParams
		want   []string
	}{
		{name: "folders", params: protocol.InitializeParams{RootURI: &other, WorkspaceFolders: []protocol.WorkspaceFolder{{URI: root}, {URI: "untitled:x"}}}, want: []string{"/ws"}},
		{name: "a root URI", params: protocol.InitializeParams{RootURI: &root}, want: []string{"/ws"}},
		{name: "a root URI that isn't a file", params: protocol.InitializeParams{RootURI: new("untitled:x")}},
		{name: "a root path", params: protocol.InitializeParams{RootPath: new("/ws")}, want: []string{"/ws"}},
		{name: "nothing", params: protocol.InitializeParams{RootPath: new("")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := foldersOf(tt.params)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("foldersOf() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDescribe renders errors for the user: a humane error with its
// advice, and a plain one.
func TestDescribe(t *testing.T) {
	if got := describe(errors.New("plain")); got != "plain" {
		t.Errorf("describe() = %q", got)
	}
}

// start starts a server with loader, delaying loads by nothing unless
// opts say otherwise, and returns the session driving it.
func start(t *testing.T, loader Loader, opts ...Option) *session {
	t.Helper()
	toServer, raw := io.Pipe()
	fromServer, out := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{t: t, raw: raw, w: jsonrpc.NewWriter(raw), in: make(chan *jsonrpc.Message, 64), done: make(chan error, 1), log: &syncBuffer{}, cancel: cancel}
	server := New(loader, append([]Option{WithDelay(0), WithLog(s.log)}, opts...)...)
	go func() {
		err := server.Serve(ctx, toServer, out)
		_ = out.Close()
		_ = toServer.Close()
		s.done <- err
	}()
	go func() {
		r := jsonrpc.NewReader(fromServer)
		for {
			body, err := r.Read()
			if err != nil {
				close(s.in)
				return
			}
			m, bad := jsonrpc.Decode(body)
			if bad != nil {
				t.Errorf("the server sent something that isn't a message: %s", body)
				continue
			}
			s.in <- m
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = raw.Close()
	})
	return s
}

// initialize initializes the session for the test workspace.
func (s *session) initialize() {
	s.t.Helper()
	s.wantNoError(s.call(protocol.MethodInitialize, map[string]any{"rootUri": "file://" + root, "capabilities": map[string]any{}}))
	s.notify(protocol.MethodInitialized, map[string]any{})
}

// shutdown shuts the session down and exits.
func (s *session) shutdown() {
	s.t.Helper()
	s.wantResult(s.call(protocol.MethodShutdown, nil), "null")
	s.exit()
}

// exit sends exit, and waits for the server to close its stream.
func (s *session) exit() {
	s.t.Helper()
	s.notify(protocol.MethodExit, nil)
	s.drain()
}

// hangUp closes the server's stdin, as an editor that quits does.
func (s *session) hangUp() {
	s.t.Helper()
	_ = s.raw.Close()
	s.drain()
}

// wait waits for the server to close its stream on its own.
func (s *session) wait() {
	s.t.Helper()
	s.drain()
}

// drain reads what's left until the server closes its stream.
func (s *session) drain() {
	s.t.Helper()
	for {
		select {
		case m, ok := <-s.in:
			if !ok {
				return
			}
			if m.IsNotification() {
				s.notes = append(s.notes, m)
			}
		case <-time.After(10 * time.Second):
			s.t.Fatal("the server didn't close its stream")
		}
	}
}

// call sends a request and returns its answer, keeping the
// notifications the server sent before it.
func (s *session) call(method string, params any) *jsonrpc.Message {
	s.t.Helper()
	s.id++
	id := json.RawMessage(fmt.Sprint(s.id))
	var raw json.RawMessage
	if params != nil {
		var err error
		if raw, err = json.Marshal(params); err != nil {
			s.t.Fatal(err)
		}
	}
	s.send(&jsonrpc.Message{ID: id, Method: method, Params: raw})
	for {
		m := s.next()
		if m.IsNotification() {
			s.notes = append(s.notes, m)
			continue
		}
		if string(m.ID) != string(id) {
			s.t.Fatalf("got the answer to %s, waiting for %s", m.ID, id)
		}
		return m
	}
}

// notify sends a notification.
func (s *session) notify(method string, params any) {
	s.t.Helper()
	s.send(jsonrpc.Notification(method, params))
}

// send writes a message to the server.
func (s *session) send(m *jsonrpc.Message) {
	s.t.Helper()
	if err := s.w.Write(m); err != nil {
		s.t.Fatal(err)
	}
}

// sendRaw writes bytes to the server as they are.
func (s *session) sendRaw(b string) {
	s.t.Helper()
	if _, err := io.WriteString(s.raw, b); err != nil {
		s.t.Fatal(err)
	}
}

// next returns the next message from the server.
func (s *session) next() *jsonrpc.Message {
	s.t.Helper()
	select {
	case m, ok := <-s.in:
		if !ok {
			s.t.Fatal("the server closed its stream")
		}
		return m
	case <-time.After(10 * time.Second):
		s.t.Fatal("the server didn't answer")
	}
	return nil
}

// wantError checks that m failed with code.
func (s *session) wantError(m *jsonrpc.Message, code jsonrpc.Code) {
	s.t.Helper()
	if m.Error == nil || m.Error.Code != code {
		s.t.Errorf("answer = %+v (result %s), want error %d", m.Error, m.Result, code)
	}
}

// wantNoError checks that m succeeded.
func (s *session) wantNoError(m *jsonrpc.Message) {
	s.t.Helper()
	if m.Error != nil {
		s.t.Fatalf("answer = %+v", m.Error)
	}
}

// wantResult checks m's result.
func (s *session) wantResult(m *jsonrpc.Message, want string) {
	s.t.Helper()
	s.wantNoError(m)
	if string(m.Result) != want {
		s.t.Errorf("result = %s, want %s", m.Result, want)
	}
}

// decode reads m's result into v.
func (s *session) decode(m *jsonrpc.Message, v any) {
	s.t.Helper()
	s.wantNoError(m)
	if err := json.Unmarshal(m.Result, v); err != nil {
		s.t.Fatal(err)
	}
}

// uri returns the URI of a file of the test workspace.
func (s *session) uri(file string) string { return "file://" + root + "/" + file }

// at returns the params of a request at a position in file.
func (s *session) at(file string, line, char int) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": s.uri(file)}, "position": map[string]any{"line": line, "character": char}}
}

// opening returns the params of didOpen for file with text.
func (s *session) opening(file, text string) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": s.uri(file), "languageId": "sigil", "version": 1, "text": text}}
}

// framed frames a body as the base protocol does.
func framed(body string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(body), body)
}

// Write implements io.Writer.
func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

// String returns what was written.
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}
