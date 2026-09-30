package engine_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"testing"

	"github.com/spechtlabs/sigil/internal/engine"
)

// kind is the Access kind of the CLI's eval tests, with a host function
// of every shape the tests need.
const kind = `kind Access version 1

type User {
  name: string
  teams: list<string>
  admin: bool
}

input user: User
input resource: string
input age: duration
input names: list<string>

fn owner(string) -> string
fn ttl_for(User, duration) -> duration

decision deny {
  reason: banned | too_old | no_rule_matched
}

decision allow {
  reason: admin | team_member
  ttl: duration = 1h
}

collect one
precedence deny > allow

default deny(reason: no_rule_matched)
`

// policy is access.main, which reads every input and calls both host
// functions.
const policy = `policy access.main: Access@1

assert("named_user", user.name != "")

when user.admin {
  allow(reason: admin, ttl: ttl_for(user, age))
}

when resource == "vault" and owner(resource) == user.name {
  allow(reason: team_member, ttl: 15m)
}

when age > 30d {
  deny(reason: too_old)
}

when any n in names: n == "banned" {
  deny(reason: banned)
}
`

// other is a second policy, which access.main doesn't use, with a type
// error.
const other = `policy access.other: Access@1

when user.nmae == "x" {
  allow(reason: admin)
}
`

// file is a virtual file of a request.
type file struct {
	Path   string `json:"path"`
	Source string `json:"source"`
}

// response is a decoded response.
type response map[string]any

func TestVersion(t *testing.T) {
	bi := &debug.BuildInfo{GoVersion: "go1.27.1", Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
		{Key: "GOOS", Value: "wasip1"}, {Key: "GOARCH", Value: "wasm"}, {Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.modified", Value: "true"},
	}}
	tests := []struct {
		name string
		opts []engine.Option
		want string
	}{
		{name: "release", opts: []engine.Option{engine.WithVersion("1.2.3"), engine.WithBuildInfo(bi)}, want: `{"id":4,"ok":true,"version":"1.2.3","commit":"abc123","commitTime":"unknown","dirty":true,"goVersion":"go1.27.1","platform":"wasip1/wasm"}`},
		{name: "module version", opts: []engine.Option{engine.WithBuildInfo(bi)}, want: `{"id":4,"ok":true,"version":"(devel)","commit":"abc123","commitTime":"unknown","dirty":true,"goVersion":"go1.27.1","platform":"wasip1/wasm"}`},
		{name: "nil build info keeps the binary's", opts: []engine.Option{engine.WithBuildInfo(nil), engine.WithVersion("v")}, want: `"version":"v"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(engine.New(tt.opts...).Call([]byte(`{"op": "version", "id": 4}`)))
			if !strings.Contains(got, tt.want) {
				t.Errorf("version = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestMalformed checks that every request the engine can't act on is a
// response with ok false, never a panic or silence.
func TestMalformed(t *testing.T) {
	tests := []struct {
		name string
		req  string
		want string
	}{
		{name: "not JSON", req: `op=version`, want: "the request isn't a JSON object"},
		{name: "unknown field", req: `{"op": "version", "verbose": true}`, want: `unknown field \"verbose\"`},
		{name: "string id", req: `{"op": "version", "id": "x"}`, want: "the request isn't a JSON object"},
		{name: "unknown op", req: `{"op": "evaluate", "id": 9}`, want: `"id":9,"ok":false,"error":{"message":"unknown op \"evaluate\""`},
		{name: "no op", req: `{}`, want: `unknown op \"\"`},
		{name: "eval without a handle", req: `{"op": "eval", "input": {}}`, want: "no compiled policy has handle 0"},
		{name: "release of nothing", req: `{"op": "release", "handle": 3}`, want: "no compiled policy has handle 3"},
		{name: "explain of nothing", req: `{"op": "explain"}`, want: "the request holds no files"},
		{name: "format without a source", req: `{"op": "format"}`, want: "format needs a source"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := string(engine.New().Call([]byte(tt.req)))
			if !strings.Contains(got, tt.want) || !strings.Contains(got, `"ok":false`) {
				t.Errorf("Call(%s) = %s, want it to fail with %s", tt.req, got, tt.want)
			}
		})
	}
	if got := string(engine.Malformed("boom")); !strings.Contains(got, `"message":"boom"`) {
		t.Errorf("Malformed() = %s", got)
	}
	if got := string(engine.HostMisbehaved("bad memory")); got != `{"error":"bad memory"}` {
		t.Errorf("HostMisbehaved() = %s", got)
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		req  map[string]any
		want string
	}{
		{name: "canonical", req: map[string]any{"source": "policy a.b: K@1\n"}, want: `{"ok":true,"source":"policy a.b: K@1\n","formatted":true}`},
		{name: "reformatted", req: map[string]any{"source": "policy   a.b:K@1"}, want: `{"ok":true,"source":"policy a.b: K@1\n","formatted":false}`},
		{name: "syntax error", req: map[string]any{"source": "policy a.b: K@1\nwhen {\n", "path": "a/b.sigil"}, want: `"message":"a/b.sigil has syntax errors"`},
		{name: "syntax error from stdin", req: map[string]any{"source": "when {"}, want: `u003cstdin`}, // escaped, as the CLI's JSON is
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req["op"] = "format"
			if got := raw(t, engine.New(), tt.req); !strings.Contains(got, tt.want) {
				t.Errorf("format = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestCheck(t *testing.T) {
	const lets = "policy access.lets: Access@1\n\nlet unused = true\n\nwhen user.admin {\n  allow(reason: admin)\n}\n"
	const guard = "policy platform.guard: Access@1\n\nwhen user.name == \"\" {\n  deny(reason: banned)\n}\n"
	base := []file{{"kind.sigil", kind}, {"access/main.sigil", policy}}
	with := func(extra ...file) []file { return append(append([]file{}, base...), extra...) }
	tests := []struct {
		name  string
		req   map[string]any
		want  []string // substrings of the response
		fails bool     // ok is false
	}{
		{name: "clean", req: map[string]any{"files": base}, want: []string{`{"ok":true,"diagnostics":[]}`}},
		{name: "type error", req: map[string]any{"files": with(file{"access/other.sigil", other})}, want: []string{`"severity":"error"`, `"file":"access/other.sigil","document":"access.other"`, `"line":3`}},
		{name: "narrowed to a policy", req: map[string]any{"files": with(file{"access/other.sigil", other}), "policies": []string{"access.main"}}, want: []string{`"diagnostics":[]`}},
		{name: "pattern matching nothing", req: map[string]any{"files": base, "policies": []string{"nope.*"}}, want: []string{`no policy matches \"nope.*\"`}, fails: true},
		{name: "lint at its default", req: map[string]any{"files": with(file{"access/lets.sigil", lets})}, want: []string{`"severity":"warning","lint":"unused-let"`}},
		{name: "lint set to error", req: map[string]any{"files": with(file{"access/lets.sigil", lets}), "lints": map[string]string{"unused-let": "error"}}, want: []string{`"severity":"error","lint":"unused-let"`}},
		{name: "lint off", req: map[string]any{"files": with(file{"access/lets.sigil", lets}), "lints": map[string]string{"unused-let": "off"}}, want: []string{`"diagnostics":[]`}},
		{name: "unknown lint", req: map[string]any{"files": base, "lints": map[string]string{"unused-lets": "off"}}, want: []string{`unknown lint \"unused-lets\"`, `"help":"did you mean \"unused-let\"? the lints are unused-import, `}, fails: true},
		{name: "unknown level", req: map[string]any{"files": base, "lints": map[string]string{"unused-let": "loud"}}, want: []string{`unused-let: unknown level \"loud\"`}, fails: true},
		{name: "requirement met", req: map[string]any{"files": with(file{"platform/guard.sigil", guard}), "require": []map[string]any{{"policy": "platform.guard", "trusted": []string{"platform/"}, "roots": []string{"access.*"}}}}, want: []string{`"severity":"error"`, `access.main doesn't invoke platform.guard`}},
		{name: "requirement undefined", req: map[string]any{"files": base, "require": []map[string]any{{"policy": "platform.gaurd"}}}, want: []string{`require[0]: platform.gaurd is required, but no policy platform.gaurd was found`}, fails: true},
		{name: "requirement outside its trusted path", req: map[string]any{"files": with(file{"platform/guard.sigil", guard}, file{"other/x.sigil", "policy other.x: Access@1\n"}), "require": []map[string]any{{"policy": "platform.guard", "trusted": []string{"other"}}}}, want: []string{`require[0]: platform.guard must come from other, but it's defined at platform/guard.sigil`}, fails: true},
		{name: "trusted path holding nothing", req: map[string]any{"files": base, "require": []map[string]any{{"policy": "platform.guard", "trusted": []string{"platform"}}}}, want: []string{`require[0]: the trusted path platform of platform.guard holds no file`}, fails: true},
		{name: "requirement without a policy", req: map[string]any{"files": base, "require": []map[string]any{{"roots": []string{"x"}}}}, want: []string{`require[0] names no policy`}, fails: true},
		{name: "two requirements from one trusted path", req: map[string]any{"files": with(file{"platform/guard.sigil", guard}, file{"platform/other.sigil", strings.Replace(guard, "platform.guard", "platform.other", 1)}), "require": []map[string]any{{"policy": "platform.guard", "trusted": []string{"platform"}}, {"policy": "platform.other", "trusted": []string{"platform/"}}}}, want: []string{`access.main doesn't invoke platform.guard`, `access.main doesn't invoke platform.other`}},
		{name: "roots matching one policy twice", req: map[string]any{"files": with(file{"platform/guard.sigil", guard}), "require": []map[string]any{{"policy": "platform.guard", "roots": []string{"access.*", "access.main"}}}}, want: []string{`access.main doesn't invoke platform.guard`}},
		{name: "a requirement beside a document that doesn't check", req: map[string]any{"files": with(file{"platform/guard.sigil", guard}, file{"access/other.sigil", other}), "policies": []string{"access.main"}, "require": []map[string]any{{"policy": "platform.guard"}}}, want: []string{`access.main doesn't invoke platform.guard`}},
		{name: "no files", req: map[string]any{}, want: []string{"the request holds no files"}, fails: true},
		{name: "a file without a path", req: map[string]any{"files": []file{{"", kind}}}, want: []string{"file 1 has no path"}, fails: true},
		{name: "a file given twice", req: map[string]any{"files": []file{{"./kind.sigil", kind}, {"kind.sigil", kind}, {"a.sigil", policy}}}, want: []string{`"diagnostics":[]`}},
		{name: "a path given twice", req: map[string]any{"files": []file{{"./kind.sigil", kind}, {"kind.sigil", kind + "\n"}}}, want: []string{"kind.sigil is given twice, with two sources"}, fails: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req["op"] = "check"
			got := raw(t, engine.New(), tt.req)
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("check = %s, want it to contain %s", got, want)
				}
			}
			if failed := strings.HasPrefix(got, `{"ok":false`); failed != tt.fails {
				t.Errorf("check = %s, want failed = %v", got, tt.fails)
			}
		})
	}
}

func TestCompileErrors(t *testing.T) {
	tests := []struct {
		name  string
		req   map[string]any
		want  []string
		diags bool // the failure carries diagnostics
	}{
		{name: "two policies, none named", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}, {"b.sigil", strings.Replace(other, "user.nmae", "user.name", 1)}}}, want: []string{"the bundle holds several policies"}},
		{name: "unknown policy", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}, "policy": "access.nope"}, want: []string{`no policy matches \"access.nope\"`}},
		{name: "the bundle doesn't check", req: map[string]any{"files": []file{{"kind.sigil", "kind Access version 1\n\ninput x: nope\n"}, {"a.sigil", policy}}}, want: []string{"the bundle doesn't check, so nothing was compiled"}, diags: true},
		{name: "the root doesn't check", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}, {"b.sigil", other}}, "policy": "access.other"}, want: []string{"the bundle doesn't check, so nothing was compiled", `"line":3`}, diags: true},
		{name: "unknown function", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}, "functions": []string{"ownr"}}, want: []string{"functions: the kind Access has no host function ownr", `did you mean \"owner\"?`}},
		{name: "a function far from every name", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}, "functions": []string{"zzzzzzzzzz"}}, want: []string{"the kind declares: owner, ttl_for"}},
		{name: "a function of a kind without any", req: map[string]any{"files": []file{{"k.sigil", "kind K version 1\n\ninput x: int\n\ndecision allow {\n  reason: yes\n}\n\ncollect one\nprecedence allow\n\ndefault allow(reason: yes)\n"}, {"p.sigil", "policy k.main: K@1\n"}}, "functions": []string{"f"}}, want: []string{"the kind declares no host functions"}},
		{name: "no files", req: map[string]any{}, want: []string{"the request holds no files"}},
		{name: "stub of an unknown function", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}, "stubs": map[string]any{"ownr": map[string]any{"returns": "ada"}}}, want: []string{"stubs: the kind has no host function ownr to stub"}},
		{name: "stub of the wrong type", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}, "stubs": map[string]any{"owner": map[string]any{"returns": []int{1}}}}, want: []string{"stubs: stub owner"}},
		{name: "stubs that aren't an object", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}, "stubs": []int{1}}, want: []string{"stubs: "}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req["op"] = "compile"
			got := raw(t, engine.New(), tt.req)
			if !strings.HasPrefix(got, `{"ok":false`) {
				t.Fatalf("compile = %s, want it to fail", got)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("compile = %s, want it to contain %s", got, want)
				}
			}
			if has := strings.Contains(got, `"diagnostics":[`); has != tt.diags {
				t.Errorf("compile = %s, want diagnostics = %v", got, tt.diags)
			}
		})
	}
}

// TestCompileChecksEveryDocument checks that compile, as a host's
// Kind.Load, fails on a broken document the root never uses, among the
// files or the trusted files, with that document's diagnostics: the ones
// check reports for the same files.
func TestCompileChecksEveryDocument(t *testing.T) {
	const guard = "policy platform.guard: Access@1\n\nwhen age > 30d {\n  deny(reason: too_old)\n}\n"
	const brokenGuard = "policy platform.broken: Access@1\n\nwhen user.nmae == \"x\" {\n  deny(reason: banned)\n}\n"
	root := file{"access/main.sigil", policy}
	tests := []struct {
		name    string
		files   []file
		trusted []file
		want    string // in the diagnostics, re-encoded with sorted keys
	}{
		{name: "a type error among the files", files: []file{{"kind.sigil", kind}, root, {"access/other.sigil", other}}, want: `"document":"access.other","file":"access/other.sigil","help":"did you mean \"name\"? User declares: name, teams, admin","line":3,"message":"unknown field`},
		{name: "a syntax error among the files", files: []file{{"kind.sigil", kind}, root, {"access/other.sigil", "policy access.other: Access@1\n\nwhen {\n"}}, want: `"file":"access/other.sigil"`},
		{name: "a document of an unknown kind", files: []file{{"kind.sigil", kind}, root, {"x.sigil", "policy x.y: Nope@1\n"}}, want: "no kind Nope was found"},
		{name: "a type error among the trusted files", files: []file{{"kind.sigil", kind}, root}, trusted: []file{{"platform/guard.sigil", guard}, {"platform/broken.sigil", brokenGuard}}, want: `"document":"platform.broken","file":"platform/broken.sigil"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := engine.New()
			compiled := call(t, e, map[string]any{"op": "compile", "policy": "access.main", "files": tt.files, "trusted_files": tt.trusted})
			checked := call(t, e, map[string]any{"op": "check", "files": tt.files, "trusted_files": tt.trusted})
			problem, _ := compiled["error"].(map[string]any)
			if compiled["ok"] != false || problem["message"] != "the bundle doesn't check, so nothing was compiled" {
				t.Fatalf("compile = %v, want it to fail", compiled)
			}
			if !reflect.DeepEqual(compiled["diagnostics"], checked["diagnostics"]) {
				t.Errorf("compile's diagnostics\n%v\ndiffer from check's\n%v", compiled["diagnostics"], checked["diagnostics"])
			}
			if got, _ := json.Marshal(compiled["diagnostics"]); !strings.Contains(string(got), tt.want) {
				t.Errorf("diagnostics = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestEval(t *testing.T) {
	// ttl2h30 implements both host functions: ada owns everything, and
	// every ttl is 2h30m.
	ttl2h30 := func(req []byte) []byte {
		if strings.Contains(string(req), `"function":"owner"`) {
			return []byte(`{"result": "ada"}`)
		}
		return []byte(`{"result": "2h30m"}`)
	}
	files := []file{{"kind.sigil", kind}, {"access/main.sigil", policy}}
	admin := `{"user": {"name": "ada", "admin": true}, "age": "1h"}`
	tests := []struct {
		name      string
		host      engine.Host
		functions []string
		stubs     map[string]any
		input     string
		want      []string
	}{
		{name: "default", input: `{"user": {"name": "cy"}}`, want: []string{`"decision":"deny","reason":"no_rule_matched"`, `"trace":[]`}},
		{name: "host function", host: ttl2h30, functions: []string{"owner", "ttl_for"}, input: admin,
			want: []string{`"payload":{"ttl":"2h30m"},"policy":"access.main","decision":"allow","reason":"admin"`, `"position":"access/main.sigil:6:3"`}},
		{name: "host function in a condition", host: ttl2h30, functions: []string{"owner"}, input: `{"user": {"name": "ada"}, "resource": "vault"}`, want: []string{`"decision":"allow","reason":"team_member"`}},
		{name: "host error", host: answer(`{"error": "directory unavailable"}`), functions: []string{"owner"}, input: `{"user": {"name": "ada"}, "resource": "vault"}`, want: []string{`"kind":"runtime"`, "host function owner failed: directory unavailable"}},
		{name: "host error that isn't a string", host: answer(`{"error": 1}`), functions: []string{"owner"}, input: `{"user": {"name": "ada"}, "resource": "vault"}`, want: []string{"host function owner failed: the host answered with an error that isn't a string"}},
		{name: "host answering neither", host: answer(`{}`), functions: []string{"ttl_for"}, input: admin, want: []string{"host function ttl_for failed: the host answered with neither a result nor an error", `a null result is {\"result\": null}`}},
		{name: "host answering null", host: answer(`{"result": null}`), functions: []string{"ttl_for"}, input: admin, want: []string{"host function ttl_for failed: the host returned result"}},
		{name: "host answering garbage", host: answer(`not json`), functions: []string{"ttl_for"}, input: admin, want: []string{"the host's answer isn't a JSON object"}},
		{name: "host answering the wrong type", host: answer(`{"result": 5}`), functions: []string{"ttl_for"}, input: admin, want: []string{"host function ttl_for failed: the host returned result: ", "fn ttl_for(User, duration)"}},
		{name: "no host", functions: []string{"ttl_for"}, input: admin, want: []string{"host function ttl_for failed: no host is attached to run it", `"help":"the engine runs without a host; stub ttl_for instead"`}},
		{name: "unbound", input: admin, want: []string{"host function ttl_for failed: no implementation in this sigil binary", "--stub ttl_for=VALUE"}},
		{name: "stub", stubs: map[string]any{"ttl_for": map[string]any{"returns": "3h"}}, input: admin, want: []string{`"payload":{"ttl":"3h"}`}},
		{name: "stub replacing a host function", host: ttl2h30, functions: []string{"ttl_for"}, stubs: map[string]any{"ttl_for": map[string]any{"error": "stubbed out"}}, input: admin, want: []string{"host function ttl_for failed: stubbed out"}},
		{name: "assert", input: `{}`, want: []string{`"error":{"kind":"assertion","phase":"input"`, `"reason":"named_user","policy":"access.main","position":"access/main.sigil:3:1"`}},
		{name: "unknown input", input: `{"usr": {}}`, want: []string{`"ok":false`, `the input: `, "usr"}},
		{name: "not an object", input: `[1]`, want: []string{`"ok":false`, "the input: "}},
		{name: "no input", want: []string{`"ok":false`, "eval needs an input"}},
		{name: "null input", input: `null`, want: []string{`"ok":false`, "eval needs an input"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := engine.New(engine.WithHost(tt.host))
			h := compile(t, e, map[string]any{"files": files, "functions": tt.functions, "stubs": tt.stubs})
			req := `{"op": "eval", "handle": ` + fmt.Sprint(h)
			if tt.input != "" {
				req += `, "input": ` + tt.input
			}
			got := string(e.Call([]byte(req + "}")))
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("eval = %s, want it to contain %s", got, want)
				}
			}
		})
	}
}

// TestInvocation checks an evaluation through an invoked policy: the
// candidate's chain, and a compile error only a compile finds, an
// invocation argument out of its param's bounds.
func TestInvocation(t *testing.T) {
	const limits = "policy access.limits: Access@1\n\nparam max_age: duration = 30d, min: 1d\n\nwhen age > max_age {\n  deny(reason: too_old)\n}\n"
	chained := func(maxAge string) string {
		return "policy access.chained: Access@1\n\nuse access.limits\n\nlimits(max_age: " + maxAge + ")\n"
	}
	e := engine.New()
	files := []file{{"kind.sigil", kind}, {"access/limits.sigil", limits}, {"access/chained.sigil", chained("2d")}}
	h := compile(t, e, map[string]any{"files": files, "policy": "access.chained"})
	got := raw(t, e, map[string]any{"op": "eval", "handle": h, "input": map[string]any{"user": map[string]any{"name": "cy"}, "age": "3d"}})
	if want := `"chain":["access/chained.sigil:5:1"]`; !strings.Contains(got, want) {
		t.Errorf("eval = %s, want the chain %s", got, want)
	}

	files[2] = file{"access/chained.sigil", chained("1h")}
	for _, req := range []map[string]any{
		{"op": "compile", "files": files, "policy": "access.chained"},
		{"op": "explain", "files": files, "policy": "access.chained"},
	} {
		got := raw(t, e, req)
		if !strings.Contains(got, `"ok":false`) || !strings.Contains(got, `"file":"access/chained.sigil"`) || !strings.Contains(got, "doesn't compile") {
			t.Errorf("%s = %s, want it stopped by the out-of-bounds argument", req["op"], got)
		}
	}
}

// TestHostArgsOutsideJSON checks that a host function call whose args
// JSON can't carry, an infinite float, fails as a runtime error rather
// than reaching the host.
func TestHostArgsOutsideJSON(t *testing.T) {
	const k = "kind Ratio version 1\n\ninput x: float\n\nfn ratio(float) -> float\n\ndecision allow {\n  reason: big\n}\n\ncollect one\nprecedence allow\n\ndefault allow(reason: big)\n"
	const p = "policy ratio.main: Ratio@1\n\nwhen ratio(x + x) > 1.0 {\n  allow(reason: big)\n}\n"
	called := false
	e := engine.New(engine.WithHost(func([]byte) []byte { called = true; return []byte(`{"result": 2.0}`) }))
	h := compile(t, e, map[string]any{"files": []file{{"k.sigil", k}, {"p.sigil", p}}, "functions": []string{"ratio"}})
	got := raw(t, e, map[string]any{"op": "eval", "handle": h, "input": map[string]any{"x": 1.7e308}})
	if !strings.Contains(got, "host function ratio failed: its args can't be sent as JSON") || called {
		t.Errorf("eval = %s, called = %v; want the call to fail before the host", got, called)
	}
}

// TestPanic checks that a panic while handling a request, here in the
// host, is an answer rather than the end of the engine.
func TestPanic(t *testing.T) {
	e := engine.New(engine.WithHost(func([]byte) []byte { panic("host exploded") }))
	h := compile(t, e, map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}, "functions": []string{"owner"}})
	got := raw(t, e, map[string]any{"op": "eval", "id": 12, "handle": h, "input": map[string]any{"user": map[string]any{"name": "ada"}, "resource": "vault"}})
	if !strings.HasPrefix(got, `{"id":12,"ok":false`) || !strings.Contains(got, "sigil failed while handling the request: host exploded") {
		t.Errorf("eval = %s, want the panic as an answer", got)
	}
	if got := raw(t, e, map[string]any{"op": "release", "handle": h}); got != `{"ok":true}` {
		t.Errorf("release after the panic = %s", got)
	}
}

// TestTimeout checks that an evaluation past its deadline stops, with
// the fallback and a canceled error, however long the loop it's in.
func TestTimeout(t *testing.T) {
	e := engine.New()
	h := compile(t, e, map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", policy}}})
	names := make([]string, 500_000)
	for i := range names {
		names[i] = "x"
	}
	input, _ := json.Marshal(map[string]any{"user": map[string]any{"name": "cy"}, "names": names})
	resp := call(t, e, map[string]any{"op": "eval", "handle": h, "input": json.RawMessage(input), "timeout_ms": 1})
	failure, _ := resp["error"].(map[string]any)
	if failure["kind"] != "canceled" || resp["decision"] != "deny" || !strings.Contains(fmt.Sprint(failure["message"]), "deadline exceeded") {
		t.Errorf("eval = %v, want a canceled evaluation that falls back to deny", resp)
	}
	resp = call(t, e, map[string]any{"op": "eval", "handle": h, "input": json.RawMessage(input), "timeout_ms": 60_000})
	if resp["error"] != nil || resp["reason"] != "no_rule_matched" {
		t.Errorf("eval = %v, want it to finish in time", resp)
	}
}

func TestExplain(t *testing.T) {
	files := []file{{"kind.sigil", kind}, {"access/main.sigil", policy}}
	e := engine.New()
	h := compile(t, e, map[string]any{"files": files})
	byHandle := raw(t, e, map[string]any{"op": "explain", "handle": h})
	byFiles := raw(t, e, map[string]any{"op": "explain", "files": files})
	byPattern := raw(t, e, map[string]any{"op": "explain", "files": files, "policy": "access.*"})
	if byHandle != byFiles || byFiles != byPattern {
		t.Errorf("explain by handle, files and pattern differ:\n%s\n%s\n%s", byHandle, byFiles, byPattern)
	}
	for _, want := range []string{`"explanations":[{"policy":"access.main","policies":1,"modules":0,"rules":[`, `"kind":"assert","reason":"named_user","phase":"input"`, `"chain":["access.main:6"]`} {
		if !strings.Contains(byHandle, want) {
			t.Errorf("explain = %s, want it to contain %s", byHandle, want)
		}
	}
	tests := []struct {
		name string
		req  map[string]any
		want string
	}{
		{name: "both", req: map[string]any{"handle": h, "files": files}, want: "explain takes a handle or files, not both"},
		{name: "unknown handle", req: map[string]any{"handle": 99}, want: "no compiled policy has handle 99"},
		{name: "no policies", req: map[string]any{"files": files[:1]}, want: "the bundle holds no policies"},
		{name: "no match", req: map[string]any{"files": files, "policy": "x.*"}, want: `no policy matches \"x.*\"`},
		{name: "bundle doesn't check", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", other}}, "policy": "x"}, want: "the bundle doesn't check, so nothing was explained"},
		{name: "root doesn't check", req: map[string]any{"files": []file{{"kind.sigil", kind}, {"a.sigil", other}}}, want: "the policies to explain don't check, so nothing was explained"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.req["op"] = "explain"
			if got := raw(t, e, tt.req); !strings.Contains(got, tt.want) || !strings.HasPrefix(got, `{"ok":false`) {
				t.Errorf("explain = %s, want it to fail with %s", got, tt.want)
			}
		})
	}
}

// TestRelease checks that a released handle is gone, and that handles
// aren't reused.
func TestRelease(t *testing.T) {
	e := engine.New()
	files := []file{{"kind.sigil", kind}, {"a.sigil", policy}}
	h1 := compile(t, e, map[string]any{"files": files})
	if got := raw(t, e, map[string]any{"op": "release", "handle": h1, "id": 2}); got != `{"id":2,"ok":true}` {
		t.Errorf("release = %s", got)
	}
	if got := raw(t, e, map[string]any{"op": "release", "handle": h1}); !strings.Contains(got, "no compiled policy has handle 1") {
		t.Errorf("second release = %s", got)
	}
	if h2 := compile(t, e, map[string]any{"files": files}); h2 == h1 {
		t.Errorf("compile after release = handle %v again", h2)
	}
}

// TestConcurrentEvals runs evaluations of one handle, and compiles and
// releases of others, at once, for the race detector.
func TestConcurrentEvals(t *testing.T) {
	e := engine.New()
	files := []file{{"kind.sigil", kind}, {"a.sigil", policy}}
	h := compile(t, e, map[string]any{"files": files})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 20 {
				resp := call(t, e, map[string]any{"op": "eval", "handle": h, "input": map[string]any{"user": map[string]any{"name": "cy"}}})
				if resp["reason"] != "no_rule_matched" {
					t.Errorf("eval = %v", resp)
				}
				other := compile(t, e, map[string]any{"files": files})
				raw(t, e, map[string]any{"op": "explain", "handle": other})
				raw(t, e, map[string]any{"op": "release", "handle": other})
			}
		})
	}
	wg.Wait()
}

// compile compiles a policy and returns its handle.
func compile(t *testing.T, e *engine.Engine, req map[string]any) float64 {
	t.Helper()
	req["op"] = "compile"
	resp := call(t, e, req)
	h, ok := resp["handle"].(float64)
	if resp["ok"] != true || !ok {
		t.Fatalf("compile = %v", resp)
	}
	return h
}

// call sends a request and decodes the response.
func call(t *testing.T, e *engine.Engine, req map[string]any) response {
	t.Helper()
	var resp response
	if err := json.Unmarshal([]byte(raw(t, e, req)), &resp); err != nil {
		t.Fatalf("the response isn't JSON: %v", err)
	}
	return resp
}

// raw sends a request and returns the response as it is.
func raw(t *testing.T, e *engine.Engine, req map[string]any) string {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return string(e.Call(body))
}

// answer returns a host that answers every call with resp.
func answer(resp string) engine.Host {
	return func([]byte) []byte { return []byte(resp) }
}

// TestRequire checks compile's requirements, policy.Require with
// policy.From(trusted files): the root must invoke the trusted policy
// unconditionally, with arguments in its params' bounds, and no other
// document may take its name or its files' paths.
func TestRequire(t *testing.T) {
	const guard = "policy platform.guard: Access@1\n\nparam max_age: duration = 30d, min: 1d\n\nwhen age > max_age {\n  deny(reason: too_old)\n}\n"
	team := func(body string) file {
		return file{"team/main.sigil", "policy team.main: Access@1\n\nuse platform.guard\n\n" + body + "\n\nwhen user.admin {\n  allow(reason: admin)\n}\n"}
	}
	trusted := []file{{"platform/guard.sigil", guard}}
	req := []map[string]any{{"policy": "platform.guard"}}
	tests := []struct {
		name    string
		files   []file
		trusted []file
		require []map[string]any
		want    []string // substrings of the response
		ok      bool
	}{
		{name: "invoked", files: []file{{"kind.sigil", kind}, team("guard(max_age: 2d)")}, trusted: trusted, require: req, ok: true},
		{name: "invoked with defaults", files: []file{{"kind.sigil", kind}, team("guard()")}, trusted: trusted, require: req, ok: true},
		{
			name:  "not invoked",
			files: []file{{"kind.sigil", kind}, {"team/main.sigil", "policy team.main: Access@1\n\nwhen user.admin {\n  allow(reason: admin)\n}\n"}}, trusted: trusted, require: req,
			want: []string{`"message":"team.main doesn't invoke platform.guard"`, `"file":"team/main.sigil","document":"team.main"`, "the host requires platform.guard for every Access policy"},
		},
		{
			name:  "invoked under when",
			files: []file{{"kind.sigil", kind}, team("when user.name == \"x\" {\n  guard()\n}")}, trusted: trusted, require: req,
			want: []string{`"message":"platform.guard must be invoked unconditionally"`, `"line":6`},
		},
		{
			name:  "redefined by the team",
			files: []file{{"kind.sigil", kind}, team("guard()"), {"team/guard.sigil", "policy platform.guard: Access@1\n"}}, trusted: trusted, require: req,
			want: []string{`"message":"policy platform.guard is defined twice"`, "the name belongs to the trusted source, defined at platform/guard.sigil:1:1", `"file":"team/guard.sigil"`},
		},
		{
			name:  "redefined by the team, and not invoked",
			files: []file{{"kind.sigil", kind}, {"team/main.sigil", "policy team.main: Access@1\n"}, {"team/guard.sigil", "policy platform.guard: Access@1\n"}}, trusted: trusted, require: req,
			want: []string{`"message":"policy platform.guard is defined twice"`},
		},
		{
			name:  "argument out of bounds",
			files: []file{{"kind.sigil", kind}, team("guard(max_age: 1h)")}, trusted: trusted, require: req,
			want: []string{"the policy doesn't compile", `"file":"team/main.sigil"`, "max_age"},
		},
		{
			name:  "a team file at a trusted path",
			files: []file{{"kind.sigil", kind}, team("guard()"), {"platform/guard.sigil", "policy platform.guard: Access@1\n"}}, trusted: trusted, require: req,
			want: []string{"platform/guard.sigil is among both the files and the trusted files"},
		},
		{
			name:  "only the team defines it",
			files: []file{{"kind.sigil", kind}, team("guard()"), {"team/guard.sigil", guard}}, trusted: []file{{"platform/other.sigil", "policy platform.other: Access@1\n"}}, require: req,
			want: []string{`"message":"require[0]: platform.guard isn't among the trusted files"`, `"message":"platform.guard must come from the trusted files, but it's defined here","help":"the host reads a required policy only from its trusted source`, `"file":"team/guard.sigil"`},
		},
		{
			name:  "nobody defines it, though the root uses it",
			files: []file{{"kind.sigil", kind}, team("guard()")}, trusted: []file{{"platform/other.sigil", "policy platform.other: Access@1\n"}}, require: req,
			want: []string{"require[0]: platform.guard is required, but the trusted files define no policy platform.guard"},
		},
		{
			name:  "nobody defines it",
			files: []file{{"kind.sigil", kind}, {"team/main.sigil", "policy team.main: Access@1\n"}}, trusted: []file{{"platform/other.sigil", "policy platform.other: Access@1\n"}}, require: req,
			want: []string{"require[0]: platform.guard is required, but the trusted files define no policy platform.guard"},
		},
		{
			name:  "without trusted files, a policy of the bundle",
			files: []file{{"kind.sigil", kind}, team("guard()"), {"team/guard.sigil", guard}}, require: req, ok: true,
		},
		{
			name:  "without trusted files, nobody defines it",
			files: []file{{"kind.sigil", kind}, {"team/main.sigil", "policy team.main: Access@1\n"}}, require: req,
			want: []string{"team.main doesn't invoke platform.guard"},
		},
		{
			name:  "a policy of another kind",
			files: []file{{"kind.sigil", kind}, {"team/main.sigil", "policy team.main: Access@1\n"}}, trusted: []file{{"k.sigil", "kind Other version 1\n\ninput x: int\n\ndecision ok {\n  reason: yes\n}\n\ncollect one\nprecedence ok\n\ndefault ok(reason: yes)\n"}, {"other.sigil", "policy other.guard: Other@1\n"}},
			require: []map[string]any{{"policy": "other.guard"}}, ok: true,
		},
		{name: "roots", files: []file{{"kind.sigil", kind}, team("guard()")}, trusted: trusted, require: []map[string]any{{"policy": "platform.guard", "roots": []string{"x"}}}, want: []string{"require[0]: compile's requirements take only a policy"}},
		{name: "no policy", files: []file{{"kind.sigil", kind}, team("guard()")}, trusted: trusted, require: []map[string]any{{}}, want: []string{"require[0] names no policy"}},
		{name: "a trusted file without a path", files: []file{{"kind.sigil", kind}, team("guard()")}, trusted: []file{{"", guard}}, require: req, want: []string{"trusted file 1 has no path"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := engine.New()
			got := raw(t, e, map[string]any{"op": "compile", "policy": "team.main", "files": tt.files, "trusted_files": tt.trusted, "require": tt.require})
			if ok := strings.HasPrefix(got, `{"ok":true`); ok != tt.ok {
				t.Fatalf("compile = %s, want ok = %v", got, tt.ok)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("compile = %s, want it to contain %s", got, want)
				}
			}
			if !tt.ok {
				return
			}
			resp := call(t, e, map[string]any{"op": "eval", "handle": 1, "input": map[string]any{"user": map[string]any{"name": "ada", "admin": true}, "age": "40d"}})
			if resp["ok"] != true || resp["error"] != nil {
				t.Errorf("eval = %v", resp)
			}
		})
	}
}

// TestIDEcho checks that every response that can carry the request's id
// does: even one to a request that names a field no op takes, or gives a
// field the wrong type. Only a request that isn't JSON, or whose id isn't
// a number, has none to echo.
func TestIDEcho(t *testing.T) {
	tests := []struct {
		name string
		req  string
		want string // the response's start
	}{
		{name: "unknown field", req: `{"op": "eval", "id": 5, "handle": 1, "timeoutMs": 10}`, want: `{"id":5,"ok":false,"error":{"message":"the request isn't a JSON object of the fields an op takes: json: unknown field \"timeoutMs\""`},
		{name: "field of the wrong type", req: `{"op": "eval", "id": 6, "handle": "one"}`, want: `{"id":6,"ok":false,"error":{"message":"the request isn't a JSON object`},
		{name: "unknown op", req: `{"op": "evaluate", "id": 7}`, want: `{"id":7,"ok":false`},
		{name: "not JSON", req: `{"op": "eval", "id": 8,`, want: `{"ok":false,"error":{"message":"the request isn't a JSON object`},
		{name: "an id that isn't a number", req: `{"op": "version", "id": "x"}`, want: `{"ok":false,"error":{"message":"the request isn't a JSON object`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(engine.New().Call([]byte(tt.req))); !strings.HasPrefix(got, tt.want) {
				t.Errorf("Call(%s) = %s, want it to start %s", tt.req, got, tt.want)
			}
		})
	}
}
