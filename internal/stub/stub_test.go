package stub_test

import (
	"reflect"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/stub"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want stub.Set
		errs []string // each problem's Error(), in order
		help string   // the first problem's help, when checked
	}{
		{name: "empty document", src: ``},
		{name: "null", src: `~`},
		{name: "empty object", src: `{}`, want: stub.Set{}},
		{
			name: "returns",
			src:  "owner:\n  returns: ada\n",
			want: stub.Set{"owner": {Name: "owner", Line: 1, Returns: &stub.Value{Raw: "ada", Line: 2}}},
		},
		{
			name: "returns null",
			src:  "teams:\n  returns: null\n",
			want: stub.Set{"teams": {Name: "teams", Line: 1, Returns: &stub.Value{Line: 2}}},
		},
		{
			name: "error",
			src:  "owner: {error: directory unavailable}\n",
			want: stub.Set{"owner": {Name: "owner", Line: 1, Error: "directory unavailable"}},
		},
		{
			name: "calls with a fallback",
			src:  "owner:\n  calls:\n    - args: [prod]\n      returns: ada\n    - {args: [dev], error: nope}\n  returns: bob\n",
			want: stub.Set{"owner": {Name: "owner", Line: 1, Returns: &stub.Value{Raw: "bob", Line: 6}, Calls: []*stub.Call{
				{Line: 3, Args: []*stub.Value{{Raw: "prod", Line: 3}}, Returns: &stub.Value{Raw: "ada", Line: 4}},
				{Line: 5, Args: []*stub.Value{{Raw: "dev", Line: 5}}, Error: "nope"},
			}}},
		},
		{
			name: "calls alone",
			src:  "owner:\n  calls:\n    - {args: [], returns: ada}\n",
			want: stub.Set{"owner": {Name: "owner", Line: 1, Calls: []*stub.Call{{Line: 3, Args: []*stub.Value{}, Returns: &stub.Value{Raw: "ada", Line: 3}}}}},
		},
		{
			name: "aliases",
			src:  "a: &s {returns: ada}\nb: *s\n",
			want: stub.Set{
				"a": {Name: "a", Line: 1, Returns: &stub.Value{Raw: "ada", Line: 1}},
				"b": {Name: "b", Line: 2, Returns: &stub.Value{Raw: "ada", Line: 1}},
			},
		},
		{name: "not an object", src: `[owner]`, errs: []string{"line 1: stubs must be an object"}},
		{name: "a key that isn't a name", src: `{[a]: {returns: 1}}`, errs: []string{"line 1: a stub's key must be a host function's name"}},
		{
			name: "stubbed twice",
			src:  "owner: {returns: a}\nowner: {returns: b}\n",
			errs: []string{"line 2: owner is stubbed twice"},
			help: "the first stub is on line 1; give each host function one",
		},
		{name: "a stub that isn't an object", src: `owner: ada`, errs: []string{"line 1: stub owner must be an object with returns, error, calls"}},
		{
			name: "unknown key",
			src:  "owner: {returnz: ada}\n",
			errs: []string{`line 1: stub owner: unknown key "returnz"`, "line 1: stub owner gives no result"},
			help: `did you mean "returns"? a stub takes returns, error, calls`,
		},
		{
			name: "unknown key nothing like the others",
			src:  "owner: {returns: ada, zzzzzzzz: 1}\n",
			errs: []string{`line 1: stub owner: unknown key "zzzzzzzz"`},
			help: "a stub takes returns, error, calls",
		},
		{name: "a key that isn't a scalar", src: "owner: {[a]: 1, returns: ada}\n", errs: []string{`line 1: stub owner: unknown key ""`}},
		{name: "a key given twice", src: "owner: {returns: a, returns: b}\n", errs: []string{"line 1: stub owner: returns is given twice"}},
		{name: "both returns and error", src: "owner: {returns: ada, error: nope}\n", errs: []string{"line 1: stub owner gives both returns and error"}},
		{name: "in line order", src: "owner:\n  returns: a\n  error: b\n  retrns: c\n", errs: []string{"line 1: stub owner gives both returns and error", `line 4: stub owner: unknown key "retrns"`}},
		{name: "no result", src: "owner: {}\n", errs: []string{"line 1: stub owner gives no result"}},
		{name: "no calls", src: "owner: {calls: []}\n", errs: []string{"line 1: stub owner gives no result"}},
		{name: "empty error", src: "owner: {error: ''}\n", errs: []string{"line 1: stub owner: error must be a message"}},
		{name: "null error", src: "owner: {error: null}\n", errs: []string{"line 1: stub owner: error must be a message"}},
		{name: "error that isn't text", src: "owner:\n  error: [a]\n", errs: []string{"line 2: stub owner: error must be a message"}},
		{name: "calls that aren't a list", src: "owner:\n  calls: {args: []}\n", errs: []string{"line 2: stub owner: calls must be a list"}},
		{name: "a call that isn't an object", src: "owner:\n  calls: [ada]\n", errs: []string{"line 2: stub owner: call 1 must be an object with args, returns, error"}},
		{name: "a call without args", src: "owner:\n  calls:\n    - returns: ada\n", errs: []string{"line 3: stub owner: call 1 has no args"}},
		{name: "args that aren't a list", src: "owner:\n  calls:\n    - {args: prod, returns: ada}\n", errs: []string{"line 3: stub owner: call 1: args must be a list"}},
		{name: "a call without a result", src: "owner:\n  calls:\n    - args: [prod]\n", errs: []string{"line 3: stub owner: call 1 needs exactly one of returns and error"}},
		{name: "a call with both", src: "owner:\n  calls:\n    - {args: [prod], returns: a, error: b}\n", errs: []string{"line 3: stub owner: call 1 needs exactly one of returns and error"}},
		{name: "a call with a bad error", src: "owner:\n  calls:\n    - {args: [prod], error: [x]}\n", errs: []string{"line 3: stub owner: call 1: error must be a message"}},
		{name: "a call with an unknown key", src: "owner:\n  calls:\n    - {arg: [prod], returns: a}\n", errs: []string{`line 3: stub owner: call 1: unknown key "arg"`, "line 3: stub owner: call 1 has no args"}},
		{name: "a value that doesn't decode", src: "owner: {returns: !!int x}\n", errs: []string{"line 1: stub owner: cannot decode !!str `x` as a !!int"}},
		{name: "an arg that doesn't decode", src: "owner:\n  calls:\n    - {args: [!!int x], returns: a}\n", errs: []string{"line 3: stub owner: call 1: cannot decode !!str `x` as a !!int"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var doc yaml.Node
			if err := yaml.Unmarshal([]byte(tt.src), &doc); err != nil {
				t.Fatal(err)
			}
			got, errs := stub.Parse(&doc)
			checkParse(t, got, errs, tt.want, tt.errs, tt.help)
		})
	}
}

func TestParseNil(t *testing.T) {
	if got, errs := stub.Parse(nil); got != nil || errs != nil {
		t.Errorf("Parse(nil) = %v, %v", got, errs)
	}
	if got, errs := stub.Parse(&yaml.Node{Kind: yaml.DocumentNode}); got != nil || errs != nil {
		t.Errorf("Parse(empty document) = %v, %v", got, errs)
	}
}

func TestParseDocument(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want stub.Set
		errs []string
	}{
		{name: "empty", src: ""},
		{name: "yaml", src: "owner: {returns: ada}\n", want: stub.Set{"owner": {Name: "owner", Line: 1, Returns: &stub.Value{Raw: "ada", Line: 1}}}},
		{name: "json", src: `{"owner": {"returns": ["a", 1]}}`, want: stub.Set{"owner": {Name: "owner", Line: 1, Returns: &stub.Value{Raw: []any{"a", 1}, Line: 1}}}},
		{name: "not yaml", src: "owner: [\n", errs: []string{"not valid YAML or JSON: line 1: did not find expected node content"}},
		{name: "wrong shape", src: "owner: {}\n", errs: []string{"line 1: stub owner gives no result"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := stub.ParseDocument([]byte(tt.src))
			checkParse(t, got, errs, tt.want, tt.errs, "")
		})
	}
}

func TestParseFlag(t *testing.T) {
	tests := []struct {
		flag string
		want stub.Set
		err  string
	}{
		{flag: "owner=ada", want: stub.Set{"owner": {Name: "owner", Returns: &stub.Value{Raw: "ada"}}}},
		{flag: " owner =ada", want: stub.Set{"owner": {Name: "owner", Returns: &stub.Value{Raw: "ada"}}}},
		{flag: "teams=[a, b]", want: stub.Set{"teams": {Name: "teams", Returns: &stub.Value{Raw: []any{"a", "b"}}}}},
		{flag: `user={"name": "ada", "n": 2}`, want: stub.Set{"user": {Name: "user", Returns: &stub.Value{Raw: map[string]any{"name": "ada", "n": 2}}}}},
		{flag: "eq=a=b", want: stub.Set{"eq": {Name: "eq", Returns: &stub.Value{Raw: "a=b"}}}},
		{flag: "none=", want: stub.Set{"none": {Name: "none", Returns: &stub.Value{}}}},
		{flag: "owner", err: `stub "owner" isn't NAME=VALUE`},
		{flag: "=ada", err: `stub "=ada" isn't NAME=VALUE`},
		{flag: "teams=[a", err: "stub teams: the value isn't valid JSON or YAML: line 1: did not find expected ',' or ']'"},
	}
	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			got, err := stub.ParseFlag(tt.flag)
			if tt.err != "" {
				if err == nil || err.Error() != tt.err || err.Help == "" {
					t.Fatalf("ParseFlag(%q) error = %v, want %q with help", tt.flag, err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseFlag(%q): %v", tt.flag, err)
			}
			if !reflect.DeepEqual(strip(got), tt.want) {
				t.Errorf("ParseFlag(%q) = %s, want %s", tt.flag, dump(got), dump(tt.want))
			}
		})
	}
}

func TestMerge(t *testing.T) {
	a := &stub.Func{Name: "a", Error: "base"}
	b := &stub.Func{Name: "b", Error: "base"}
	b2 := &stub.Func{Name: "b", Error: "over"}
	c := &stub.Func{Name: "c", Error: "over"}
	base := stub.Set{"a": a, "b": b}
	tests := []struct {
		name       string
		base, over stub.Set
		want       stub.Set
	}{
		{name: "both empty"},
		{name: "no overrides", base: base, want: base},
		{name: "no base", over: stub.Set{"c": c}, want: stub.Set{"c": c}},
		{name: "per function", base: base, over: stub.Set{"b": b2, "c": c}, want: stub.Set{"a": a, "b": b2, "c": c}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.base.Merge(tt.over); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Merge = %v, want %v", got, tt.want)
			}
		})
	}
	if base["b"] != b || len(base) != 2 {
		t.Error("Merge changed the base set")
	}
}

// TestUnmarshalYAML decodes a set as a field, as a test file does.
func TestUnmarshalYAML(t *testing.T) {
	var ok struct {
		Stubs stub.Set `yaml:"stubs"`
	}
	if err := yaml.Unmarshal([]byte("stubs:\n  owner: {returns: ada}\n"), &ok); err != nil {
		t.Fatal(err)
	}
	if ok.Stubs["owner"] == nil || ok.Stubs["owner"].Returns.Raw != "ada" {
		t.Errorf("Stubs = %s", dump(ok.Stubs))
	}
	var bad struct {
		Stubs stub.Set `yaml:"stubs"`
	}
	err := yaml.Unmarshal([]byte("stubs:\n  owner: {}\n  lookup: {returns: a, error: b}\n"), &bad)
	errs, isErrs := err.(stub.Errors)
	if !isErrs || len(errs) != 2 {
		t.Fatalf("Unmarshal error = %#v, want two stub.Errors", err)
	}
	if want := "line 2: stub owner gives no result; line 3: stub lookup gives both returns and error"; err.Error() != want {
		t.Errorf("Error() = %q, want %q", err.Error(), want)
	}
}

func TestError(t *testing.T) {
	tests := []struct {
		name    string
		err     *stub.Error
		text    string
		display string
		advice  []string
	}{
		{name: "line and help", err: &stub.Error{Line: 3, Msg: "bad", Help: "fix it"}, text: "line 3: bad", display: "line 3: bad (fix it)", advice: []string{"fix it"}},
		{name: "neither", err: &stub.Error{Msg: "bad"}, text: "bad", display: "bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.text {
				t.Errorf("Error() = %q, want %q", got, tt.text)
			}
			if got := tt.err.Display(); got != tt.display {
				t.Errorf("Display() = %q, want %q", got, tt.display)
			}
			if got := tt.err.Advice(); !reflect.DeepEqual(got, tt.advice) {
				t.Errorf("Advice() = %q, want %q", got, tt.advice)
			}
			if tt.err.Cause() != nil {
				t.Error("Cause() != nil")
			}
		})
	}
}

// checkParse compares a parse result with the set or the problems expected.
func checkParse(t *testing.T, got stub.Set, errs []*stub.Error, want stub.Set, wantErrs []string, help string) {
	t.Helper()
	if wantErrs != nil {
		msgs := make([]string, len(errs))
		for i, e := range errs {
			msgs[i] = e.Error()
		}
		if !reflect.DeepEqual(msgs, wantErrs) {
			t.Fatalf("errors =\n  %s\nwant\n  %s", strings.Join(msgs, "\n  "), strings.Join(wantErrs, "\n  "))
		}
		if got != nil {
			t.Errorf("set = %s alongside errors", dump(got))
		}
		if help != "" && errs[0].Help != help {
			t.Errorf("help = %q, want %q", errs[0].Help, help)
		}
		for _, e := range errs {
			if e.Help == "" {
				t.Errorf("%s has no help", e)
			}
		}
		return
	}
	if errs != nil {
		t.Fatalf("errors: %v", stub.Errors(errs))
	}
	if !reflect.DeepEqual(strip(got), want) {
		t.Errorf("set =\n%s\nwant\n%s", dump(got), dump(want))
	}
}

// dump renders a set for a failure message.
func dump(s stub.Set) string {
	out, _ := yaml.Marshal(s)
	return string(out)
}

// strip clears every value's node, which the expected sets leave out, so
// a set compares by what it holds.
func strip(s stub.Set) stub.Set {
	for _, st := range s {
		drop := func(v *stub.Value) {
			if v != nil {
				v.Node = nil
			}
		}
		drop(st.Returns)
		for _, c := range st.Calls {
			drop(c.Returns)
			for _, a := range c.Args {
				drop(a)
			}
		}
	}
	return s
}
