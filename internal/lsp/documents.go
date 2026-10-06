package lsp

import (
	"encoding/json"
	"path/filepath"
	"slices"

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
	r := s.rootOf(path)
	d.root = r.Path
	s.docs[path] = d
	s.start(s.touch(r))
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
	s.changed(d)
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
	s.prune()
	if pr := s.projects[d.root]; pr != nil {
		pr.dirty = true
		s.start(pr)
	}
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
	s.changed(d)
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
	s.reroot()
	return nil
}

// didChangeWatched follows files changed on disk. A configuration file
// created or deleted moves documents to another root, so every open
// document's project is found again, and every project is loaded again
// after the delay.
func (s *Server) didChangeWatched() {
	s.reroot()
}

// reroot finds every open document's project again, drops the projects
// no document belongs to any more, and loads the rest after the delay.
func (s *Server) reroot() {
	for _, path := range sortedKeys(s.docs) {
		d := s.docs[path]
		r := s.rootOf(d.path)
		d.root = r.Path
		s.touch(r)
	}
	s.prune()
	s.schedule()
}

// changed marks d's project changed and loads it after the delay.
func (s *Server) changed(d *document) {
	if p := s.projects[d.root]; p != nil {
		p.dirty = true
	}
	s.schedule()
}

// open returns the open document uri names, or nil.
func (s *Server) open(uri string) *document {
	path, ok := pathOf(uri)
	if !ok {
		return nil
	}
	return s.docs[path]
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
