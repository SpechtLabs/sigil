package lsp

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"sort"
	"time"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// document is one document the client has open.
type document struct {
	uri     string
	path    string // the file's absolute path
	canon   string // the path with its symbolic links resolved, to match it to a project's file
	root    string // the root of the project it belongs to
	text    []byte
	version int32
}

// project is a project with documents open: its latest load, and what
// the server published for it.
type project struct {
	snap      *Snapshot
	versions  map[string]int32  // the version of each open document the load read, by path
	published map[string]string // the URIs the server published diagnostics for, and their paths
	names     map[string]string // the names the load reads files by, by their canonical paths
	root      string
	shown     string // the last load error shown to the user, so it's shown once
	dirty     bool   // a document changed since the load
}

// didOpen opens a document and loads its project at once, so its
// diagnostics come without waiting for the delay.
func (s *Server) didOpen(raw json.RawMessage) *jsonrpc.Error {
	p, bad := decode[protocol.DidOpenTextDocumentParams](raw)
	if bad != nil {
		return bad
	}
	path, ok := pathOf(p.TextDocument.URI)
	if !ok {
		return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "only file: documents are checked, not " + p.TextDocument.URI}
	}
	d := &document{uri: p.TextDocument.URI, path: path, canon: canonical(path), text: []byte(p.TextDocument.Text), version: p.TextDocument.Version}
	d.root = s.loader.Root(path, s.folders)
	s.docs[path] = d
	s.touch(d.root)
	s.refresh(s.projects[d.root])
	return nil
}

// didChange applies a document's changes, in order: a change without a
// range replaces the whole text, as the full sync the server asks for
// sends it, and one with a range replaces that range. Its project is
// loaded again after the delay.
func (s *Server) didChange(raw json.RawMessage) *jsonrpc.Error {
	p, bad := decode[protocol.DidChangeTextDocumentParams](raw)
	if bad != nil {
		return bad
	}
	d := s.open(p.TextDocument.URI)
	if d == nil {
		return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: p.TextDocument.URI + " isn't open"}
	}
	for _, c := range p.ContentChanges {
		d.text = apply(d.text, c, s.encoding)
	}
	d.version = p.TextDocument.Version
	s.touch(d.root)
	s.schedule()
	return nil
}

// didClose closes a document. Its project is loaded again at once, from
// disk now, so problems only the unsaved buffer had are cleared; a
// project without open documents left is dropped, and every diagnostic
// published for it is cleared.
func (s *Server) didClose(raw json.RawMessage) *jsonrpc.Error {
	p, bad := decode[protocol.DidCloseTextDocumentParams](raw)
	if bad != nil {
		return bad
	}
	d := s.open(p.TextDocument.URI)
	if d == nil {
		return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: p.TextDocument.URI + " isn't open"}
	}
	delete(s.docs, d.path)
	s.touch(d.root)
	s.prune()
	s.refresh(s.projects[d.root])
	return nil
}

// didSave takes the saved text, when the client sends it, and loads the
// project again after the delay, since a file on disk changed.
func (s *Server) didSave(raw json.RawMessage) *jsonrpc.Error {
	p, bad := decode[protocol.DidSaveTextDocumentParams](raw)
	if bad != nil {
		return bad
	}
	d := s.open(p.TextDocument.URI)
	if d == nil {
		return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: p.TextDocument.URI + " isn't open"}
	}
	if p.Text != nil {
		d.text = []byte(*p.Text)
	}
	s.touch(d.root)
	s.schedule()
	return nil
}

// didChangeFolders follows the client's workspace folders: every open
// document's project is found again, and loaded after the delay.
func (s *Server) didChangeFolders(raw json.RawMessage) *jsonrpc.Error {
	p, bad := decode[protocol.DidChangeWorkspaceFoldersParams](raw)
	if bad != nil {
		return bad
	}
	for _, f := range p.Event.Removed {
		if path, ok := pathOf(f.URI); ok {
			s.folders = slices.DeleteFunc(s.folders, func(have string) bool { return have == path })
		}
	}
	for _, f := range p.Event.Added {
		if path, ok := pathOf(f.URI); ok && !slices.Contains(s.folders, path) {
			s.folders = append(s.folders, path)
		}
	}
	for _, d := range s.docs {
		d.root = s.loader.Root(d.path, s.folders)
	}
	s.touchAll()
	s.prune()
	return nil
}

// touchAll marks every project changed, such as after files changed on
// disk, and loads them again after the delay.
func (s *Server) touchAll() {
	for _, d := range s.docs {
		s.touch(d.root)
	}
	s.schedule()
}

// touch marks the project at root changed, creating it on first use.
func (s *Server) touch(root string) {
	p, ok := s.projects[root]
	if !ok {
		p = &project{root: root}
		s.projects[root] = p
	}
	p.dirty = true
}

// prune drops the projects no open document belongs to any more, and
// clears what was published for them.
func (s *Server) prune() {
	held := map[string]bool{}
	for _, d := range s.docs {
		held[d.root] = true
	}
	for _, root := range sortedKeys(s.projects) {
		if held[root] {
			continue
		}
		for _, uri := range sortedKeys(s.projects[root].published) {
			s.notify(protocol.MethodPublishDiagnostic, protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{}})
		}
		delete(s.projects, root)
	}
}

// schedule loads the changed projects once the delay has passed without
// another change.
func (s *Server) schedule() {
	s.stopTimer()
	s.timer = time.NewTimer(s.delay) //nolint:clockinterface // tests set the delay with WithDelay, and nothing reads the time
}

// stopTimer stops a scheduled load.
func (s *Server) stopTimer() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// flush loads every changed project again and publishes its diagnostics.
func (s *Server) flush() {
	for _, root := range sortedKeys(s.projects) {
		s.refresh(s.projects[root])
	}
}

// refresh loads p again if a document changed since its last load, and
// publishes its diagnostics. A nil p is left alone.
func (s *Server) refresh(p *project) {
	if p == nil || !p.dirty {
		return
	}
	overlay := map[string][]byte{}
	versions := map[string]int32{}
	for _, d := range s.docs {
		overlay[d.path] = d.text
		if d.root == p.root {
			versions[d.path] = d.version
		}
	}
	p.snap = s.load(p.root, overlay)
	p.versions = versions
	p.names = nil
	p.dirty = false
	if p.snap.Err != nil {
		if msg := describe(p.snap.Err); msg != p.shown {
			p.shown = msg
			s.show(protocol.MessageError, msg)
		}
	} else {
		p.shown = ""
	}
	s.publish(p)
}

// load loads the project at root. A loader that panics leaves an empty
// snapshot, and the panic in the log.
func (s *Server) load(root string, overlay map[string][]byte) (snap *Snapshot) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("loading %s panicked: %v", root, r)
			snap = &Snapshot{}
		}
	}()
	if snap = s.loader.Load(root, overlay); snap == nil {
		snap = &Snapshot{}
	}
	return snap
}

// publish publishes p's diagnostics, file by file, and an empty list for
// every file it published before that has none now. A diagnostic without
// a file, which no document can show, goes to the log. An open document's
// list carries the version the load read.
func (s *Server) publish(p *project) {
	byURI := map[string][]protocol.Diagnostic{}
	paths := map[string]string{}
	for _, e := range p.snap.Diagnostics {
		if e.File == "" {
			s.logf("%s", e.Error())
			continue
		}
		uri, path := s.uriOfName(e.File)
		var src []byte
		if p.snap.Project != nil {
			src = p.snap.Project.SourceOf(e.File)
		}
		byURI[uri] = append(byURI[uri], diagnostic(e, newLines(src, s.encoding)))
		paths[uri] = path
	}
	for _, uri := range sortedKeys(byURI) {
		s.notify(protocol.MethodPublishDiagnostic, protocol.PublishDiagnosticsParams{URI: uri, Version: p.version(paths[uri]), Diagnostics: byURI[uri]})
	}
	for _, uri := range sortedKeys(p.published) {
		if _, still := byURI[uri]; !still {
			s.notify(protocol.MethodPublishDiagnostic, protocol.PublishDiagnosticsParams{URI: uri, Version: p.version(p.published[uri]), Diagnostics: []protocol.Diagnostic{}})
		}
	}
	p.published = paths
}

// version returns the version of the open document at path the latest
// load read, or nil for a file that isn't open.
func (p *project) version(path string) *int32 {
	if v, ok := p.versions[path]; ok {
		return &v
	}
	return nil
}

// uriOfName returns the URI of a file a project names, and the path of
// the open document it is, if any: an open document's own URI, so the
// client matches it to the buffer however the path was spelled.
func (s *Server) uriOfName(name string) (uri, path string) {
	path = filepath.FromSlash(name)
	if d, ok := s.docs[path]; ok {
		return d.uri, d.path
	}
	canon := canonical(path)
	for _, d := range s.docs {
		if d.canon == canon {
			return d.uri, d.path
		}
	}
	return uriOf(path), path
}

// open returns the open document uri names, or nil.
func (s *Server) open(uri string) *document {
	path, ok := pathOf(uri)
	if !ok {
		return nil
	}
	return s.docs[path]
}

// current returns the open document uri names, its project's latest load,
// made fresh if a document changed since, and the name the project knows
// its file by. The name is "" when the project didn't read the file.
func (s *Server) current(uri string) (*document, *Snapshot, string) {
	d := s.open(uri)
	if d == nil {
		return nil, nil, ""
	}
	p := s.projects[d.root]
	s.refresh(p)
	if p == nil || p.snap == nil || p.snap.Project == nil {
		return d, nil, ""
	}
	return d, p.snap, p.nameOf(d)
}

// nameOf returns the name p's load reads d's file by, or "".
func (p *project) nameOf(d *document) string {
	if p.names == nil {
		p.names = map[string]string{}
		for _, name := range p.snap.Project.SourceNames() {
			p.names[canonical(filepath.FromSlash(name))] = name
		}
	}
	return p.names[d.canon]
}

// diagnostic converts a diagnostic of `sigil check` into the protocol's,
// with its help after the message, as check prints it.
func diagnostic(e *diag.Error, l *lines) protocol.Diagnostic {
	from, to := 0, 0
	if e.Pos.IsValid() {
		from, to = e.Pos.Offset, e.Pos.Offset
		if e.End.IsValid() && e.End.Offset > from {
			to = e.End.Offset
		}
	}
	msg := e.Msg
	if e.Help != "" {
		msg += "\nhelp: " + e.Help
	}
	severity := protocol.SeverityError
	if e.Severity == diag.SeverityWarning {
		severity = protocol.SeverityWarning
	}
	return protocol.Diagnostic{Range: l.rangeOf(from, to), Severity: severity, Code: e.Code, Source: "sigil", Message: msg}
}

// canonical returns path with its symbolic links resolved, or path itself
// when it can't be, such as for a file only an editor buffer holds.
func canonical(path string) string {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	if real, err := filepath.EvalSymlinks(filepath.Dir(path)); err == nil {
		return filepath.Join(real, filepath.Base(path))
	}
	return path
}

// sortedKeys returns m's keys, sorted, so what the server sends doesn't
// depend on map order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// apply returns text with one change made: the whole new text, or the
// text that replaces the change's range, whose positions count in
// encoding. A range given end first is taken the right way round.
func apply(text []byte, c protocol.TextDocumentContentChangeEvent, encoding string) []byte {
	if c.Range == nil {
		return []byte(c.Text)
	}
	l := newLines(text, encoding)
	from, to := l.offset(c.Range.Start), l.offset(c.Range.End)
	if to < from {
		from, to = to, from
	}
	return slices.Concat(text[:from], []byte(c.Text), text[to:])
}
