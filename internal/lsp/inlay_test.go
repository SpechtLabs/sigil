package lsp

import (
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/lsp/protocol"
)

// TestHints checks the inlay hints of a document: the type after each
// let's name and each variable's, in source order, and only in the range
// asked for.
func TestHints(t *testing.T) {
	src := head + "let a = service.owners\nlet b = filter o in a: o != \"\"\nlet broken = nope\n\nwhen any c in changes: c.hotfix {\n  let d = environment\n  deny(reason: not_eligible)\n}\n"
	v, _ := viewOf(t, "production.sigil", src)
	var got []string
	for _, h := range v.hints(0, len(src)) {
		got = append(got, src[strings.LastIndexAny(src[:h.offset], " \n")+1:h.offset]+h.label)
	}
	want := []string{"a: list<string>", "b: list<string>", "o: string", "c: Release", "d: string"}
	if !slices.Equal(got, want) {
		t.Errorf("hints = %q, want %q", got, want)
	}
	from := strings.Index(src, "let b")
	if got := v.hints(from, from+20); len(got) != 2 {
		t.Errorf("hints in the range of b = %+v, want b's and o's", got)
	}
}

// TestInlayHintRequest checks textDocument/inlayHint over the protocol.
func TestInlayHintRequest(t *testing.T) {
	s := start(t, &memLoader{files: testWorkspace(t)})
	s.initialize()
	s.notify(protocol.MethodDidOpen, s.opening("production.sigil", "policy p: DeployApproval@2\n\nlet a = 1\n"))
	s.settle("production.sigil", 1)
	var hints []protocol.InlayHint
	params := map[string]any{"textDocument": map[string]any{"uri": s.uri("production.sigil")}, "range": map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 3, "character": 0}}}
	s.decode(s.call(protocol.MethodInlayHint, params), &hints)
	if len(hints) != 1 || hints[0].Label != ": int" || hints[0].Position != (protocol.Position{Line: 2, Character: 5}) || hints[0].Kind != protocol.InlayHintType {
		t.Errorf("hints = %+v", hints)
	}
	params["textDocument"] = map[string]any{"uri": s.uri("nope.sigil")}
	s.wantResult(s.call(protocol.MethodInlayHint, params), "[]")
	s.shutdown()
}

// TestHintsInAModule checks the hints of a module's lets.
func TestHintsInAModule(t *testing.T) {
	src := "module deploy.common: DeployApproval@2\n\npub let owns_service = actor.teams any in service.owners\npub let cleared = true\n"
	v, _ := viewOf(t, "common.sigil", src)
	if got := v.hints(0, len(src)); len(got) != 2 || got[0].label != ": bool" || got[1].label != ": bool" {
		t.Errorf("hints = %+v, want two bools", got)
	}
}
