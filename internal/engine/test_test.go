package engine_test

import (
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/engine"
)

// suite tests access.main, stubbing ttl_for for every case.
const suite = `policy: access.main
stubs:
  ttl_for: {returns: 2h}
cases:
  - name: admin
    input: {user: {name: ada, admin: true}, age: 1h}
    expect:
      decision: allow
      reason: admin
      payload: {ttl: 2h}
  - name: nobody
    input: {user: {name: cy}}
    expect: {decision: deny, reason: no_rule_matched}
`

// limits is a policy chained invokes, as a host's trusted document.
const limits = "policy platform.limits: Access@1\n\nparam max_age: duration = 30d, min: 1d\n\nwhen age > max_age {\n  deny(reason: too_old)\n}\n"

// chained invokes platform.limits.
const chained = "policy access.chained: Access@1\n\nuse platform.limits\n\nlimits(max_age: 2d)\n"

// chainedSuite tests access.chained.
const chainedSuite = "policy: access.chained\ncases:\n  - name: too old\n    input: {user: {name: cy}, age: 3d}\n    expect: {decision: deny, reason: too_old}\n"

// TestTest checks the test op: each request it fails, and the results,
// a test file's or a case's, it answers with what sigil test reports.
func TestTest(t *testing.T) {
	files := []file{{"kind.sigil", kind}, {"access/main.sigil", policy}}
	one := func(src string) []file { return []file{{"access/main_test.yaml", src}} }
	tests := []struct {
		name  string
		req   map[string]any
		want  []string // substrings of the response
		fails bool     // ok is false
	}{
		{name: "passing", req: map[string]any{"test_files": one(suite)}, want: []string{
			`{"ok":true,"results":[{"file":"access/main_test.yaml","policy":"access.main","cases":[{"name":"admin","line":5,"passed":true},{"name":"nobody","line":11,"passed":true}]}]}`,
		}},
		{name: "a failing case", req: map[string]any{"test_files": one(strings.Replace(suite, "reason: admin", "reason: team_member", 1))}, want: []string{
			`{"ok":true,`, `{"name":"admin","failures":["got allow(reason: admin), want allow(reason: team_member)"],"line":5,"passed":false}`, `{"name":"nobody","line":11,"passed":true}`,
		}},
		{name: "a case's own stubs", req: map[string]any{"test_files": one("policy: access.main\ncases:\n  - name: vault\n    stubs: {owner: {returns: ada}}\n    input: {user: {name: ada}, resource: vault}\n    expect: {decision: allow, reason: team_member}\n")}, want: []string{
			`"cases":[{"name":"vault","line":3,"passed":true}]`,
		}},
		{name: "a case's stubs of an unknown function", req: map[string]any{"test_files": one("policy: access.main\ncases:\n  - name: vault\n    stubs: {ownr: {returns: ada}}\n    input: {user: {name: ada}, resource: vault}\n    expect: {decision: allow, reason: team_member}\n")}, want: []string{
			`"error":"access/main_test.yaml:4: case \"vault\": the kind has no host function ownr to stub`, `"cases":[]`,
		}},
		{name: "a conflict", req: map[string]any{"test_files": one("policy: access.main\nstubs: {owner: {returns: ada}, ttl_for: {returns: 2h}}\ncases:\n  - name: both\n    input: {user: {name: ada, admin: true}, resource: vault}\n    expect: {decision: allow, reason: admin}\n")}, want: []string{
			`"failures":["got a conflict (`, `"passed":false`,
		}},
		{name: "an unstubbed host function", req: map[string]any{"test_files": one("policy: access.main\ncases:\n  - name: vault\n    input: {user: {name: ada}, resource: vault}\n    expect: {decision: allow, reason: team_member}\n")}, want: []string{
			`host function owner failed: no implementation in this sigil binary`, `"passed":false`,
		}},
		{name: "input_file", req: map[string]any{
			"test_files": one("policy: access.main\ncases:\n  - name: nobody\n    input_file: testdata/nobody.json\n    expect: {decision: deny, reason: no_rule_matched}\n"),
			"data_files": []file{{"access/testdata/nobody.json", `{"user": {"name": "cy"}}`}},
		}, want: []string{`"cases":[{"name":"nobody","line":3,"passed":true}]`}},
		{name: "input_file in YAML, above the test file", req: map[string]any{
			"test_files": one("policy: access.main\ncases:\n  - name: nobody\n    input_file: ../shared/nobody.yaml\n    expect: {decision: deny, reason: no_rule_matched}\n"),
			"data_files": []file{{"./shared//nobody.yaml", "user: {name: cy}\n"}},
		}, want: []string{`"cases":[{"name":"nobody","line":3,"passed":true}]`}},
		{name: "input_file missing", req: map[string]any{
			"test_files": one("policy: access.main\ncases:\n  - name: nobody\n    input_file: nope.json\n    expect: {decision: deny, reason: no_rule_matched}\n"),
			"data_files": []file{{"nope.json", `{}`}},
		}, want: []string{`{"name":"nobody","error":"input_file nope.json couldn't be read (input_file is relative to the test file)","line":3,"passed":false}`}},
		{name: "run", req: map[string]any{"test_files": one(suite), "run": "^no"}, want: []string{`"cases":[{"name":"nobody","line":11,"passed":true}]`}},
		{name: "run matching nothing", req: map[string]any{"test_files": one(suite), "run": "nothing"}, want: []string{`"cases":[]`}},
		{name: "a test file that isn't YAML", req: map[string]any{"test_files": one("policy: [")}, want: []string{
			`{"ok":true,"results":[{"file":"access/main_test.yaml","policy":"","error":"access/main_test.yaml: not a valid test file: `, `"cases":[]`,
		}},
		{name: "stubs of the wrong shape", req: map[string]any{"test_files": one("policy: access.main\nstubs: {owner: 5}\ncases: []\n")}, want: []string{`"error":"access/main_test.yaml:2: `, `"cases":[]`}},
		{name: "a case that doesn't fit the kind", req: map[string]any{"test_files": one("policy: access.main\ncases:\n  - name: x\n    input: {}\n    expect: {decision: allow, reason: nope}\n")}, want: []string{
			`"error":"access/main_test.yaml:3: case \"x\": decision allow has no reason \"nope\"`, `"cases":[]`,
		}},
		{name: "a file's stubs that don't fit", req: map[string]any{"test_files": one("policy: access.main\nstubs: {ttl_for: {returns: 5}}\ncases: []\n")}, want: []string{`"error":"access/main_test.yaml:2: stub ttl_for`}},
		{name: "a policy the files don't define", req: map[string]any{"test_files": one("policy: access.nope\ncases: []\n")}, want: []string{
			`"policy":"access.nope","error":"error: bundle has no policy access.nope\n  = help: the bundle defines: access.main"`,
		}},
		{name: "a policy that doesn't check", req: map[string]any{"files": append(files, file{"access/other.sigil", other}), "test_files": one("policy: access.other\ncases: []\n")}, want: []string{
			`"policy":"access.other","error":"access/other.sigil:3:11: error: unknown field `, `on type User\n`,
		}},
		{name: "a policy that only a compile finds wrong", req: map[string]any{
			"files":      append(files, file{"access/limits.sigil", "policy access.limits: Access@1\n\nparam max_age: duration = 30d, min: 1d\n\nwhen age > max_age {\n  deny(reason: too_old)\n}\n"}, file{"access/chained.sigil", "policy access.chained: Access@1\n\nuse access.limits\n\nlimits(max_age: 1h)\n"}),
			"test_files": one("policy: access.chained\ncases: []\n"),
		}, want: []string{`"policy":"access.chained","error":"access/chained.sigil:5:`, `"cases":[]`}},
		{name: "a document the policy doesn't use, that doesn't check", req: map[string]any{"files": append(files, file{"access/other.sigil", other}), "test_files": one(suite)}, want: []string{`"cases":[{"name":"admin","line":5,"passed":true}`}},
		{name: "a kind that doesn't check", req: map[string]any{"files": []file{{"kind.sigil", "kind Access version 1\n\ninput x: nope\n"}, {"access/main.sigil", policy}}, "test_files": one(suite)}, want: []string{`"policy":"access.main","error":"kind.sigil:1:6: error: kind Access declares no decisions`}},
		{name: "test files in the CLI's order", req: map[string]any{"test_files": []file{{"b_test.yaml", suite}, {"a_test.yml", suite}, {"./b_test.yaml", suite}}}, want: []string{`"results":[{"file":"a_test.yml",`, `{"file":"b_test.yaml",`}},
		{name: "invalid run", req: map[string]any{"test_files": one(suite), "run": "("}, want: []string{"run isn't a valid regular expression: error parsing regexp: missing closing ): `(`", `"help":"run takes a Go regular expression matched against case names"`}, fails: true},
		{name: "no test files", req: map[string]any{}, want: []string{"the request holds no test files"}, fails: true},
		{name: "a test file named like no test file", req: map[string]any{"test_files": []file{{"access/main.yaml", suite}}}, want: []string{"the test file access/main.yaml isn't named like one", "a test file's name ends in _test.yaml or _test.yml"}, fails: true},
		{name: "a test file without a path", req: map[string]any{"test_files": []file{{"", suite}}}, want: []string{"test file 1 has no path"}, fails: true},
		{name: "a data file without a path", req: map[string]any{"test_files": one(suite), "data_files": []file{{"a.json", "{}"}, {"", "{}"}}}, want: []string{"data file 2 has no path"}, fails: true},
		{name: "a path given twice", req: map[string]any{"test_files": one(suite), "data_files": []file{{"access/main_test.yaml", "{}"}}}, want: []string{"access/main_test.yaml is given twice, with two sources"}, fails: true},
		{name: "a data file at a policy's path", req: map[string]any{"test_files": one(suite), "data_files": []file{{"./access/main.sigil", "{}"}}}, want: []string{"access/main.sigil is given twice, with two sources"}, fails: true},
		{name: "a data file at a trusted file's path", req: map[string]any{"trusted_files": []file{{"platform/limits.sigil", limits}}, "test_files": one(suite), "data_files": []file{{"platform/limits.sigil", "{}"}}}, want: []string{"platform/limits.sigil is given twice, with two sources"}, fails: true},
		{name: "a data file that is a policy", req: map[string]any{"test_files": one(suite), "data_files": []file{{"access/main.sigil", policy}}}, want: []string{`"cases":[{"name":"admin","line":5,"passed":true}`}},
		{name: "a path among the files and the trusted files", req: map[string]any{"trusted_files": []file{{"access/main.sigil", policy}}, "test_files": one(suite)}, want: []string{"access/main.sigil is among both the files and the trusted files"}, fails: true},
		{name: "no files", req: map[string]any{"files": nil, "test_files": one(suite)}, want: []string{
			`{"ok":true,"results":[{"file":"access/main_test.yaml","policy":"access.main","error":"error: bundle has no policy access.main\n  = help: the bundle defines no policies","cases":[]}]}`,
		}},
		{name: "a policy using a trusted one", req: map[string]any{"files": append(files, file{"access/chained.sigil", chained}), "trusted_files": []file{{"platform/limits.sigil", limits}}, "test_files": one(chainedSuite)}, want: []string{
			`{"ok":true,"results":[{"file":"access/main_test.yaml","policy":"access.chained","cases":[{"name":"too old","line":3,"passed":true}]}]}`,
		}},
		{name: "a policy using a trusted one, without it", req: map[string]any{"files": append(files, file{"access/chained.sigil", chained}), "test_files": one(chainedSuite)}, want: []string{`"policy":"access.chained","error":"access/chained.sigil:3:5`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req["op"] = "test"
			if _, ok := tt.req["files"]; !ok {
				tt.req["files"] = files
			}
			got := raw(t, engine.New(), tt.req)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("test = %s, want it to contain %s", got, want)
				}
			}
			if failed := strings.HasPrefix(got, `{"ok":false`); failed != tt.fails {
				t.Errorf("test = %s, want failed = %v", got, tt.fails)
			}
		})
	}
}
