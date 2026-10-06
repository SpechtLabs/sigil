// Package lsp is the Sigil language server that `sigil lsp` runs. An
// editor starts it in the background, talks to it over stdin and stdout,
// and gets the diagnostics `sigil check` reports, completion, hover,
// go-to-definition and formatting for the policies, modules and kind
// files it has open.
//
// A [Server] reads the messages package jsonrpc frames and handles them
// one at a time, in order, on the goroutine that called [Server.Serve];
// a second goroutine only reads, so a $/cancelRequest is seen while an
// earlier request is still being answered. Nothing but protocol goes to
// the client's stream: logs go to the writer [WithLog] names, stderr in
// the CLI, and what the user should see goes out as window/showMessage.
//
// Each open document belongs to a project, which a [Loader] finds and
// loads: the CLI's reads it the way `sigil check` does, from the nearest
// configuration file. The open buffers replace their files on disk, and a
// project is loaded again a moment after it last changed, on a goroutine
// of its own so the message loop keeps answering, then its diagnostics
// are published. Completion, hover and definition check the document's
// current text against the project's latest load (see newView), and read
// the checker's [check.Info], its scopes included, and the tokens around
// the cursor (see context.go), so they work on a document that doesn't
// parse yet, and cost a check of one document after an edit.
package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// lifecycle is where a session is: before initialize, running, or shut
// down and waiting for exit.
type lifecycle uint8

const (
	uninitialized lifecycle = iota
	running
	shutDown
)

// ending is what a message does to the session: nothing, end it after a
// shutdown, or end it without one.
type ending uint8

const (
	goingOn ending = iota
	exited
	exitedEarly
)

// null is JSON's null, the result of shutdown.
var null = json.RawMessage("null")

// configNames are the names of a configuration file, which the server
// asks the client to watch.
var configNames = []string{"sigil.yaml", "sigil.json", "sigil.toml", ".sigil.yaml", ".sigil.json", ".sigil.toml"}

// maxCanceled is how many cancellations the server keeps before it
// forgets them all.
const maxCanceled = 256

// Server is one language server session. Create it with [New] and run
// it with [Server.Serve], once.
type Server struct {
	loader   Loader
	log      io.Writer
	out      *jsonrpc.Writer
	docs     map[string]*document // the open documents, by path
	projects map[string]*project  // the projects they belong to, by root
	canceled *cancellations       // the requests canceled before they were answered
	loads    chan loaded          // the loads that finished
	done     chan struct{}        // closed when Serve returns, so a load still running drops its result
	noted    map[string]bool      // the root notes shown to the user, so each is shown once
	timer    *time.Timer          // fires when the projects that changed are loaded again; nil when none waits
	version  string
	encoding string   // what a position's character counts: protocol.EncodingUTF8 or protocol.EncodingUTF16
	folders  []string // the client's workspace folders, as absolute paths
	delay    time.Duration
	nextID   int  // the id of the last request the server sent
	watch    bool // the client lets the server register file watchers
	state    lifecycle
}

// cancellations are the ids of the requests the client canceled before
// the server answered them. The reading goroutine adds to them while the
// server works, so a mutex guards them. A cancellation of a request that
// was answered already stays until there are maxCanceled of them, when
// they're all forgotten: the protocol makes canceling a hint.
type cancellations struct {
	ids map[string]bool
	mu  sync.Mutex
}

// input is what the reading goroutine passes on: a message, a message
// that isn't one and the error to answer it with, or the error that ended
// the stream.
type input struct {
	msg *jsonrpc.Message
	bad *jsonrpc.Error
	err error
}

// New returns a server that loads projects with loader.
func New(loader Loader, opts ...Option) *Server {
	s := &Server{
		loader:   loader,
		log:      io.Discard,
		docs:     map[string]*document{},
		projects: map[string]*project{},
		canceled: &cancellations{ids: map[string]bool{}},
		loads:    make(chan loaded),
		noted:    map[string]bool{},
		encoding: protocol.EncodingUTF16,
		delay:    defaultDelay,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Serve runs the session: it reads messages from in and writes the
// answers to out until the client sends exit, in ends, or ctx is done. It
// returns nil after a shutdown and exit, as the protocol asks; an exit
// without a shutdown, a stream that ends without one, and a stream whose
// framing breaks are errors, so the process exits with a failure.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error { //nolint:lifecycle // Serve returns when ctx is done or the client exits; there's nothing else to stop
	s.out = jsonrpc.NewWriter(out)
	inputs := make(chan input)
	s.done = make(chan struct{})
	defer close(s.done)
	go s.read(jsonrpc.NewReader(in), inputs, s.done)
	defer s.stopTimer()
	for {
		var tick <-chan time.Time
		if s.timer != nil {
			tick = s.timer.C
		}
		select {
		case <-ctx.Done():
			return humane.Wrap(ctx.Err(), "the language server was stopped", "the editor starts it again when it needs it")
		case <-tick:
			s.timer = nil
			s.flush()
		case res := <-s.loads:
			s.finish(res)
		case in := <-inputs:
			if in.err != nil {
				return s.ended(in.err)
			}
			switch s.handle(in) {
			case exited:
				return nil
			case exitedEarly:
				return humane.New("the editor ended the session without shutting the language server down", "the editor ends a session with shutdown, then exit")
			}
		}
	}
}

// read reads messages until the stream ends or breaks, which it passes on
// as the last input. A $/cancelRequest is recorded here, so it takes
// effect even while the request it cancels waits behind another.
func (s *Server) read(r *jsonrpc.Reader, inputs chan<- input, done <-chan struct{}) {
	for {
		body, err := r.Read()
		var in input
		if err != nil {
			in.err = err
		} else {
			in.msg, in.bad = jsonrpc.Decode(body)
			if in.bad == nil && in.msg.Method == protocol.MethodCancelRequest {
				s.canceled.cancel(in.msg.Params)
				continue
			}
		}
		select {
		case inputs <- in:
		case <-done:
			return
		}
		if err != nil {
			return
		}
	}
}

// ended handles the end of the stream: expected after a shutdown, and an
// error otherwise, or when the framing broke.
func (s *Server) ended(err error) error {
	if !errors.Is(err, io.EOF) {
		s.logf("the stream from the editor broke: %v", err)
		return humane.Wrap(err, "the stream from the editor broke", "restart the language server from the editor")
	}
	if s.state == shutDown {
		return nil
	}
	return humane.New("the editor closed the connection without shutting the language server down", "the editor ends a session with shutdown, then exit; restart the server from the editor")
}

// handle handles one input and says whether it ends the session.
func (s *Server) handle(in input) ending {
	if in.bad != nil {
		s.send(jsonrpc.Failure(in.msg.ID, in.bad.Code, in.bad.Message))
		return goingOn
	}
	m := in.msg
	switch {
	case m.IsRequest():
		s.request(m)
	case m.IsNotification():
		return s.notification(m)
	}
	// A response: the server sends no requests, so there's nothing to match
	// it to.
	return goingOn
}

// request answers one request.
func (s *Server) request(m *jsonrpc.Message) {
	if s.canceled.take(m.ID) {
		s.send(jsonrpc.Failure(m.ID, jsonrpc.RequestCanceled, "the request was canceled"))
		return
	}
	switch {
	case s.state == uninitialized && m.Method != protocol.MethodInitialize:
		s.send(jsonrpc.Failure(m.ID, jsonrpc.ServerNotInitialized, "the server isn't initialized yet; send initialize first"))
		return
	case s.state == shutDown:
		s.send(jsonrpc.Failure(m.ID, jsonrpc.InvalidRequest, "the server is shut down; the only message left is exit"))
		return
	}
	if s.postpone(m) {
		return
	}
	result, bad := s.call(m)
	if bad != nil {
		s.send(jsonrpc.Failure(m.ID, bad.Code, bad.Message))
		return
	}
	s.send(jsonrpc.Response(m.ID, result))
}

// call runs the handler of a request's method and returns its result,
// encoded. A handler that panics answers with an InternalError, and the
// session goes on.
func (s *Server) call(m *jsonrpc.Message) (result json.RawMessage, bad *jsonrpc.Error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("%s panicked: %v\n%s", m.Method, r, debug.Stack())
			result, bad = nil, &jsonrpc.Error{Code: jsonrpc.InternalError, Message: fmt.Sprintf("%s failed: %v", m.Method, r)}
		}
	}()
	switch m.Method {
	case protocol.MethodInitialize:
		return reply(s.initialize(m.Params))
	case protocol.MethodShutdown:
		s.state = shutDown
		return null, nil
	case protocol.MethodCompletion:
		return reply(s.completion(m.Params))
	case protocol.MethodHover:
		return reply(s.hover(m.Params))
	case protocol.MethodDefinition:
		return reply(s.definition(m.Params))
	case protocol.MethodFormatting:
		return reply(s.formatting(m.Params))
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: fmt.Sprintf("the Sigil language server doesn't implement %s", m.Method)}
}

// reply encodes a handler's result, or passes its error on.
func reply[T any](v T, bad *jsonrpc.Error) (json.RawMessage, *jsonrpc.Error) {
	if bad != nil {
		return nil, bad
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InternalError, Message: "the result couldn't be encoded: " + err.Error()}
	}
	return raw, nil
}

// notification handles one notification and says whether it ends the
// session. Notifications before initialize and after shutdown are
// dropped, apart from exit; so are the ones the server doesn't know, as
// the protocol asks.
func (s *Server) notification(m *jsonrpc.Message) ending {
	if m.Method == protocol.MethodExit {
		if s.state != shutDown {
			return exitedEarly
		}
		return exited
	}
	if s.state != running {
		return goingOn
	}
	defer func() {
		if r := recover(); r != nil {
			s.logf("%s panicked: %v\n%s", m.Method, r, debug.Stack())
		}
	}()
	var bad *jsonrpc.Error
	switch m.Method {
	case protocol.MethodDidOpen:
		bad = s.didOpen(m.Params)
	case protocol.MethodDidChange:
		bad = s.didChange(m.Params)
	case protocol.MethodDidClose:
		bad = s.didClose(m.Params)
	case protocol.MethodDidSave:
		bad = s.didSave(m.Params)
	case protocol.MethodInitialized:
		s.register()
	case protocol.MethodDidChangeWatched:
		bad = s.didChangeWatched(m.Params)
	case protocol.MethodDidChangeFolders:
		bad = s.didChangeFolders(m.Params)
	}
	if bad != nil {
		s.logf("%s: %s", m.Method, bad.Message)
	}
	return goingOn
}

// initialize answers initialize: it reads the workspace folders and the
// position encodings the client supports, and says what the server
// offers. A second initialize is an error.
func (s *Server) initialize(raw json.RawMessage) (*protocol.InitializeResult, *jsonrpc.Error) {
	if s.state != uninitialized {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidRequest, Message: "the server is already initialized"}
	}
	p, bad := decode[protocol.InitializeParams](raw)
	if bad != nil {
		return nil, bad
	}
	s.folders = foldersOf(p)
	if w := p.Capabilities.Workspace; w != nil && w.DidChangeWatchedFiles != nil {
		s.watch = w.DidChangeWatchedFiles.DynamicRegistration
	}
	if p.Capabilities.General != nil && slices.Contains(p.Capabilities.General.PositionEncodings, protocol.EncodingUTF8) {
		s.encoding = protocol.EncodingUTF8
	}
	s.state = running
	return &protocol.InitializeResult{
		ServerInfo: &protocol.ServerInfo{Name: "sigil", Version: s.version},
		Capabilities: protocol.ServerCapabilities{
			PositionEncoding: s.encoding,
			TextDocumentSync: &protocol.TextDocumentSyncOptions{
				OpenClose: true,
				Change:    protocol.SyncFull,
				Save:      &protocol.SaveOptions{},
			},
			CompletionProvider:         &protocol.CompletionOptions{TriggerCharacters: []string{".", ":", "{", "(", ",", "@"}},
			HoverProvider:              true,
			DefinitionProvider:         true,
			DocumentFormattingProvider: true,
			Workspace: &protocol.WorkspaceCapabilities{
				WorkspaceFolders: &protocol.WorkspaceFoldersServerCapabilities{Supported: true, ChangeNotifications: true},
			},
		},
	}, nil
}

// foldersOf returns the client's workspace folders as paths: the folders
// it lists, or else its root URI, or else its root path.
func foldersOf(p protocol.InitializeParams) []string {
	var out []string
	for _, f := range p.WorkspaceFolders {
		if path, ok := pathOf(f.URI); ok {
			out = append(out, path)
		}
	}
	switch {
	case len(out) > 0:
	case p.RootURI != nil:
		if path, ok := pathOf(*p.RootURI); ok {
			out = append(out, path)
		}
	case p.RootPath != nil && *p.RootPath != "":
		out = append(out, *p.RootPath)
	}
	return out
}

// cancel records that the request id names was canceled.
func (c *cancellations) cancel(raw json.RawMessage) {
	p, bad := decode[protocol.CancelParams](raw)
	if bad != nil || len(p.ID) == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.ids) >= maxCanceled {
		c.ids = map[string]bool{}
	}
	c.ids[string(p.ID)] = true
}

// take reports whether the request with id was canceled, and forgets
// that it was.
func (c *cancellations) take(id json.RawMessage) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ids[string(id)] {
		return false
	}
	delete(c.ids, string(id))
	return true
}

// send writes m to the client. A write that fails means the client is
// gone, which the next read finds out.
func (s *Server) send(m *jsonrpc.Message) {
	if err := s.out.Write(m); err != nil {
		s.logf("%v", err)
	}
}

// notify sends the client a notification.
func (s *Server) notify(method string, params any) {
	s.send(jsonrpc.Notification(method, params))
}

// show shows the user a message, and logs it.
func (s *Server) show(t protocol.MessageType, msg string) {
	s.logf("%s", msg)
	s.notify(protocol.MethodShowMessage, protocol.ShowMessageParams{Type: t, Message: msg})
}

// logf writes a line to the log.
func (s *Server) logf(format string, args ...any) {
	_, _ = fmt.Fprintf(s.log, "sigil lsp: "+strings.TrimRight(format, "\n")+"\n", args...)
}

// decode reads a method's params into a T, or returns the InvalidParams
// error that says why they don't fit.
func decode[T any](raw json.RawMessage) (T, *jsonrpc.Error) {
	var v T
	if len(raw) == 0 {
		return v, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "the method needs params"}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return v, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "the params don't fit the method: " + err.Error()}
	}
	return v, nil
}

// describe renders an error for the user: its message, and a humane
// error's advice after it.
func describe(err error) string {
	var h humane.Error
	if errors.As(err, &h) && len(h.Advice()) > 0 {
		return h.Error() + "\nhelp: " + strings.Join(h.Advice(), "; ")
	}
	return err.Error()
}

// postpone keeps a request about a position in a document whose project
// is still being loaded for the first time, and reports whether it did:
// there's nothing to answer it from yet, so the load answers it when it
// finishes.
func (s *Server) postpone(m *jsonrpc.Message) bool {
	switch m.Method {
	case protocol.MethodCompletion, protocol.MethodHover, protocol.MethodDefinition:
	default:
		return false
	}
	var p protocol.TextDocumentPositionParams
	if json.Unmarshal(m.Params, &p) != nil {
		return false
	}
	_, pr := s.current(p.TextDocument.URI)
	if pr == nil || pr.snap != nil {
		return false
	}
	pr.waiting = append(pr.waiting, m)
	return true
}

// register asks the client to watch the files a project is made of, the
// `.sigil` files and the configuration files, and to tell the server when
// one changes on disk, when the client lets it.
func (s *Server) register() {
	if !s.watch {
		return
	}
	watchers := []protocol.FileSystemWatcher{{GlobPattern: "**/*.sigil"}}
	for _, name := range configNames {
		watchers = append(watchers, protocol.FileSystemWatcher{GlobPattern: "**/" + name})
	}
	s.nextID++
	s.send(jsonrpc.Request(json.RawMessage(strconv.Itoa(s.nextID)), protocol.MethodRegisterCapability, protocol.RegistrationParams{
		Registrations: []protocol.Registration{{
			ID:              "sigil-watched-files",
			Method:          protocol.MethodDidChangeWatched,
			RegisterOptions: protocol.DidChangeWatchedFilesRegistrationOptions{Watchers: watchers},
		}},
	}))
}
