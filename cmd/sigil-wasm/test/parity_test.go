package wasmtest

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/command"
	"github.com/spechtlabs/sigil/internal/engine"
)

// The CLI's check testdata, and the deploy-gates example's configuration.
const (
	checkData   = root + "/cmd/sigil/command/check/testdata"
	gatesConfig = gates + "/sigil.yaml"
)

// TestCLIParity checks that the module answers with the records the sigil
// CLI prints as JSON for the same files: check, eval and explain over the
// CLI's golden fixtures and the deploy-gates example, and fmt over the
// formatter's.
func TestCLIParity(t *testing.T) {
	gatesReqs := []map[string]any{
		{"policy": "deploy.guardrails", "trusted": []string{gates + "/platform/deploy"}, "roots": []string{"payments.*", "checkout.*"}},
		{"policy": "access.guardrails", "trusted": []string{gates + "/platform/access"}, "roots": []string{"access.main"}},
	}
	gatesLints := map[string]string{"gated-deny": "error", "gated-assert": "error", "path-matches-name": "error"}
	kind := checkData + "/deploy_approval.sigil"
	defaults := "--config=" + checkData + "/config/defaults.yaml"
	tests := []struct {
		name  string
		args  []string         // the CLI's
		files []string         // the module's, in the CLI's order
		reqs  []map[string]any // the module's; the last one's response is compared
		field string           // the response field that holds the CLI's record; the whole response without ok for ""
	}{
		{name: "check lints", args: []string{"check", defaults, "--kind", kind, checkData + "/lints"}, files: []string{kind, checkData + "/lints"}, reqs: []map[string]any{{"op": "check"}}, field: "diagnostics"},
		{name: "check errors", args: []string{"check", defaults, "--kind", kind, checkData + "/errors"}, files: []string{kind, checkData + "/errors"}, reqs: []map[string]any{{"op": "check"}}, field: "diagnostics"},
		{name: "check several kinds", args: []string{"check", defaults, checkData + "/multikind"}, files: []string{checkData + "/multikind"}, reqs: []map[string]any{{"op": "check"}}, field: "diagnostics"},
		{name: "check deploy-gates", args: []string{"check", "--config", gatesConfig, gates}, files: []string{gates}, reqs: []map[string]any{{"op": "check", "require": gatesReqs, "lints": gatesLints}}, field: "diagnostics"},
		{name: "eval winner", args: evalArgs("access", "admin.json"), files: evalFiles("access"), reqs: evalReqs(t, "admin.json", nil)},
		{name: "eval assert", args: evalArgs("access", "unnamed.json"), files: evalFiles("access"), reqs: evalReqs(t, "unnamed.json", nil)},
		{name: "eval conflict", args: evalArgs("access", "conflict.json"), files: evalFiles("access"), reqs: evalReqs(t, "conflict.json", nil)},
		{name: "eval unbound function", args: evalArgs("access", "vault.json"), files: evalFiles("access"), reqs: evalReqs(t, "vault.json", nil)},
		{name: "eval stub", args: append(evalArgs("access", "vault.json"), "--stub", "owner=ada"), files: evalFiles("access"), reqs: evalReqs(t, "vault.json", map[string]any{"owner": map[string]any{"returns": "ada"}})},
		{name: "eval collect", args: evalArgs("grants", "grants.json"), files: evalFiles("grants"), reqs: evalReqs(t, "grants.json", nil)},
		{name: "eval outcome assert", args: evalArgs("grants", "writeonly.json"), files: evalFiles("grants"), reqs: evalReqs(t, "writeonly.json", nil)},
		{name: "eval enum", args: evalArgs("tiers", "tier_standard.json"), files: evalFiles("tiers"), reqs: evalReqs(t, "tier_standard.json", nil)},
		{
			name:  "eval deploy-gates",
			args:  []string{"eval", "--config", gatesConfig, "--policy", "access.main", "--input", gates + "/access/testdata/break-glass.json", gates},
			files: []string{gates},
			reqs:  []map[string]any{{"op": "compile", "policy": "access.main"}, {"op": "eval", "handle": 1, "input": fixture(t, gates+"/access/testdata/break-glass.json")}},
		},
		{name: "explain deploy-gates", args: []string{"explain", "--config", gatesConfig, gates}, files: []string{gates}, reqs: []map[string]any{{"op": "explain"}}, field: "explanations"},
		{name: "explain by handle", args: []string{"explain", "--config", gatesConfig, "--policy", "payments.production", gates}, files: []string{gates}, reqs: []map[string]any{{"op": "compile", "policy": "payments.production"}, {"op": "explain", "handle": 1}}, field: "explanations"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := cli(t, tt.args...)
			fs := files(t, tt.files...)
			inst := newInstance(t, nil)
			var got map[string]any
			for _, req := range tt.reqs {
				if req["op"] != "eval" && req["handle"] == nil {
					req["files"] = fs
				}
				got = inst.Request(req)
			}
			if tt.field != "" {
				same(t, got[tt.field], want)
				return
			}
			delete(got, "ok")
			same(t, got, want)
		})
	}
}

// TestFormatParity checks that the module formats as `sigil fmt` does,
// and reports the same syntax errors.
func TestFormatParity(t *testing.T) {
	names, err := filepath.Glob(root + "/internal/format/testdata/*.sigil")
	if err != nil || len(names) == 0 {
		t.Fatalf("Glob() = %v, %v", names, err)
	}
	inst := newInstance(t, nil)
	for _, name := range names {
		t.Run(filepath.Base(name), func(t *testing.T) {
			records, _ := cli(t, "fmt", name).([]any)
			if len(records) != 1 {
				t.Fatalf("sigil fmt printed %v, want one record", records)
			}
			want, _ := records[0].(map[string]any)
			f := file(t, name)
			got := inst.Request(map[string]any{"op": "format", "source": f["source"], "path": f["path"]})
			if want["diagnostics"] != nil {
				same(t, got["diagnostics"], want["diagnostics"])
				return
			}
			same(t, got["source"], want["source"])
			same(t, got["formatted"], want["formatted"])
		})
	}
}

// newNative returns the engine, running natively.
func newNative() *engine.Engine { return engine.New() }

// evalArgs are the CLI's args to evaluate the eval testdata's policies of
// kind against an input.
func evalArgs(kind, input string) []string {
	return []string{"eval", "--kind", evalData + "/" + kind + ".sigil", "--input", evalData + "/inputs/" + input, evalData + "/" + kind}
}

// evalFiles are the module's files for the eval testdata's kind.
func evalFiles(kind string) []string {
	return []string{evalData + "/" + kind + ".sigil", evalData + "/" + kind}
}

// evalReqs compile the only policy, with stubs, and evaluate it against
// an input of the eval testdata.
func evalReqs(t *testing.T, input string, stubs map[string]any) []map[string]any {
	return []map[string]any{{"op": "compile", "stubs": stubs}, {"op": "eval", "handle": 1, "input": fixture(t, evalData+"/inputs/"+input)}}
}

// cli runs the sigil command line with -o json and returns what it
// printed, decoded. A command that fails, such as a check that finds
// errors, still prints its records.
func cli(t *testing.T, args ...string) any { //nolint:emptyinterface // any JSON document
	t.Helper()
	cmd := command.NewCommand()
	var out, errs bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errs)
	cmd.SetIn(strings.NewReader(""))
	cmd.SetArgs(append([]string{"-o", "json"}, args...))
	_ = cmd.Execute()
	var v any
	if err := json.Unmarshal(out.Bytes(), &v); err != nil {
		t.Fatalf("sigil %s printed %q, and %q to stderr: %v", strings.Join(args, " "), out.String(), errs.String(), err)
	}
	return v
}

// same fails the test unless the module's answer equals the CLI's, as
// JSON values.
func same(t *testing.T, got, want any) { //nolint:emptyinterface // any JSON values
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		g, _ := json.MarshalIndent(got, "", "  ")
		w, _ := json.MarshalIndent(want, "", "  ")
		t.Errorf("the module answered\n%s\nwhere the CLI printed\n%s", g, w)
	}
}
