package lsp

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// snippetCapabilities are the capabilities of a client whose completion
// takes snippets.
var snippetCapabilities = map[string]any{"textDocument": map[string]any{"completion": map[string]any{"completionItem": map[string]any{"snippetSupport": true}}}}

// TestCompletionSnippets checks that completions insert snippets only for
// a client that says it takes them, and plain text otherwise, and what
// the protocol's items carry: the preselected one and what each is.
func TestCompletionSnippets(t *testing.T) {
	src := "policy p: DeployApproval@2\n\nwhen cleared {\n  \n}\n"
	tests := []struct {
		name         string
		capabilities map[string]any
		want         string
		format       protocol.InsertTextFormat
	}{
		{name: "a client that takes snippets", capabilities: snippetCapabilities, want: "deny(reason: ${1:not_eligible})", format: protocol.Snippet},
		{name: "a client that doesn't", capabilities: map[string]any{}, want: "deny"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := start(t, &memLoader{files: testWorkspace(t)})
			s.wantNoError(s.call(protocol.MethodInitialize, map[string]any{"rootUri": "file://" + root, "capabilities": tt.capabilities}))
			s.notify(protocol.MethodInitialized, map[string]any{})
			s.notify(protocol.MethodDidOpen, s.opening("production.sigil", src))
			s.settle("production.sigil", 1)
			var list protocol.CompletionList
			s.decode(s.call(protocol.MethodCompletion, s.at("production.sigil", 3, 2)), &list)
			var deny *protocol.CompletionItem
			for i := range list.Items {
				if list.Items[i].Label == "deny" {
					deny = &list.Items[i]
				}
			}
			if deny == nil {
				t.Fatalf("didn't offer deny: %+v", list.Items)
			}
			if deny.TextEdit.NewText != tt.want || deny.InsertTextFormat != tt.format {
				t.Errorf("deny inserts %q as %d, want %q as %d", deny.TextEdit.NewText, deny.InsertTextFormat, tt.want, tt.format)
			}
			if deny.LabelDetails == nil || deny.LabelDetails.Description != "decision" {
				t.Errorf("label details = %+v, want the description decision", deny.LabelDetails)
			}
			var cond protocol.CompletionList
			s.decode(s.call(protocol.MethodCompletion, s.at("production.sigil", 2, 5)), &cond)
			if len(cond.Items) == 0 || !cond.Items[0].Preselect || cond.Items[0].Label != "release.hotfix" {
				t.Errorf("after when, first = %+v, want release.hotfix preselected", cond.Items[:min(1, len(cond.Items))])
			}
			s.shutdown()
		})
	}
}

// TestSignatureHelpRequest checks textDocument/signatureHelp over the
// protocol: the signature of the call at the position, and null outside
// every call.
func TestSignatureHelpRequest(t *testing.T) {
	s := start(t, &memLoader{files: testWorkspace(t)})
	s.initialize()
	src := "policy p: DeployApproval@2\n\nwhen split(environment, \n"
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", src))
	s.settle("production.sigil", 1)
	var help protocol.SignatureHelp
	s.decode(s.call(protocol.MethodSignatureHelp, s.at("production.sigil", 2, 24)), &help)
	if len(help.Signatures) != 1 || help.Signatures[0].Label != "split(string, string) -> list<string>" {
		t.Fatalf("signatures = %+v", help.Signatures)
	}
	if help.ActiveParameter == nil || *help.ActiveParameter != 1 || help.Signatures[0].Parameters[1].Label != [2]uint32{14, 20} {
		t.Errorf("active parameter = %v of %+v, want the second, 14 to 20", help.ActiveParameter, help.Signatures[0].Parameters)
	}
	s.wantResult(s.call(protocol.MethodSignatureHelp, s.at("production.sigil", 0, 3)), "null")
	s.wantResult(s.call(protocol.MethodSignatureHelp, s.at("nope.sigil", 0, 0)), "null")
	s.shutdown()
}

// TestCodeActions checks textDocument/codeAction: a diagnostic's quick
// fixes as its data carries them, none when the client asks for other
// kinds, and none once the document no longer holds what a fix replaces.
func TestCodeActions(t *testing.T) {
	s := start(t, &memLoader{files: testWorkspace(t)})
	s.initialize()
	src := "policy p: DeployApproval@2\n\nwhen servce.name == \"\" {\n  deny(reason: not_eligible)\n}\n"
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", src))
	var diagnostics []protocol.Diagnostic
	for _, n := range s.settle("production.sigil", 1) {
		var p protocol.PublishDiagnosticsParams
		if n.Method == protocol.MethodPublishDiagnostic && json.Unmarshal(n.Params, &p) == nil && p.URI == s.uri("production.sigil") {
			diagnostics = p.Diagnostics
		}
	}
	if len(diagnostics) != 1 || diagnostics[0].Data == nil {
		t.Fatalf("diagnostics = %+v, want one with fixes", diagnostics)
	}
	ask := func(only ...string) []protocol.CodeAction {
		var actions []protocol.CodeAction
		params := map[string]any{
			"textDocument": map[string]any{"uri": s.uri("production.sigil")},
			"range":        diagnostics[0].Range,
			"context":      map[string]any{"diagnostics": diagnostics, "only": only},
		}
		s.decode(s.call(protocol.MethodCodeAction, params), &actions)
		return actions
	}
	actions := ask()
	if len(actions) != 1 {
		t.Fatalf("actions = %+v, want one", actions)
	}
	a := actions[0]
	edits := a.Edit.Changes[s.uri("production.sigil")]
	if a.Title != "Change to `service`" || a.Kind != protocol.CodeActionQuickFix || !a.IsPreferred || len(edits) != 1 || edits[0].NewText != "service" {
		t.Errorf("action = %+v", a)
	}
	if got := ask(protocol.CodeActionQuickFix); len(got) != 1 {
		t.Errorf("asked for quick fixes, got %+v", got)
	}
	if got := ask("refactor"); len(got) != 0 {
		t.Errorf("asked for refactorings, got %+v", got)
	}
	s.notify(protocol.MethodDidChange, s.changing("production.sigil", 2, strings.Replace(src, "servce", "svc", 1)))
	if got := ask(); len(got) != 0 {
		t.Errorf("after the name changed, got %+v, want none", got)
	}
	s.shutdown()
}

// TestCodeActionsStale checks that a fix of a document edited since its
// diagnostics were published doesn't apply to a different construct at
// the same place: the constructor that missed a field is now another
// one, whose `)` is where the old one's was.
func TestCodeActionsStale(t *testing.T) {
	s := start(t, &memLoader{files: testWorkspace(t)}, WithDelay(time.Hour))
	s.initialize()
	src := "policy p: DeployApproval@2\n\nwhen true {\n  review(reason: service_owner)\n}\n"
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", src))
	var diagnostics []protocol.Diagnostic
	for _, n := range s.settle("production.sigil", 1) {
		var p protocol.PublishDiagnosticsParams
		if n.Method == protocol.MethodPublishDiagnostic && json.Unmarshal(n.Params, &p) == nil && p.URI == s.uri("production.sigil") {
			diagnostics = p.Diagnostics
		}
	}
	if len(diagnostics) != 1 || diagnostics[0].Data == nil {
		t.Fatalf("diagnostics = %+v, want the missing field with a fix", diagnostics)
	}
	s.notify(protocol.MethodDidChange, s.changing("production.sigil", 2, strings.Replace(src, "review(reason: service_owner)", "deny(reason: no_rule_matched)", 1)))
	var actions []protocol.CodeAction
	params := map[string]any{
		"textDocument": map[string]any{"uri": s.uri("production.sigil")},
		"range":        diagnostics[0].Range,
		"context":      map[string]any{"diagnostics": diagnostics},
	}
	s.decode(s.call(protocol.MethodCodeAction, params), &actions)
	if len(actions) != 0 {
		t.Errorf("after the constructor changed, got %+v, want none", actions)
	}
	s.shutdown()
}
