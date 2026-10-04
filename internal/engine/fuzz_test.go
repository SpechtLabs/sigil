package engine_test

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/engine"
)

// presets are the playground's workspaces, which it sends the engine as
// they are, or as a share link rebuilt them.
const presets = "../../docs/.vuepress/components/playground/presets"

// FuzzCall sends any request to a new engine: the answer is one JSON
// object whose ok is a bool, a failure says why, a panic never stands in
// for an answer, and a second engine answers the same request the same.
func FuzzCall(f *testing.F) {
	files := []file{{"kind.sigil", kind}, {"access/main.sigil", policy}}
	for _, req := range []map[string]any{
		{"op": "version", "id": 1},
		{"op": "check", "files": append(files, file{"access/other.sigil", other}), "lints": map[string]string{"unused-let": "error"}},
		{"op": "check", "files": append(files, file{"platform/limits.sigil", limits}, file{"access/chained.sigil", chained}), "require": []map[string]any{{"policy": "platform.limits", "trusted": []string{"platform"}, "roots": []string{"access.*"}}}},
		{"op": "compile", "files": files, "functions": []string{"owner"}, "stubs": map[string]any{"ttl_for": map[string]any{"calls": []any{map[string]any{"args": []any{map[string]any{"name": "ada"}, "1h"}, "returns": "2h"}}}}},
		{"op": "compile", "files": files, "policy": "access.main", "require": []map[string]any{{"policy": "access.main"}}, "trusted_files": []file{{"platform/limits.sigil", limits}}},
		{"op": "explain", "files": append(files, file{"platform/limits.sigil", limits}, file{"access/chained.sigil", chained}), "policy": "access.*"},
		{"op": "format", "source": policy, "path": "access/main.sigil"},
		{"op": "test", "files": files, "test_files": []file{{"access/main_test.yaml", suite}}, "run": "adm"},
		{"op": "test", "files": append(files, file{"access/chained.sigil", chained}), "trusted_files": []file{{"platform/limits.sigil", limits}}, "test_files": []file{{"access/chained_test.yaml", chainedSuite}}},
		{"op": "eval", "handle": 1, "input": map[string]any{}, "timeout_ms": 10},
		{"op": "release", "handle": 1},
	} {
		body, err := json.Marshal(req)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(body)
	}
	for _, req := range presetRequests(f) {
		f.Add(req)
	}
	f.Add([]byte(`{"op": "version", "id": 1e400}`))
	f.Add([]byte(`{"op": "check", "files": [{"path": "", "source": ""}, {"path": "", "source": ""}]}`))
	f.Fuzz(func(t *testing.T, req []byte) {
		got := engine.New().Call(req)
		var resp struct {
			OK    *bool `json:"ok"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(got, &resp); err != nil || resp.OK == nil {
			t.Fatalf("the response isn't a JSON object with ok: %v\n%s", err, got)
		}
		if !*resp.OK && (resp.Error == nil || resp.Error.Message == "") {
			t.Fatalf("a failure without a message: %s", got)
		}
		if resp.Error != nil && strings.HasPrefix(resp.Error.Message, "sigil failed while handling the request") {
			t.Fatalf("handling the request panicked: %s", resp.Error.Message)
		}
		if again := engine.New().Call(req); string(again) != string(got) {
			t.Fatalf("the same request got another answer:\n%s\nthen:\n%s", got, again)
		}
	})
}

// FuzzEval evaluates any input, with a host that gives any answer, on a
// policy compiled once: an evaluation always answers, and an input that
// decodes always gets the kind's single decision, the same twice.
func FuzzEval(f *testing.F) {
	// Fuzz workers call one at a time, so the host reads the answer the
	// current call set.
	var reply []byte
	e := engine.New(engine.WithHost(func([]byte) []byte { return reply }))
	body, err := json.Marshal(map[string]any{"op": "compile", "files": []file{{"kind.sigil", kind}, {"access/main.sigil", policy}}, "functions": []string{"owner", "ttl_for"}})
	if err != nil {
		f.Fatal(err)
	}
	var compiled struct {
		Handle uint32 `json:"handle"`
	}
	if resp := e.Call(body); json.Unmarshal(resp, &compiled) != nil || compiled.Handle == 0 {
		f.Fatalf("compile = %s", resp)
	}
	prefix := fmt.Sprintf(`{"op": "eval", "handle": %d, "input": `, compiled.Handle)
	for _, input := range []string{`{"user": {"name": "cy"}}`, `{"user": {"name": "ada", "admin": true}, "age": "1h"}`, `{"user": {"name": "ada"}, "resource": "vault"}`, `{"user": {"name": "x", "teams": ["a"]}, "age": "31d", "names": ["banned"]}`, `{}`, `{"usr": {}}`, `[1]`, `null`, `{"age": 1.5e300}`} {
		for _, answer := range []string{`{"result": "ada"}`, `{"result": "2h30m"}`, `{"error": "directory unavailable"}`, `{"error": 1}`, `{}`, `{"result": null}`, `{"result": 5}`, `not json`} {
			f.Add([]byte(input), []byte(answer))
		}
	}
	f.Fuzz(func(t *testing.T, input, answer []byte) {
		if !json.Valid(input) {
			return
		}
		reply = answer
		got := e.Call([]byte(prefix + string(input) + "}"))
		var resp struct {
			OK       *bool  `json:"ok"`
			Decision string `json:"decision"`
			Outcome  []any  `json:"outcome"`
			Error    *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(got, &resp); err != nil || resp.OK == nil {
			t.Fatalf("the response isn't a JSON object with ok: %v\n%s", err, got)
		}
		if resp.Error != nil && strings.HasPrefix(resp.Error.Message, "sigil failed while handling the request") {
			t.Fatalf("evaluation panicked: %s", resp.Error.Message)
		}
		// A failed evaluation is still ok, with the default as its outcome.
		if *resp.OK && ((resp.Decision != "allow" && resp.Decision != "deny") || len(resp.Outcome) != 1) {
			t.Fatalf("an evaluation without one decision: %s", got)
		}
		if again := e.Call([]byte(prefix + string(input) + "}")); string(again) != string(got) {
			t.Fatalf("the same input got another answer:\n%s\nthen:\n%s", got, again)
		}
	})
}

// presetRequests returns a check and a test request for each playground
// preset, of all its files.
func presetRequests(f *testing.F) [][]byte {
	dirs, err := os.ReadDir(presets)
	if err != nil {
		f.Fatal(err)
	}
	var out [][]byte
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		var sources, tests, data []file
		fsys := os.DirFS(path.Join(presets, d.Name()))
		err := fs.WalkDir(fsys, ".", func(name string, e fs.DirEntry, err error) error {
			if err != nil || e.IsDir() {
				return err
			}
			src, err := fs.ReadFile(fsys, name)
			switch {
			case err != nil:
				return err
			case strings.HasSuffix(name, ".sigil"):
				sources = append(sources, file{name, string(src)})
			case strings.HasSuffix(name, "_test.yaml"):
				tests = append(tests, file{name, string(src)})
			default:
				data = append(data, file{name, string(src)})
			}
			return nil
		})
		if err != nil {
			f.Fatal(err)
		}
		for _, req := range []map[string]any{
			{"op": "check", "files": sources},
			{"op": "test", "files": sources, "test_files": tests, "data_files": data},
		} {
			body, err := json.Marshal(req)
			if err != nil {
				f.Fatal(err)
			}
			out = append(out, body)
		}
	}
	if len(out) == 0 {
		f.Fatal("no playground presets")
	}
	return out
}
