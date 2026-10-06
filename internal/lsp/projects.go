package lsp

import (
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// project is a project with documents open: its latest load, and what
// the server published for it.
type project struct {
	snap      *Snapshot
	versions  map[string]int32  // the version of each open document the load read, by path
	published map[string]string // the URIs the server published diagnostics for, and their paths
	names     map[string]string // the names the load reads files by, by their canonical paths
	root      string
	shown     string             // the last load error shown to the user, so it's shown once
	waiting   []*jsonrpc.Message // requests that came before the first load finished, answered once it has
	declared  bool               // a configuration file declares the project; see Root
	dirty     bool               // a document changed since the load started
	loading   bool               // a load is running
}

// loaded is a load that finished, for the message loop to take over.
type loaded struct {
	snap     *Snapshot
	versions map[string]int32
	owner    *project // the project the load was started for, which the result belongs to only while it's still the project at its root
	root     string
}

// rootOf returns the root of the project the file at path belongs to,
// and shows the root's note the first time it comes up.
func (s *Server) rootOf(path string) Root {
	r := s.loader.Root(path, s.folders)
	if r.Note != "" && !s.noted[r.Note] {
		s.noted[r.Note] = true
		s.show(protocol.MessageWarning, r.Note)
	}
	return r
}

// touch marks the project at r changed, creating it on first use, and
// returns it.
func (s *Server) touch(r Root) *project {
	p, ok := s.projects[r.Path]
	if !ok {
		p = &project{root: r.Path, published: map[string]string{}}
		s.projects[r.Path] = p
	}
	p.declared = r.Declared
	p.dirty = true
	return p
}

// prune drops the projects no open document belongs to any more, and
// answers the requests that waited for them, which now find their
// documents closed. What a dropped project published for a document that
// moved to another project is that project's to replace, so it doesn't
// flicker empty until the next load; everything else it published is
// cleared.
func (s *Server) prune() {
	held := map[string]bool{}
	for _, d := range s.docs {
		held[d.root] = true
	}
	for _, root := range sortedKeys(s.projects) {
		if held[root] {
			continue
		}
		p := s.projects[root]
		for _, uri := range sortedKeys(p.published) {
			if next := s.heir(p.published[uri]); next != nil {
				next.published[uri] = p.published[uri]
				continue
			}
			s.notify(protocol.MethodPublishDiagnostic, protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{}})
		}
		delete(s.projects, root)
		for _, m := range p.waiting {
			s.request(m)
		}
	}
}

// heir returns the project of the open document at path, which publishes
// its diagnostics now, or nil when it isn't open.
func (s *Server) heir(path string) *project {
	d, ok := s.docs[path]
	if !ok {
		return nil
	}
	return s.projects[d.root]
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

// flush starts a load of every changed project.
func (s *Server) flush() {
	for _, root := range sortedKeys(s.projects) {
		s.start(s.projects[root])
	}
}

// start loads p again on a goroutine of its own, when a document changed
// since its last load started and no load of it is running, so the
// message loop goes on answering, shutdown and cancellations included,
// however long the load takes. [Server.finish] takes the result. A nil p
// is left alone, and nothing loads after shutdown.
func (s *Server) start(p *project) {
	if p == nil || !p.dirty || p.loading || s.state == shutDown {
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
	p.dirty, p.loading = false, true
	go s.background(loaded{versions: versions, owner: p, root: p.root}, overlay, s.done)
}

// background loads the project res names, with overlay, which nothing
// changes any more, and hands res, with the snapshot, to the message loop,
// or drops it when done is closed because Serve returned.
func (s *Server) background(res loaded, overlay map[string][]byte, done <-chan struct{}) {
	res.snap = s.load(res.root, overlay)
	select {
	case s.loads <- res:
	case <-done:
	}
}

// finish takes a finished load over: its project answers from it, shows
// what stopped the check, publishes its diagnostics, answers the
// requests that waited for it, and loads again when a document changed in
// the meantime. A load of a project dropped since is thrown away, and so is
// one of a project dropped and created again at the same root, whose own
// load is the one that counts: each project runs one load at a time, so a
// result always belongs to the project it was started for.
func (s *Server) finish(res loaded) {
	p := s.projects[res.root]
	if p == nil || p != res.owner {
		return
	}
	p.loading = false
	p.snap, p.versions, p.names = res.snap, res.versions, nil
	if p.snap.Err != nil {
		if msg := describe(p.snap.Err); msg != p.shown {
			p.shown = msg
			s.show(protocol.MessageError, msg)
		}
	} else {
		p.shown = ""
	}
	if s.state != shutDown {
		s.publish(p)
	}
	waiting := p.waiting
	p.waiting = nil
	for _, m := range waiting {
		s.request(m)
	}
	if p.dirty && s.timer == nil {
		s.start(p)
	}
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

// publish publishes p's diagnostics, file by file: those of every file p
// reports on, a list for every open document of p even when it's empty,
// and an empty list for every file it published before that has none now.
// A diagnostic without a file, which no document can show, goes to the
// log. An open document's list carries the version the load read.
func (s *Server) publish(p *project) {
	byURI := map[string][]protocol.Diagnostic{}
	paths := map[string]string{}
	owns := map[string]bool{}
	for _, e := range p.snap.Diagnostics {
		if e.File == "" {
			s.logf("%s", e.Error())
			continue
		}
		uri, path := s.uriOfName(e.File)
		if _, seen := owns[path]; !seen {
			owns[path] = s.reports(p, path)
		}
		if !owns[path] {
			continue
		}
		var src []byte
		if p.snap.Project != nil {
			src = p.snap.Project.SourceOf(e.File)
		}
		byURI[uri] = append(byURI[uri], diagnostic(e, newLines(src, s.encoding)))
		paths[uri] = path
	}
	for _, d := range s.docs {
		if d.root == p.root && byURI[d.uri] == nil {
			byURI[d.uri], paths[d.uri] = []protocol.Diagnostic{}, d.path
		}
	}
	for _, uri := range sortedKeys(byURI) {
		s.notify(protocol.MethodPublishDiagnostic, protocol.PublishDiagnosticsParams{URI: uri, Version: p.version(paths[uri]), Diagnostics: byURI[uri]})
	}
	for _, uri := range sortedKeys(p.published) {
		if _, still := byURI[uri]; !still {
			s.notify(protocol.MethodPublishDiagnostic, protocol.PublishDiagnosticsParams{URI: uri, Diagnostics: []protocol.Diagnostic{}})
		}
	}
	p.published = paths
}

// reports reports whether p publishes the diagnostics of the file at
// path. It does for its open documents. A declared project does for
// every file it read, apart from those below the root of another
// project, such as a directory with a configuration file of its own,
// which that project reports on; a file outside its root, such as a kind
// file its configuration names, is its own.
func (s *Server) reports(p *project, path string) bool {
	if d, ok := s.docs[path]; ok {
		return d.root == p.root
	}
	if !p.declared {
		return false
	}
	if !inside(path, p.root) {
		return true
	}
	return s.loader.Root(path, s.folders).Path == p.root
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

// current returns the open document uri names and its project, either
// nil when the document isn't open.
func (s *Server) current(uri string) (*document, *project) {
	d := s.open(uri)
	if d == nil {
		return nil, nil
	}
	return d, s.projects[d.root]
}

// nameOf returns the name p's latest load reads d's file by, or d's own
// path, slash-separated, when the load didn't read it.
func (p *project) nameOf(d *document) string {
	if p.names == nil {
		p.names = map[string]string{}
		for _, name := range p.snap.Project.SourceNames() {
			p.names[canonical(filepath.FromSlash(name))] = name
		}
	}
	if name, ok := p.names[d.canon]; ok {
		return name
	}
	return filepath.ToSlash(d.path)
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

// inside reports whether path is dir or below it.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
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
