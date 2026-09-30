package command_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/cmd/sigil/command"
	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/engine"
)

// The deploy-gates example's policy repository, and its configuration.
const (
	gates       = "../../../examples/deploy-gates/policies"
	gatesConfig = gates + "/sigil.yaml"
)

// TestEngineCheckParity checks that the engine's check op answers with
// the records `sigil check -o json` prints for the same files: the
// command's testdata, and the deploy-gates example's policies with the
// requirements and lint levels of its sigil.yaml.
func TestEngineCheckParity(t *testing.T) {
	const kind = "check/testdata/deploy_approval.sigil"
	defaults := []string{"--config", "check/testdata/config/defaults.yaml"}
	tests := []struct {
		name  string
		args  []string // flags besides --kind and the paths
		kinds []string
		paths []string
		req   map[string]any // the engine request's fields besides files
	}{
		{name: "lints", args: defaults, kinds: []string{kind}, paths: []string{"check/testdata/lints"}},
		{name: "errors", args: defaults, kinds: []string{kind}, paths: []string{"check/testdata/errors"}},
		{name: "compile", args: defaults, kinds: []string{kind}, paths: []string{"check/testdata/compile"}},
		{name: "stale kind", args: defaults, kinds: []string{kind}, paths: []string{"check/testdata/stale"}},
		{name: "legacy kind", args: defaults, kinds: []string{"check/testdata/kinds/legacy.sigil"}, paths: []string{"check/testdata/compile"}},
		{name: "several kinds", args: defaults, paths: []string{"check/testdata/multikind"}},
		{name: "unknown kind", args: defaults, paths: []string{"check/testdata/unknown_kind"}},
		{name: "one policy", args: append([]string{"--policy", "scope.a"}, defaults...), kinds: []string{kind}, paths: []string{"check/testdata/scope"}, req: map[string]any{"policies": []string{"scope.a"}}},
		{name: "a requirement", args: append([]string{"--require", "deploy.guardrails"}, defaults...), kinds: []string{kind}, paths: []string{"check/testdata/lints"}, req: map[string]any{"require": []map[string]any{{"policy": "deploy.guardrails"}}}},
		{
			name:  "deploy-gates",
			args:  []string{"--config", gatesConfig},
			paths: []string{gates},
			req: map[string]any{
				"require": []map[string]any{
					{"policy": "deploy.guardrails", "trusted": []string{gates + "/platform/deploy"}, "roots": []string{"payments.*", "checkout.*"}},
					{"policy": "access.guardrails", "trusted": []string{gates + "/platform/access"}, "roots": []string{"access.main"}},
				},
				"lints": map[string]string{"gated-deny": "error", "gated-assert": "error", "path-matches-name": "error"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"-o", "json", "check"}, tt.args...)
			for _, k := range tt.kinds {
				args = append(args, "--kind", k)
			}
			want := cli(t, append(args, tt.paths...)...)
			got := request(t, "check", files(t, tt.kinds, tt.paths), tt.req)
			same(t, got["diagnostics"], want)
		})
	}
}

// TestEngineEvalParity checks that compiling and evaluating with the
// engine answers with the record `sigil eval -o json` prints: every
// outcome the command's testdata reaches, stubs included, and the
// deploy-gates example's access policy.
func TestEngineEvalParity(t *testing.T) {
	tests := []struct {
		name   string
		kind   string // under eval/testdata, without .sigil
		paths  []string
		input  string
		policy string
		stubs  string   // a stubs file, under eval/testdata/stubs
		stub   []string // --stub flags
	}{
		{name: "winner", kind: "access", input: "eval/testdata/inputs/admin.json"},
		{name: "default", kind: "access", input: "eval/testdata/inputs/nobody.json"},
		{name: "assert", kind: "access", input: "eval/testdata/inputs/unnamed.json"},
		{name: "conflict", kind: "access", input: "eval/testdata/inputs/conflict.json"},
		{name: "conflict outcome", kind: "access_conflict", paths: []string{"eval/testdata/access"}, input: "eval/testdata/inputs/conflict.json"},
		{name: "unbound function", kind: "access", input: "eval/testdata/inputs/vault.json"},
		{name: "stub", kind: "access", input: "eval/testdata/inputs/vault.json", stub: []string{"owner=ada"}},
		{name: "stubs file", kind: "access", input: "eval/testdata/inputs/vault.json", stubs: "owner.yaml"},
		{name: "stub error", kind: "access", input: "eval/testdata/inputs/vault.json", stubs: "down.yaml"},
		{name: "stub unmatched", kind: "access", input: "eval/testdata/inputs/vault.json", stubs: "only_calls.json"},
		{name: "collect", kind: "grants", input: "eval/testdata/inputs/grants.json"},
		{name: "collect nothing", kind: "grants", input: "eval/testdata/inputs/nogrants.json"},
		{name: "outcome assert", kind: "grants", input: "eval/testdata/inputs/writeonly.json"},
		{name: "candidate assert", kind: "reviews", input: "eval/testdata/inputs/selfreview.json"},
		{name: "enum", kind: "tiers", input: "eval/testdata/inputs/tier_standard.json"},
		{name: "deploy-gates access", paths: []string{gates}, input: gates + "/access/testdata/admin-platform.json", policy: "access.main"},
		{name: "deploy-gates access, outsider", paths: []string{gates}, input: gates + "/access/testdata/outsider.json", policy: "access.main"},
		{name: "deploy-gates access, break glass", paths: []string{gates}, input: gates + "/access/testdata/break-glass.json", policy: "access.main"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"-o", "json", "eval", "--input", tt.input}
			var kinds []string
			paths := tt.paths
			if tt.kind != "" {
				kinds = []string{"eval/testdata/" + tt.kind + ".sigil"}
				args = append(args, "--kind", kinds[0])
				if paths == nil {
					paths = []string{"eval/testdata/" + strings.TrimSuffix(tt.kind, "_conflict")}
				}
			}
			if tt.policy != "" {
				args = append(args, "--policy", tt.policy)
			}
			if tt.paths != nil && tt.kind == "" {
				args = append(args, "--config", gatesConfig)
			}
			stubs := map[string]any{}
			if tt.stubs != "" {
				args = append(args, "--stubs", "eval/testdata/stubs/"+tt.stubs)
				stubs = stubsFile(t, "eval/testdata/stubs/"+tt.stubs)
			}
			for _, s := range tt.stub {
				args = append(args, "--stub", s)
				name, value, _ := strings.Cut(s, "=")
				stubs[name] = map[string]any{"returns": value}
			}
			want := cli(t, append(args, paths...)...)

			e := engine.New()
			compiled := call(t, e, map[string]any{"op": "compile", "files": files(t, kinds, paths), "policy": tt.policy, "stubs": stubs})
			input, err := os.ReadFile(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			got := call(t, e, map[string]any{"op": "eval", "handle": compiled["handle"], "input": json.RawMessage(input)})
			delete(got, "ok")
			same(t, got, want)
		})
	}
}

// TestEngineExplainParity checks that the engine's explain op, by files
// and by handle, answers with the records `sigil explain -o json`
// prints.
func TestEngineExplainParity(t *testing.T) {
	const kind = "explain/testdata/deploy_approval.sigil"
	tests := []struct {
		name    string
		kinds   []string
		paths   []string
		pattern string
		config  string
	}{
		{name: "one policy", kinds: []string{kind}, paths: []string{"explain/testdata/deploy", "explain/testdata/payments"}, pattern: "payments.production"},
		{name: "a pattern", kinds: []string{kind}, paths: []string{"explain/testdata"}, pattern: "deploy.*"},
		{name: "deploy-gates", paths: []string{gates}, config: gatesConfig},
		{name: "deploy-gates team", paths: []string{gates}, config: gatesConfig, pattern: "checkout.production"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := []string{"-o", "json", "explain"}
			for _, k := range tt.kinds {
				args = append(args, "--kind", k)
			}
			if tt.config != "" {
				args = append(args, "--config", tt.config)
			}
			if tt.pattern != "" {
				args = append(args, "--policy", tt.pattern)
			}
			want := cli(t, append(args, tt.paths...)...)
			fs := files(t, tt.kinds, tt.paths)
			got := request(t, "explain", fs, map[string]any{"policy": tt.pattern})
			same(t, got["explanations"], want)

			if strings.Contains(tt.pattern, "*") || tt.pattern == "" {
				return
			}
			e := engine.New()
			compiled := call(t, e, map[string]any{"op": "compile", "files": fs, "policy": tt.pattern})
			byHandle := call(t, e, map[string]any{"op": "explain", "handle": compiled["handle"]})
			same(t, byHandle["explanations"], want)
		})
	}
}

// TestEngineFormatParity checks that the engine's format op formats as
// `sigil fmt` does, and reports the same syntax errors.
func TestEngineFormatParity(t *testing.T) {
	names, err := filepath.Glob("../../../internal/format/testdata/*.sigil")
	if err != nil || len(names) == 0 {
		t.Fatalf("Glob() = %v, %v", names, err)
	}
	names = append(names, "eval/testdata/access.sigil", gates+"/teams/payments/production.sigil")
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			src, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			records, _ := cli(t, "-o", "json", "fmt", name).([]any)
			if len(records) != 1 {
				t.Fatalf("sigil fmt printed %v, want one record", records)
			}
			want, _ := records[0].(map[string]any)
			got := call(t, engine.New(), map[string]any{"op": "format", "source": string(src), "path": filepath.ToSlash(name)})
			if want["diagnostics"] != nil {
				same(t, got["diagnostics"], want["diagnostics"])
				return
			}
			same(t, got["source"], want["source"])
			same(t, got["formatted"], want["formatted"])
		})
	}
}

// cli runs the sigil command line and returns what it printed as JSON,
// decoded. A command that fails, such as a check that finds errors, still
// prints its records.
func cli(t *testing.T, args ...string) any { //nolint:emptyinterface // any JSON document
	t.Helper()
	cmd := command.NewCommand()
	var out, errs bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errs)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(args)
	_ = cmd.Execute()
	var v any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("sigil %s printed %q, and %q to stderr: %v", strings.Join(args, " "), out.String(), errs.String(), err)
	}
	return v
}

// files returns the kind files, then the files below the paths, in the
// order the CLI reads them and named as it names them, as the engine's
// virtual files.
func files(t *testing.T, kinds, paths []string) []map[string]string {
	t.Helper()
	names, err := project.Expand(paths, project.IsSigil)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]map[string]string, 0, len(kinds)+len(names))
	for _, name := range append(append([]string{}, kinds...), names...) {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, map[string]string{"path": filepath.ToSlash(filepath.Clean(name)), "source": string(src)})
	}
	return out
}

// stubsFile reads a stubs file, YAML or JSON, as the engine's stubs.
func stubsFile(t *testing.T, name string) map[string]any {
	t.Helper()
	src, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var stubs map[string]any
	if err := yaml.Unmarshal(src, &stubs); err != nil {
		t.Fatal(err)
	}
	return stubs
}

// request sends one op over files to a new engine and returns its
// successful response.
func request(t *testing.T, op string, fs []map[string]string, fields map[string]any) map[string]any {
	t.Helper()
	req := map[string]any{"op": op, "files": fs}
	maps.Copy(req, fields)
	resp := call(t, engine.New(), req)
	if resp["ok"] != true {
		t.Fatalf("%s = %v", op, resp)
	}
	return resp
}

// call sends one request and decodes the response.
func call(t *testing.T, e *engine.Engine, req map[string]any) map[string]any {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var resp map[string]any
	if err := json.Unmarshal(e.Call(body), &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

// same fails the test unless the engine's answer equals the CLI's, as
// JSON values.
func same(t *testing.T, got, want any) { //nolint:emptyinterface // any JSON values
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("the engine answered\n%s\nwhere the CLI printed\n%s", g, w)
	}
}
