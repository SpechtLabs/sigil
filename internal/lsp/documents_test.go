package lsp

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"

	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/lsp/jsonrpc"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// broken is a version of production.sigil with two errors.
const broken = "policy payments.production: DeployApproval@2\n\nwhen nope {\n  deny(reason: nope)\n}\n"

// fixed is a version of production.sigil without errors, but with a lint
// finding: a let nothing reads.
const fixed = "policy payments.production: DeployApproval@2\n\nlet unused = 1\n\nwhen true {\n  deny(reason: not_eligible)\n}\n"

// TestDiagnostics follows the diagnostics a session publishes as
// documents open, change and close.
func TestDiagnostics(t *testing.T) {
	s := start(t, &memLoader{files: testWorkspace(t)})
	s.initialize()
	prod := s.uri("production.sigil")

	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", broken))
	s.settle("production.sigil", 1)
	got := s.published(prod)
	want := []string{
		"2:5-2:9 error: unknown name `nope`\nhelp: names come from the kind's inputs, host functions, decisions and enum values, and the document's params, lets and imports",
		"3:15-3:19 error: decision deny has no reason `nope`\nhelp: deny declares: not_eligible, soak_too_short, no_rule_matched",
	}
	if got.Version == nil || *got.Version != 1 || !slices.Equal(render(got.Diagnostics), want) {
		t.Errorf("after the open, published %v (version %v), want %v (version 1)", render(got.Diagnostics), got.Version, want)
	}

	s.notify(protocol.MethodDidChange, s.changing("production.sigil", 2, fixed))
	s.settle("production.sigil", 2)
	got = s.published(prod)
	want = []string{"2:4-2:10 warning unused-let: let unused is never read\nhelp: remove the let, or read it; a private let can't be imported"}
	if got.Version == nil || *got.Version != 2 || !slices.Equal(render(got.Diagnostics), want) {
		t.Errorf("after the change, published %v (version %v), want %v (version 2)", render(got.Diagnostics), got.Version, want)
	}

	s.notify(protocol.MethodDidClose, map[string]any{"textDocument": map[string]any{"uri": prod}})
	s.sync("production.sigil")
	if got = s.published(prod); len(got.Diagnostics) != 0 {
		t.Errorf("after the close, published %v, want none", render(got.Diagnostics))
	}
	s.shutdown()
}

// TestDiagnosticsInOtherFiles checks that an edit that breaks another
// file publishes that file's diagnostics, and that fixing it clears them.
func TestDiagnosticsInOtherFiles(t *testing.T) {
	s := start(t, &memLoader{files: testWorkspace(t)})
	s.initialize()
	common := strings.Replace(string(testWorkspace(t)[root+"/common.sigil"]), "pub let cleared", "pub let gone", 1)
	s.notify(protocol.MethodDidOpen, s.opening("common.sigil", common))
	s.settleOn("production.sigil")
	got := s.published(s.uri("production.sigil"))
	if got.Version != nil || len(got.Diagnostics) == 0 || !strings.Contains(got.Diagnostics[0].Message, "deploy.common has no pub let `cleared`") {
		t.Errorf("published %v (version %v) for production.sigil, want the missing let without a version", render(got.Diagnostics), got.Version)
	}
	s.notify(protocol.MethodDidChange, s.changing("common.sigil", 2, string(testWorkspace(t)[root+"/common.sigil"])))
	s.settleOn("production.sigil")
	if got = s.published(s.uri("production.sigil")); len(got.Diagnostics) != 0 {
		t.Errorf("after the fix, published %v for production.sigil, want none", render(got.Diagnostics))
	}
	s.shutdown()
}

// TestLoadErrors checks that what stops a load is shown to the user
// once, until it changes, and that the project still completes.
func TestLoadErrors(t *testing.T) {
	l := &memLoader{files: testWorkspace(t), err: humane.New("sigil.yaml: unknown key", "check the key")}
	s := start(t, l)
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", "policy p: DeployApproval@2\n\nwhen "))
	s.settle("production.sigil", 1)
	s.notify(protocol.MethodDidChange, s.changing("production.sigil", 2, "policy p: DeployApproval@2\n\nwhen s"))
	s.settle("production.sigil", 2)
	var shown []string
	for _, n := range s.notes {
		if n.Method == protocol.MethodShowMessage {
			var p protocol.ShowMessageParams
			_ = json.Unmarshal(n.Params, &p)
			shown = append(shown, p.Message)
		}
	}
	if want := []string{"sigil.yaml: unknown key\nhelp: check the key"}; !slices.Equal(shown, want) {
		t.Errorf("showed %q, want %q", shown, want)
	}
	var list protocol.CompletionList
	s.decode(s.call(protocol.MethodCompletion, s.at("production.sigil", 2, 6)), &list)
	if len(list.Items) != 2 || list.Items[0].Label != "service" || list.Items[1].Label != "split" {
		t.Errorf("completed %+v, want service and split", list.Items)
	}
	s.shutdown()
}

// TestReloads checks what loads a project again: a save, files changed on
// disk, and changed workspace folders.
func TestReloads(t *testing.T) {
	l := &memLoader{files: testWorkspace(t)}
	s := start(t, l)
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", fixed))
	s.settle("production.sigil", 1)
	tests := []struct {
		name   string
		method string
		params any
	}{
		{name: "a save with its text", method: protocol.MethodDidSave, params: map[string]any{"textDocument": map[string]any{"uri": s.uri("production.sigil")}, "text": broken}},
		{name: "a save", method: protocol.MethodDidSave, params: map[string]any{"textDocument": map[string]any{"uri": s.uri("production.sigil")}}},
		{name: "files changed on disk", method: protocol.MethodDidChangeWatched, params: map[string]any{"changes": []any{}}},
		{name: "folders changed", method: protocol.MethodDidChangeFolders, params: map[string]any{"event": map[string]any{
			"added":   []any{map[string]any{"uri": "file:///elsewhere", "name": "x"}, map[string]any{"uri": "file:///ws", "name": "ws"}},
			"removed": []any{map[string]any{"uri": "file:///gone", "name": "y"}},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := l.count()
			s.notify(tt.method, tt.params)
			s.settle("production.sigil", 1)
			if l.count() != before+1 {
				t.Errorf("loaded %d times, want once", l.count()-before)
			}
		})
	}
	got := s.published(s.uri("production.sigil"))
	if len(got.Diagnostics) != 2 {
		t.Errorf("published %v, want the errors of the saved text", render(got.Diagnostics))
	}
	s.shutdown()
}

// TestFolderChangeDropsProjects checks that a project whose documents
// move to another root is dropped, and that the new project replaces its
// diagnostics without an empty list in between.
func TestFolderChangeDropsProjects(t *testing.T) {
	l := &folderLoader{memLoader{files: testWorkspace(t)}}
	s := start(t, l)
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", broken))
	s.settle("production.sigil", 1)
	s.notify(protocol.MethodDidChangeFolders, map[string]any{"event": map[string]any{"added": []any{}, "removed": []any{map[string]any{"uri": "file:///ws", "name": "ws"}}}})
	noFlicker(t, s, "production.sigil")
	s.shutdown()
}

// TestNotificationsThatDoNothing sends notifications the server ignores,
// and logs: for documents that aren't open, or aren't files, and a loader
// that panics finding a root.
func TestNotificationsThatDoNothing(t *testing.T) {
	s := start(t, &rootPanics{memLoader{files: testWorkspace(t)}})
	s.initialize()
	missing := map[string]any{"textDocument": map[string]any{"uri": s.uri("missing.sigil"), "version": 2}, "contentChanges": []any{}}
	for _, n := range []struct {
		method string
		params any
	}{
		{protocol.MethodDidOpen, map[string]any{"textDocument": map[string]any{"uri": "untitled:Untitled-1", "text": "x", "version": 1}}},
		{protocol.MethodDidChange, missing},
		{protocol.MethodDidClose, missing},
		{protocol.MethodDidSave, missing},
		{protocol.MethodDidClose, map[string]any{"textDocument": 1}},
		{protocol.MethodDidSave, map[string]any{"textDocument": 1}},
		{protocol.MethodDidChange, map[string]any{"textDocument": 1}},
		{protocol.MethodDidChangeFolders, map[string]any{"event": 1}},
		{protocol.MethodDidChangeWatched, map[string]any{"changes": 1}},
		{protocol.MethodDidOpen, s.opening("production.sigil", fixed)},
	} {
		s.notify(n.method, n.params)
	}
	s.wantResult(s.call(protocol.MethodHover, s.at("production.sigil", 0, 0)), "null")
	s.shutdown()
	for _, want := range []string{"only file: documents are checked", "missing.sigil isn't open", "the params don't fit the method", "textDocument/didOpen panicked: no root"} {
		if !strings.Contains(s.log.String(), want) {
			t.Errorf("the log doesn't hold %q:\n%s", want, s.log.String())
		}
	}
}

// TestLoaderFailures checks a session whose loader panics, or loads
// nothing: requests still answer, with what the tokens alone offer.
func TestLoaderFailures(t *testing.T) {
	tests := []struct {
		name   string
		loader Loader
		log    string
	}{
		{name: "a loader that panics", loader: loadPanics{}, log: "loading /ws panicked: no load"},
		{name: "a loader that loads nothing", loader: loadsNothing{}},
		{name: "a project without the file", loader: loadsNothing{snap: &Snapshot{Project: (&memLoader{files: map[string][]byte{}}).Load(root, nil).Project}}},
		{name: "a diagnostic without a file", loader: loadsNothing{snap: &Snapshot{Diagnostics: diag.ErrorList{{Msg: "bundle has no policy x"}}}}, log: "bundle has no policy x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := start(t, tt.loader)
			s.initialize()
			s.notify(protocol.MethodDidOpen, s.opening("production.sigil", "policy p: DeployApproval@2\n\nwh"))
			var list protocol.CompletionList
			s.decode(s.call(protocol.MethodCompletion, s.at("production.sigil", 2, 2)), &list)
			if len(list.Items) != 1 || list.Items[0].Label != "when" {
				t.Errorf("completed %+v, want when", list.Items)
			}
			s.wantResult(s.call(protocol.MethodHover, s.at("production.sigil", 0, 12)), "null")
			s.wantResult(s.call(protocol.MethodDefinition, s.at("production.sigil", 0, 12)), "null")
			s.shutdown()
			if !strings.Contains(s.log.String(), tt.log) {
				t.Errorf("the log doesn't hold %q:\n%s", tt.log, s.log.String())
			}
		})
	}
}

// TestRequests answers completion, hover, definition and formatting
// through a session, in both position encodings.
func TestRequests(t *testing.T) {
	src := "policy payments.production: DeployApproval@2\n\nuse deploy.common.{cleared}\n\n// é😀\nwhen cleared {\n  deny(reason: soak_too_short)\n}\n"
	for _, encoding := range []string{protocol.EncodingUTF16, protocol.EncodingUTF8} {
		t.Run(encoding, func(t *testing.T) {
			s := start(t, &memLoader{files: testWorkspace(t)})
			s.wantNoError(s.call(protocol.MethodInitialize, map[string]any{"capabilities": map[string]any{"general": map[string]any{"positionEncodings": []string{encoding}}}}))
			s.notify(protocol.MethodDidOpen, s.opening("production.sigil", src))

			var list protocol.CompletionList
			s.decode(s.call(protocol.MethodCompletion, s.at("production.sigil", 5, 9)), &list)
			if len(list.Items) != 1 || list.Items[0].Label != "cleared" || list.Items[0].TextEdit.Range.Start.Character != 5 {
				t.Errorf("completed %+v, want cleared from character 5", list.Items)
			}
			var hover protocol.Hover
			s.decode(s.call(protocol.MethodHover, s.at("production.sigil", 6, 4)), &hover)
			if !strings.Contains(hover.Contents.Value, "decision deny {") || hover.Range.Start != (protocol.Position{Line: 6, Character: 2}) {
				t.Errorf("hover = %+v", hover)
			}
			var locs []protocol.Location
			s.decode(s.call(protocol.MethodDefinition, s.at("production.sigil", 5, 7)), &locs)
			if len(locs) != 1 || locs[0].URI != s.uri("common.sigil") || locs[0].Range.Start != (protocol.Position{Line: 3, Character: 8}) {
				t.Errorf("definition = %+v", locs)
			}
			s.wantResult(s.call(protocol.MethodFormatting, s.formattingOf("production.sigil")), "[]")
			s.shutdown()
		})
	}
}

// TestFormatting formats documents: one that needs it, one that doesn't
// parse, and one that isn't open, which gets null.
func TestFormatting(t *testing.T) {
	s := start(t, &memLoader{files: testWorkspace(t)})
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", "policy p: DeployApproval@2\nlet   x =1\n"))
	var edits []protocol.TextEdit
	s.decode(s.call(protocol.MethodFormatting, s.formattingOf("production.sigil")), &edits)
	if len(edits) != 1 || edits[0].NewText != "policy p: DeployApproval@2\n\nlet x = 1\n" || edits[0].Range.End != (protocol.Position{Line: 2}) {
		t.Errorf("edits = %+v", edits)
	}
	s.notify(protocol.MethodDidOpen, s.opening("common.sigil", "module m: DeployApproval@2\nlet = \n"))
	m := s.call(protocol.MethodFormatting, s.formattingOf("common.sigil"))
	s.wantError(m, jsonrpc.RequestFailed)
	if m.Error != nil && !strings.Contains(m.Error.Message, "doesn't parse") {
		t.Errorf("error = %s", m.Error.Message)
	}
	s.wantResult(s.call(protocol.MethodFormatting, s.formattingOf("guardrails.sigil")), "null")
	s.wantError(s.call(protocol.MethodFormatting, map[string]any{"textDocument": 1}), jsonrpc.InvalidParams)
	s.shutdown()
}

// sync waits for the server to finish with the notifications before it,
// with a hover on file, which it answers after them.
func (s *session) sync(file string) {
	s.t.Helper()
	s.call(protocol.MethodHover, s.at(file, 0, 0))
}

// published returns the last diagnostics the server published for uri,
// or none.
func (s *session) published(uri string) protocol.PublishDiagnosticsParams {
	s.t.Helper()
	var out protocol.PublishDiagnosticsParams
	for _, n := range s.notes {
		var p protocol.PublishDiagnosticsParams
		if n.Method != protocol.MethodPublishDiagnostic {
			continue
		}
		if err := json.Unmarshal(n.Params, &p); err != nil {
			s.t.Fatal(err)
		}
		if p.URI == uri {
			out = p
		}
	}
	return out
}

// changing returns the params of a full didChange of file to text.
func (s *session) changing(file string, version int, text string) map[string]any {
	return map[string]any{
		"textDocument":   map[string]any{"uri": s.uri(file), "version": version},
		"contentChanges": []any{map[string]any{"text": text}},
	}
}

// formattingOf returns the params of a formatting request for file.
func (s *session) formattingOf(file string) map[string]any {
	return map[string]any{"textDocument": map[string]any{"uri": s.uri(file)}, "options": map[string]any{"tabSize": 2}}
}

// render renders diagnostics as range, severity, lint and message.
func render(diags []protocol.Diagnostic) []string {
	out := make([]string, len(diags))
	for i, d := range diags {
		severity := "error"
		if d.Severity == protocol.SeverityWarning {
			severity = "warning " + d.Code
		}
		r := d.Range
		out[i] = fmt.Sprintf("%d:%d-%d:%d %s: %s", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character, severity, d.Message)
	}
	return out
}

// folderLoader roots a file at the first folder holding it, or at the
// file itself.
type folderLoader struct{ memLoader }

// Root returns the first folder holding path, or path.
func (l *folderLoader) Root(path string, folders []string) Root {
	for _, f := range folders {
		if strings.HasPrefix(path, f+"/") {
			return Root{Path: f}
		}
	}
	return Root{Path: path}
}

// rootPanics panics finding a root.
type rootPanics struct{ memLoader }

// Root panics.
func (*rootPanics) Root(string, []string) Root { panic(errors.New("no root")) }

// loadPanics panics loading.
type loadPanics struct{}

// Root returns the workspace's root.
func (loadPanics) Root(string, []string) Root { return Root{Path: root} }

// Load panics.
func (loadPanics) Load(string, map[string][]byte) *Snapshot { panic(errors.New("no load")) }

// Changed does nothing.
func (loadPanics) Changed([]string) {}

// loadsNothing loads snap, or nil.
type loadsNothing struct{ snap *Snapshot }

// Root returns the workspace's root.
func (loadsNothing) Root(string, []string) Root { return Root{Path: root} }

// Load returns snap.
func (l loadsNothing) Load(string, map[string][]byte) *Snapshot { return l.snap }

// Changed does nothing.
func (loadsNothing) Changed([]string) {}

// TestDelay checks the delay before a reload: changes within it don't load
// the project, and a request in the meantime answers from the latest text
// by checking the edited document alone against the last load.
func TestDelay(t *testing.T) {
	l := &memLoader{files: testWorkspace(t)}
	s := start(t, l, WithDelay(time.Hour))
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", fixed))
	s.notify(protocol.MethodDidChange, s.changing("production.sigil", 2, "policy p: DeployApproval@2\n\nwhen "))
	s.notify(protocol.MethodDidChange, s.changing("production.sigil", 3, "policy p: DeployApproval@2\n\nwhen serv"))
	var list protocol.CompletionList
	s.decode(s.call(protocol.MethodCompletion, s.at("production.sigil", 2, 9)), &list)
	if len(list.Items) != 1 || list.Items[0].Label != "service" {
		t.Errorf("completed %+v, want service", list.Items)
	}
	if l.count() != 1 {
		t.Errorf("loaded %d times, want once: at the open; the request checks the edited document alone", l.count())
	}
	s.shutdown()
}
