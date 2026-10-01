package wasmtest

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/command"
	"github.com/spechtlabs/sigil/internal/engine"
	"github.com/spechtlabs/sigil/internal/testsuite"
)

// The CLI's check and test testdata, and the examples' policies and
// configurations.
const (
	checkData   = root + "/cmd/sigil/command/check/testdata"
	testData    = root + "/cmd/sigil/command/test/testdata"
	gatesConfig = gates + "/sigil.yaml"
	alerts      = root + "/examples/alert-routing/policies"
	flags       = root + "/examples/feature-flags/policies"
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

// TestTestParity checks that the test op answers with the records
// `sigil test -o json` prints for the same files: the CLI's golden fixtures,
// passing, failing, and test files that can't run, and the examples'
// test files, with their input files and, as their configurations make
// them, trusted files.
func TestTestParity(t *testing.T) {
	kind := testData + "/access.sigil"
	policy := testData + "/access/main.sigil"
	tests := []struct {
		name    string
		args    []string // the CLI's, after sigil test
		paths   []string // the module's files, test files and data files, from these paths
		trusted []string // directories among the paths whose .sigil files are the module's trusted files
		run     string
	}{
		{name: "no files", args: []string{testData + "/access/main_test.yaml"}, paths: []string{testData + "/access/main_test.yaml"}},
		{name: "pass", args: []string{"--kind", kind, testData + "/access"}, paths: []string{kind, testData + "/access"}},
		{name: "failures", args: []string{"--kind", kind, policy, testData + "/failing"}, paths: []string{kind, policy, testData + "/failing"}},
		{name: "run", args: []string{"--kind", kind, "--run", "^wrong", policy, testData + "/failing"}, paths: []string{kind, policy, testData + "/failing"}, run: "^wrong"},
		{name: "invalid", args: []string{"--kind", kind, policy, testData + "/invalid"}, paths: []string{kind, policy, testData + "/invalid"}},
		{name: "bad yaml", args: []string{"--kind", kind, policy, testData + "/badyaml"}, paths: []string{kind, policy, testData + "/badyaml"}},
		{name: "no policy", args: []string{"--kind", kind, policy, testData + "/nopolicy"}, paths: []string{kind, policy, testData + "/nopolicy"}},
		{name: "broken bundle", args: []string{"--kind", kind, testData + "/broken"}, paths: []string{kind, testData + "/broken"}},
		{name: "unrelated error", args: []string{"--kind", kind, testData + "/access", testData + "/unrelated"}, paths: []string{kind, testData + "/access", testData + "/unrelated"}},
		{name: "kind error", args: []string{"--kind", kind, testData + "/access", testData + "/brokenkind"}, paths: []string{kind, testData + "/access", testData + "/brokenkind"}},
		{name: "stubs", args: []string{"--kind", kind, policy, testData + "/stubbed"}, paths: []string{kind, policy, testData + "/stubbed"}},
		{name: "stubs of an enum", args: []string{testData + "/enumstubs"}, paths: []string{testData + "/enumstubs"}},
		{name: "stubs of an enum that don't fit", args: []string{testData + "/enumstubs/tiers.sigil", testData + "/enumstubs/main.sigil", testData + "/enumbad"}, paths: []string{testData + "/enumstubs/tiers.sigil", testData + "/enumstubs/main.sigil", testData + "/enumbad"}},
		{name: "stubs of the wrong shape", args: []string{"--kind", kind, policy, testData + "/badstubshape"}, paths: []string{kind, policy, testData + "/badstubshape"}},
		{name: "stubs that don't fit", args: []string{"--kind", kind, policy, testData + "/badstubs"}, paths: []string{kind, policy, testData + "/badstubs"}},
		{name: "stubs that fail", args: []string{"--kind", kind, policy, testData + "/stubfail"}, paths: []string{kind, policy, testData + "/stubfail"}},
		{name: "deploy-gates", args: []string{"--config", gatesConfig, gates}, paths: []string{gates}},
		{name: "deploy-gates, platform trusted", args: []string{"--config", gatesConfig, gates}, paths: []string{gates}, trusted: []string{gates + "/platform/deploy", gates + "/platform/access"}},
		{name: "alert-routing, platform trusted", args: []string{"--config", alerts + "/sigil.yaml", alerts}, paths: []string{alerts}, trusted: []string{alerts + "/platform"}},
		{name: "alert-routing", args: []string{"--config", alerts + "/sigil.yaml", alerts}, paths: []string{alerts}},
		{name: "feature-flags", args: []string{"--config", flags + "/sigil.yaml", flags}, paths: []string{flags}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := cli(t, append([]string{"test"}, tt.args...)...)
			sources, tests, data := testFiles(t, tt.paths...)
			var trusted []map[string]string
			for _, dir := range tt.trusted {
				trusted = append(trusted, files(t, dir)...)
			}
			sources = slices.DeleteFunc(sources, func(f map[string]string) bool {
				return slices.ContainsFunc(trusted, func(tf map[string]string) bool { return tf["path"] == f["path"] })
			})
			req := map[string]any{"op": "test", "files": sources, "trusted_files": trusted, "test_files": tests, "data_files": data}
			if tt.run != "" {
				req["run"] = tt.run
			}
			got := newInstance(t, nil).Request(req)
			if got["ok"] != true {
				t.Fatalf("test = %v", got)
			}
			same(t, got["results"], want)
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

// testFiles returns the test op's files for the paths, as `sigil test`
// reads them: the .sigil files, in the CLI's order; the test files, a
// file named by its path being one by its name; and every other file
// below a directory, which a case's input_file may name.
func testFiles(t *testing.T, paths ...string) (sources, tests, data []map[string]string) {
	t.Helper()
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() {
			if testsuite.IsTestFile(p) {
				tests = append(tests, file(t, p))
			} else {
				sources = append(sources, file(t, p))
			}
			continue
		}
		sources = append(sources, files(t, p)...)
		err = filepath.WalkDir(p, func(name string, d fs.DirEntry, err error) error {
			switch {
			case err != nil:
				return err
			case name != p && strings.HasPrefix(d.Name(), "."):
				if d.IsDir() {
					return filepath.SkipDir
				}
			case d.IsDir() || strings.HasSuffix(name, ".sigil"):
			case testsuite.IsTestFile(name):
				tests = append(tests, file(t, name))
			default:
				data = append(data, file(t, name))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return sources, tests, data
}
