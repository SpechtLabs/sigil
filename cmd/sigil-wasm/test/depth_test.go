package wasmtest

import (
	"fmt"
	"strings"
	"testing"
)

// depthKind is a kind small enough to nest things in.
const depthKind = "kind K version 1\n\ninput x: int\n\ndecision allow {\n  reason: yes\n}\n\ncollect one\nprecedence allow\n\ndefault allow(reason: yes)\n"

// TestDeepNesting checks that a policy nested far past the parser's limit,
// 20,000 parentheses deep, is a diagnostic in every op that reads it,
// never a crash, and that the instance answers as before afterwards,
// however many times it's sent: the stack the module would otherwise
// overflow is the host's, and every overflow leaked memory until the
// instance trapped.
func TestDeepNesting(t *testing.T) {
	deep := "policy k.main: K@1\n\nwhen " + strings.Repeat("(", 20_000) + "x" + strings.Repeat(")", 20_000) + " > 1 {\n  allow(reason: yes)\n}\n"
	files := []map[string]string{{"path": "k.sigil", "source": depthKind}, {"path": "deep.sigil", "source": deep}}
	const want = `"message":"this expression nests more than 256 levels deep"`
	inst := newInstance(t, nil)
	for _, req := range []map[string]any{
		{"op": "check", "files": files},
		{"op": "compile", "files": files},
		{"op": "explain", "files": files},
		{"op": "format", "source": deep, "path": "deep.sigil"},
	} {
		if got := string(inst.Call(encode(t, req))); !strings.Contains(got, want) {
			t.Errorf("%s = %.300s, want the nesting diagnostic", req["op"], got)
		}
	}
	compile := encode(t, map[string]any{"op": "compile", "files": files})
	for range 300 {
		inst.Call(compile)
	}
	warm := inst.memory()
	for range 1000 {
		inst.Call(compile)
	}
	// The heap settles after a few hundred at a size a little above the
	// warm one; an overflow leaked megabytes each time, so two megabytes of
	// slack still catches it.
	if grown := inst.memory(); grown > warm+2<<20 {
		t.Errorf("memory grew from %d to %d bytes over 1,000 deep compiles", warm, grown)
	}
	shallow := []map[string]string{files[0], {"path": "p.sigil", "source": "policy k.main: K@1\n\nwhen x > 1 {\n  allow(reason: yes)\n}\n"}}
	h := inst.Request(map[string]any{"op": "compile", "files": shallow})["handle"]
	if resp := inst.Request(map[string]any{"op": "eval", "handle": h, "input": map[string]any{"x": 2}}); resp["ok"] != true || resp["reason"] != "yes" {
		t.Errorf("eval after the deep requests = %v", resp)
	}
}

// TestLongImportChain checks that a chain of 20,000 policies, each
// invoking the next, is a diagnostic at the import that makes it longer
// than the limit rather than a crash, and that a chain at the limit
// compiles and evaluates.
func TestLongImportChain(t *testing.T) {
	chain := func(n int) []map[string]string {
		files := make([]map[string]string, 0, n+1)
		files = append(files, map[string]string{"path": "k.sigil", "source": depthKind})
		for i := range n {
			src := fmt.Sprintf("policy k.p%d: K@1\n\n", i)
			if i+1 < n {
				src += fmt.Sprintf("use k.p%d\n\np%d()\n", i+1, i+1)
			} else {
				src += "when x > 1 {\n  allow(reason: yes)\n}\n"
			}
			files = append(files, map[string]string{"path": fmt.Sprintf("p%d.sigil", i), "source": src})
		}
		return files
	}
	inst := newInstance(t, nil)
	got := string(inst.Call(encode(t, map[string]any{"op": "compile", "files": chain(20_000), "policy": "k.p0"})))
	if want := `"message":"imports nest more than 64 levels deep: k.p19935 starts a chain of 65 documents, each importing the next"`; !strings.HasPrefix(got, `{"ok":false`) || !strings.Contains(got, want) {
		t.Errorf("compile = %.400s, want it to fail with %s", got, want)
	}
	h := inst.Request(map[string]any{"op": "compile", "files": chain(64), "policy": "k.p0"})["handle"]
	if resp := inst.Request(map[string]any{"op": "eval", "handle": h, "input": map[string]any{"x": 2}}); resp["ok"] != true || resp["reason"] != "yes" {
		t.Errorf("eval of a chain at the limit = %v", resp)
	}
}
