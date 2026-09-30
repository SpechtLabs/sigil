package testsuite_test

import (
	"encoding/json"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/testsuite"
)

// accessKind collects one decision; rolesKind collects all.
const (
	accessKind = `kind Access version 1

enum Level: low | high

type User {
  name: string
  admin: bool
}

input user: User
input age: duration

decision deny {
  reason: too_old | no_rule_matched
}

decision allow {
  reason: admin | team_member
  ttl: duration = 1h
  scopes: list<string> = []
  level: Level = low
}

collect one
precedence deny > allow

default deny(reason: no_rule_matched)
`
	rolesKind = `kind Roles version 1

input user: string

decision read {
  reason: member
  scope: string = "all"
}

decision write {
  reason: owner
}

collect all
`
)

func TestIsTestFile(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "main_test.yaml", want: true},
		{name: "deploy/main_test.yml", want: true},
		{name: "main.yaml"},
		{name: "main_test.json"},
		{name: "main.sigil"},
	}
	for _, tt := range tests {
		if got := testsuite.IsTestFile(tt.name); got != tt.want {
			t.Errorf("IsTestFile(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		err    string
		policy string
		lines  []int
	}{
		{
			name: "cases and their lines",
			src: `policy: access.main
cases:
  - name: first
    input: {}
    expect: {decision: deny, reason: no_rule_matched}

  - name: second
    input_file: in.json
    expect:
      asserts: [named]
`,
			policy: "access.main",
			lines:  []int{3, 7},
		},
		{name: "unknown key", src: "policy: p\ncases:\n  - name: a\n    expects: {}\n", err: `line 4: unknown key "expects" in a case`},
		{name: "unknown top-level key", src: "policy: p\ntests: []\n", err: `line 2: unknown key "tests" in the file`},
		{name: "no policy", src: "cases: []\n", err: "the test file names no policy"},
		{name: "empty file", src: "", err: "not a valid test file"},
		{name: "not YAML", src: "policy: [\n", err: "not a valid test file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := testsuite.Parse("main_test.yaml", []byte(tt.src))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Parse() error = %v, want %q", err, tt.err)
				}
				if len(err.Advice()) == 0 {
					t.Error("Parse() error has no advice")
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if s.Policy != tt.policy || s.File != "main_test.yaml" {
				t.Errorf("Parse() = policy %q, file %q", s.Policy, s.File)
			}
			for i, c := range s.Cases {
				if c.Line != tt.lines[i] {
					t.Errorf("case %d line = %d, want %d", i, c.Line, tt.lines[i])
				}
			}
		})
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		kind  string
		cases string
		errs  []string // substrings of the errors, in order; none when valid
	}{
		{name: "valid decision", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {decision: allow, reason: admin, payload: {ttl: 8h}}`},
		{name: "valid outcome", kind: rolesKind, cases: `
  - name: a
    input: {}
    expect:
      outcome: [{decision: read, reason: member}]`},
		{name: "valid empty outcome", kind: rolesKind, cases: `
  - name: a
    input: {}
    expect: {outcome: []}`},
		{name: "valid asserts", kind: accessKind, cases: `
  - name: a
    input_file: in.json
    expect: {asserts: [named]}`},
		{name: "no name", kind: accessKind, cases: `
  - input: {}
    expect: {decision: deny, reason: too_old}`, errs: []string{"case has no name"}},
		{name: "duplicate name", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {decision: deny, reason: too_old}
  - name: a
    input: {}
    expect: {decision: deny, reason: too_old}`, errs: []string{`case "a" is defined twice`}},
		{name: "input and input_file", kind: accessKind, cases: `
  - name: a
    input: {}
    input_file: in.json
    expect: {decision: deny, reason: too_old}`, errs: []string{"exactly one of input and input_file"}},
		{name: "no input", kind: accessKind, cases: `
  - name: a
    expect: {decision: deny, reason: too_old}`, errs: []string{"exactly one of input and input_file"}},
		{name: "no expectation", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {}`, errs: []string{"exactly one of decision, outcome and asserts"}},
		{name: "several forms", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {decision: deny, reason: too_old, asserts: [x]}`, errs: []string{"exactly one of decision, outcome and asserts"}},
		{name: "outcome on collect one", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {outcome: [{decision: deny, reason: too_old}]}`, errs: []string{"outcome is for collect all kinds, and Access collects one"}},
		{name: "decision on collect all", kind: rolesKind, cases: `
  - name: a
    input: {}
    expect: {decision: read, reason: member}`, errs: []string{"Roles collects all, so expect an outcome"}},
		{name: "unknown decision", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {decision: alow, reason: admin}`, errs: []string{`the kind has no decision "alow"`}},
		{name: "no reason", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {decision: allow}`, errs: []string{"expected allow has no reason"}},
		{name: "unknown reason", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {decision: allow, reason: admn}`, errs: []string{`decision allow has no reason "admn"`}},
		{name: "unknown payload field", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {decision: allow, reason: admin, payload: {tll: 1h}}`, errs: []string{`decision allow has no payload field "tll"`}},
		{name: "unknown outcome entry", kind: rolesKind, cases: `
  - name: a
    input: {}
    expect: {outcome: [{decision: raed, reason: member}, {reason: owner}]}`, errs: []string{`the kind has no decision "raed"`, "expected decision has no name"}},
		{name: "empty asserts", kind: accessKind, cases: `
  - name: a
    input: {}
    expect: {asserts: []}`, errs: []string{"asserts is empty"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, perr := testsuite.Parse("main_test.yaml", []byte("policy: p\ncases:"+tt.cases+"\n"))
			if perr != nil {
				t.Fatalf("Parse: %v", perr)
			}
			errs := s.Validate(loadKind(t, tt.kind))
			if len(errs) != len(tt.errs) {
				t.Fatalf("Validate() = %v, want %d errors %q", errs, len(tt.errs), tt.errs)
			}
			for i, e := range errs {
				if !strings.Contains(e.Error(), tt.errs[i]) {
					t.Errorf("error %d = %q, want %q", i, e.Error(), tt.errs[i])
				}
				if !strings.HasPrefix(e.Error(), "main_test.yaml:") {
					t.Errorf("error %d = %q, want the file and line", i, e.Error())
				}
			}
		})
	}
}

// TestValidateHints checks that misspelled names get a did-you-mean.
func TestValidateHints(t *testing.T) {
	src := `policy: p
cases:
  - name: a
    input: {}
    expect: {decision: allow, reason: admn, payload: {}}
  - name: b
    input: {}
    expect: {decision: allow, reason: admin, payload: {tll: 1h}}
  - name: c
    input: {}
    expect: {decision: alow, reason: admin}
`
	s, perr := testsuite.Parse("t_test.yaml", []byte(src))
	if perr != nil {
		t.Fatal(perr)
	}
	errs := s.Validate(loadKind(t, accessKind))
	want := []string{`did you mean "admin"?`, `did you mean "ttl"?`, `did you mean "allow"?`}
	if len(errs) != len(want) {
		t.Fatalf("Validate() = %v", errs)
	}
	for i, e := range errs {
		if !strings.Contains(e.Display(), want[i]) {
			t.Errorf("error %d = %q, want %q", i, e.Display(), want[i])
		}
	}
}

func TestReadInput(t *testing.T) {
	fsys := fstest.MapFS{
		"deploy/testdata/in.json":  {Data: []byte(`{"n": 12345678901234567890, "x": 1}`)},
		"deploy/testdata/in.yaml":  {Data: []byte("n: 3\nuser: {name: ada}\n")},
		"deploy/testdata/bad.json": {Data: []byte(`{`)},
	}
	s := &testsuite.Suite{File: "deploy/main_test.yaml"}

	t.Run("inline", func(t *testing.T) {
		raw, err := s.ReadInput(fsys, &testsuite.Case{Input: map[string]any{"a": 1}})
		if err != nil || raw.(map[string]any)["a"] != 1 {
			t.Errorf("ReadInput() = %v, %v", raw, err)
		}
	})
	t.Run("json keeps numbers exact", func(t *testing.T) {
		raw, err := s.ReadInput(fsys, &testsuite.Case{InputFile: "testdata/in.json"})
		if err != nil {
			t.Fatal(err)
		}
		if n := raw.(map[string]any)["n"]; n != json.Number("12345678901234567890") {
			t.Errorf("n = %#v, want a json.Number", n)
		}
	})
	t.Run("yaml by extension", func(t *testing.T) {
		raw, err := s.ReadInput(fsys, &testsuite.Case{InputFile: "testdata/in.yaml"})
		if err != nil {
			t.Fatal(err)
		}
		if n := raw.(map[string]any)["n"]; n != 3 {
			t.Errorf("n = %#v, want 3", n)
		}
	})
	t.Run("missing file", func(t *testing.T) {
		_, err := s.ReadInput(fsys, &testsuite.Case{Name: "c", Line: 4, InputFile: "nope.json"})
		if err == nil || err.Error() != `deploy/main_test.yaml:4: case "c": input_file nope.json couldn't be read` {
			t.Errorf("ReadInput() error = %v", err)
		}
	})
	t.Run("invalid file", func(t *testing.T) {
		_, err := s.ReadInput(fsys, &testsuite.Case{InputFile: "testdata/bad.json"})
		if err == nil || !strings.Contains(err.Error(), "input_file testdata/bad.json isn't valid") {
			t.Errorf("ReadInput() error = %v", err)
		}
	})
}

func loadKind(t *testing.T, src string) *kind.Kind {
	t.Helper()
	k, errs := check.LoadKind("kind.sigil", []byte(src))
	if errs != nil {
		t.Fatalf("LoadKind: %v", errs)
	}
	return k
}
