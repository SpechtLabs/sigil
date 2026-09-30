package wasmtest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
)

// The fixtures: the CLI's eval testdata, and the deploy-gates example's
// policy repository.
const (
	evalData = root + "/cmd/sigil/command/eval/testdata"
	gates    = root + "/examples/deploy-gates/policies"
)

// TestABI checks the module's shape: a reactor that exports the four
// functions of ABI version 1 and imports only WASI and sigil.host_call.
func TestABI(t *testing.T) {
	inst := newInstance(t, nil)
	res, err := inst.mod.ExportedFunction("sigil_abi_version").Call(inst.ctx)
	if err != nil || res[0] != 1 {
		t.Errorf("sigil_abi_version() = %v, %v, want 1", res, err)
	}
	code, err := inst.rt.CompileModule(inst.ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	exports := code.ExportedFunctions()
	for _, name := range []string{"_initialize", "sigil_abi_version", "sigil_alloc", "sigil_free", "sigil_call"} {
		if _, ok := exports[name]; !ok {
			t.Errorf("the module doesn't export %s", name)
		}
	}
	if _, ok := exports["_start"]; ok {
		t.Error("the module exports _start; a reactor doesn't")
	}
	for _, f := range code.ImportedFunctions() {
		module, name, _ := f.Import()
		if module != "wasi_snapshot_preview1" && (module != "sigil" || name != "host_call") {
			t.Errorf("the module imports %s.%s", module, name)
		}
	}
}

// TestSize reports the module's size, raw and compressed, as a browser
// downloads it.
func TestSize(t *testing.T) {
	var gz bytes.Buffer
	w, _ := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	_, _ = w.Write(wasm)
	_ = w.Close()
	t.Logf("sigil.wasm: %.1f MiB, %.1f MiB gzipped", float64(len(wasm))/(1<<20), float64(gz.Len())/(1<<20))
}

// TestOps sends every op through the ABI, and checks each response
// equals, byte for byte, the engine's natively, and says what it should.
func TestOps(t *testing.T) {
	access := files(t, evalData+"/access.sigil", evalData+"/access")
	tests := []struct {
		name string
		reqs []map[string]any // sent in order to one instance; the last one's response is checked
		want []string
	}{
		{name: "check", reqs: []map[string]any{{"op": "check", "files": access}}, want: []string{`{"ok":true,"diagnostics":[]}`}},
		{name: "check with requirements and lints", reqs: []map[string]any{{"op": "check", "files": files(t, gates), "require": []map[string]any{
			{"policy": "deploy.guardrails", "trusted": []string{gates + "/platform/deploy"}, "roots": []string{"payments.*", "checkout.*"}},
		}, "lints": map[string]string{"path-matches-name": "error"}}}, want: []string{`"ok":true`}},
		{name: "check that fails", reqs: []map[string]any{{"op": "check", "files": append(slices.Clone(access), map[string]string{"path": "x.sigil", "source": "policy x: Access@1\nwhen user.nmae {\n}\n"})}}, want: []string{`"severity":"error","file":"x.sigil","document":"x"`}},
		{name: "compile", reqs: []map[string]any{{"op": "compile", "files": access, "id": 1}}, want: []string{`{"id":1,"ok":true,"policy":"access.main","diagnostics":[],"handle":1}`}},
		{name: "compile that fails", reqs: []map[string]any{{"op": "compile", "files": access, "policy": "nope"}}, want: []string{`"ok":false`, `no policy matches \"nope\"`}},
		{name: "eval", reqs: []map[string]any{{"op": "compile", "files": access}, {"op": "eval", "handle": 1, "input": fixture(t, evalData+"/inputs/admin.json")}}, want: []string{`"decision":"allow","reason":"admin"`}},
		{name: "eval of a conflict", reqs: []map[string]any{{"op": "compile", "files": access}, {"op": "eval", "handle": 1, "input": fixture(t, evalData+"/inputs/conflict.json")}}, want: []string{`"kind":"conflict"`}},
		{name: "explain by handle", reqs: []map[string]any{{"op": "compile", "files": access}, {"op": "explain", "handle": 1}}, want: []string{`{"ok":true,"explanations":[{"policy":"access.main"`}},
		{name: "explain files", reqs: []map[string]any{{"op": "explain", "files": files(t, gates)}}, want: []string{`"policy":"access.main"`, `"policy":"payments.production"`}},
		{name: "format", reqs: []map[string]any{{"op": "format", "source": "policy   a.b:K@1"}}, want: []string{`{"ok":true,"source":"policy a.b: K@1\n","formatted":false}`}},
		{name: "format that fails", reqs: []map[string]any{{"op": "format", "source": "when {", "path": "x.sigil"}}, want: []string{`"message":"x.sigil has syntax errors"`, `"diagnostics":[{"severity":"error","file":"x.sigil"`}},
		{name: "release", reqs: []map[string]any{{"op": "compile", "files": access}, {"op": "release", "handle": 1}}, want: []string{`{"ok":true}`}},
		{name: "eval after release", reqs: []map[string]any{{"op": "compile", "files": access}, {"op": "release", "handle": 1}, {"op": "eval", "handle": 1, "input": map[string]any{}}}, want: []string{`"message":"no compiled policy has handle 1"`}},
		{name: "unknown op", reqs: []map[string]any{{"op": "run"}}, want: []string{`unknown op \"run\"`}},
		{name: "unknown field", reqs: []map[string]any{{"op": "version", "verbose": true}}, want: []string{`unknown field \"verbose\"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newInstance(t, nil)
			native := newNative()
			var got, want []byte
			for _, req := range tt.reqs {
				got, want = inst.Call(encode(t, req)), native.Call(encode(t, req))
			}
			if !bytes.Equal(got, want) {
				t.Errorf("the module answered\n%s\nwhere the engine answers natively\n%s", got, want)
			}
			for _, w := range tt.want {
				if !strings.Contains(string(got), w) {
					t.Errorf("response = %s, want it to contain %s", got, w)
				}
			}
		})
	}
}

// TestVersion checks the version op reports the module's build.
func TestVersion(t *testing.T) {
	resp := newInstance(t, nil).Request(map[string]any{"op": "version"})
	if resp["ok"] != true || resp["platform"] != "wasip1/wasm" || !strings.HasPrefix(fmt.Sprint(resp["goVersion"]), "go1.") {
		t.Errorf("version = %v", resp)
	}
}

// TestHostFunctions checks a host function call's round trip through
// sigil.host_call: the request the module sends, the result the host
// answers with, and every way the host can get the answer wrong.
func TestHostFunctions(t *testing.T) {
	access := files(t, evalData+"/access.sigil", evalData+"/access")
	vault := fixture(t, evalData+"/inputs/vault.json") // ada asks for vault, which owner(resource) must say ada owns
	tests := []struct {
		name  string
		host  func(req []byte) []byte
		raw   func(inst *instance) func(req []byte) uint64
		stubs map[string]any
		want  []string
	}{
		{name: "result", host: answer(`{"result": "ada"}`), want: []string{`"decision":"allow","reason":"team_member"`, `"payload":{"scopes":[],"ttl":"15m"}`}},
		{name: "another result", host: answer(`{"result": "bob"}`), want: []string{`"decision":"deny","reason":"no_rule_matched"`, `"trace":[]`}},
		{name: "error", host: answer(`{"error": "directory unavailable"}`), want: []string{`"kind":"runtime"`, `host function owner failed: directory unavailable`}},
		{name: "wrong type", host: answer(`{"result": 1}`), want: []string{`host function owner failed: the host returned result: expected a string`}},
		{name: "not JSON", host: answer(`nope`), want: []string{`host function owner failed: the host's answer isn't a JSON object`}},
		{name: "memory it didn't allocate", raw: func(*instance) func([]byte) uint64 {
			return func([]byte) uint64 { return 8<<32 | 4 }
		}, want: []string{`host function owner failed: host_call returned memory it didn't allocate with sigil_alloc`}},
		{name: "longer than it allocated", raw: func(inst *instance) func([]byte) uint64 {
			return func([]byte) uint64 { return inst.write(inst.ctx, inst.mod, []byte(`{"result": "ada"}`)) + 100 }
		}, want: []string{`host_call returned memory it didn't allocate with sigil_alloc`}},
		{name: "stub over the host", host: answer(`{"result": "bob"}`), stubs: map[string]any{"owner": map[string]any{"returns": "ada"}}, want: []string{`"reason":"team_member"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var calls [][]byte
			inst := newInstance(t, func(req []byte) []byte {
				calls = append(calls, req)
				return tt.host(req)
			})
			if tt.raw != nil {
				inst.hostRaw = tt.raw(inst)
			}
			compiled := inst.Request(map[string]any{"op": "compile", "files": access, "functions": []string{"owner"}, "stubs": tt.stubs})
			got := string(inst.Call(encode(t, map[string]any{"op": "eval", "handle": compiled["handle"], "input": vault})))
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("eval = %s, want it to contain %s", got, w)
				}
			}
			if tt.host != nil && tt.stubs == nil && tt.raw == nil {
				if len(calls) != 1 || string(calls[0]) != `{"function":"owner","args":["vault"]}` {
					t.Errorf("host_call got %q, want one call of owner(vault)", calls)
				}
			}
		})
	}
}

// TestTimeout checks that the deadline stops an evaluation in the
// module, where no timer can fire while it runs.
func TestTimeout(t *testing.T) {
	inst := newInstance(t, nil)
	kind := "kind K version 1\n\ninput names: list<string>\n\ndecision allow {\n  reason: found\n}\n\ncollect one\nprecedence allow\n\ndefault allow(reason: found)\n"
	policy := "policy k.main: K@1\n\nwhen any a in names: any b in names: a != b and b == \"never\" {\n  allow(reason: found)\n}\n"
	compiled := inst.Request(map[string]any{"op": "compile", "files": []map[string]string{{"path": "k.sigil", "source": kind}, {"path": "main.sigil", "source": policy}}})
	names := make([]string, 20_000) // 400 million comparisons: minutes, without the deadline
	for i := range names {
		names[i] = fmt.Sprint(i)
	}
	resp := inst.Request(map[string]any{"op": "eval", "handle": compiled["handle"], "input": map[string]any{"names": names}, "timeout_ms": 50})
	failure, _ := resp["error"].(map[string]any)
	if failure["kind"] != "canceled" || resp["decision"] != "allow" {
		t.Errorf("eval = %v, want a canceled evaluation with the default", resp)
	}
}

// TestMalformedCall checks that a request the host didn't allocate is an
// answer, not a trap.
func TestMalformedCall(t *testing.T) {
	inst := newInstance(t, nil)
	packed, err := inst.call.Call(inst.ctx, 16, 4)
	if err != nil {
		t.Fatalf("sigil_call(16, 4): %v", err)
	}
	resp, _ := inst.mod.Memory().Read(uint32(packed[0]>>32), uint32(packed[0]))
	if !strings.Contains(string(resp), "the request isn't in memory from sigil_alloc") {
		t.Errorf("sigil_call(16, 4) = %s", resp)
	}
	// A double free, or a free of an address sigil_alloc didn't return,
	// does nothing.
	inst.freeBuf(uint32(packed[0]>>32), uint32(packed[0]))
	inst.freeBuf(uint32(packed[0]>>32), uint32(packed[0]))
	inst.freeBuf(12, 1)
	if resp := inst.Request(map[string]any{"op": "version"}); resp["ok"] != true {
		t.Errorf("version after bad frees = %v", resp)
	}
}

// TestMemory checks that memory the host frees is reclaimed: 10,000
// evaluations, and compiles each released, don't grow the module's
// memory once it's warm, while the same evaluations without frees do.
func TestMemory(t *testing.T) {
	access := files(t, evalData+"/access.sigil", evalData+"/access")
	admin := fixture(t, evalData+"/inputs/admin.json")
	inst := newInstance(t, nil)
	compiled := inst.Request(map[string]any{"op": "compile", "files": access})
	eval := encode(t, map[string]any{"op": "eval", "handle": compiled["handle"], "input": admin})
	cycle := func(n int) {
		for range n {
			inst.Call(eval)
			h := inst.Request(map[string]any{"op": "compile", "files": access})["handle"]
			inst.Request(map[string]any{"op": "release", "handle": h})
		}
	}
	cycle(1_000)
	warm := inst.memory()
	for range 10 {
		cycle(1_000)
	}
	if grown := inst.memory(); grown > warm {
		t.Errorf("memory grew from %d to %d bytes over 10,000 evaluations", warm, grown)
	}

	// The control: responses never freed stay reachable, and memory grows.
	leaky := newInstance(t, nil)
	h := leaky.Request(map[string]any{"op": "compile", "files": access})["handle"]
	eval = encode(t, map[string]any{"op": "eval", "handle": h, "input": admin})
	before := leaky.memory()
	for range 10_000 {
		res, _ := leaky.alloc.Call(leaky.ctx, uint64(len(eval)))
		leaky.mod.Memory().Write(uint32(res[0]), eval)
		if _, err := leaky.call.Call(leaky.ctx, res[0], uint64(len(eval))); err != nil {
			t.Fatal(err)
		}
	}
	if after := leaky.memory(); after <= before {
		t.Errorf("memory stayed at %d bytes with no buffer freed; the test can't tell a leak", after)
	}
}

// answer returns a host that answers every call with resp.
func answer(resp string) func([]byte) []byte {
	return func([]byte) []byte { return []byte(resp) }
}

// files returns the files at the paths, and below each directory among
// them, as the CLI reads them: a directory's files in sorted order, before
// its subdirectories', each named by its cleaned path.
func files(t testing.TB, paths ...string) []map[string]string {
	t.Helper()
	var out []map[string]string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			out = append(out, file(t, p))
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			t.Fatal(err)
		}
		var names, dirs []string
		for _, e := range entries {
			switch {
			case strings.HasPrefix(e.Name(), "."):
			case e.IsDir():
				dirs = append(dirs, filepath.Join(p, e.Name()))
			case strings.HasSuffix(e.Name(), ".sigil"):
				names = append(names, filepath.Join(p, e.Name()))
			}
		}
		sort.Strings(names)
		for _, n := range names {
			out = append(out, file(t, n))
		}
		out = append(out, files(t, dirs...)...)
	}
	return out
}

func file(t testing.TB, name string) map[string]string {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{"path": filepath.ToSlash(filepath.Clean(name)), "source": string(src)}
}

// fixture reads a JSON input.
func fixture(t testing.TB, name string) json.RawMessage {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return src
}

// TestRequire checks compile's requirements through the module, with the
// platform's documents as trusted files: the team bundle must invoke the
// required policy at its top level, with arguments in its params'
// bounds, and must not redefine it.
func TestRequire(t *testing.T) {
	kind := file(t, gates+"/deploy_approval.sigil")
	trusted := files(t, gates+"/platform/deploy")
	payments := file(t, gates+"/teams/payments/production.sigil")
	edit := func(old, new string) map[string]string {
		if !strings.Contains(payments["source"], old) {
			t.Fatalf("payments.production doesn't contain %q", old)
		}
		return map[string]string{"path": payments["path"], "source": strings.Replace(payments["source"], old, new, 1)}
	}
	guardrails := "guardrails(min_soak: 4h)"
	tests := []struct {
		name  string
		files []map[string]string
		want  string // in the response; "" for a compile that succeeds
	}{
		{name: "invoked", files: []map[string]string{kind, payments}},
		{name: "not invoked", files: []map[string]string{kind, edit(guardrails, "")}, want: `"message":"payments.production doesn't invoke deploy.guardrails"`},
		{name: "invoked under when", files: []map[string]string{kind, edit(guardrails, "when release.hotfix {\n  "+guardrails+"\n}")}, want: `"message":"deploy.guardrails must be invoked unconditionally"`},
		{name: "redefined", files: []map[string]string{kind, payments, {"path": "teams/payments/guardrails.sigil", "source": "policy deploy.guardrails: DeployApproval@1\n"}}, want: `"message":"policy deploy.guardrails is defined twice"`},
		{name: "argument out of bounds", files: []map[string]string{kind, edit(guardrails, "guardrails(min_soak: 1m)")}, want: `"file":"` + payments["path"] + `"`},
		{name: "a team file at a trusted path", files: []map[string]string{kind, payments, {"path": trusted[0]["path"], "source": "policy deploy.guardrails: DeployApproval@1\n"}}, want: "is among both the files and the trusted files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst := newInstance(t, nil)
			req := map[string]any{"op": "compile", "policy": "payments.production", "files": tt.files, "trusted_files": trusted, "require": []map[string]any{{"policy": "deploy.guardrails"}}}
			got, want := inst.Call(encode(t, req)), newNative().Call(encode(t, req))
			if !bytes.Equal(got, want) {
				t.Errorf("the module answered\n%s\nwhere the engine answers natively\n%s", got, want)
			}
			if tt.want == "" {
				if !bytes.HasPrefix(got, []byte(`{"ok":true`)) {
					t.Errorf("compile = %s, want it to succeed", got)
				}
				return
			}
			if !bytes.HasPrefix(got, []byte(`{"ok":false`)) || !strings.Contains(string(got), tt.want) {
				t.Errorf("compile = %s, want it to fail with %s", got, tt.want)
			}
		})
	}
}
