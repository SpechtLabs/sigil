package testsuite_test

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/testsuite"
)

func TestRunCase(t *testing.T) {
	allowAdmin := testsuite.Got{Decision: "allow", Reason: "admin", Position: "p.sigil:3:3", Payload: map[string]any{"ttl": 8 * time.Hour, "scopes": []string{"*"}}}
	read := func(scope string) testsuite.Got {
		return testsuite.Got{Decision: "read", Reason: "member", Position: "r.sigil:1:1", Payload: map[string]any{"scope": scope}}
	}
	write := testsuite.Got{Decision: "write", Reason: "owner", Position: "r.sigil:2:1", Payload: map[string]any{}}

	tests := []struct {
		name   string
		kind   string
		expect testsuite.Expect
		got    testsuite.Outcome
		want   []string // the failures; none when the case passes
	}{
		{
			name:   "decision, reason and payload",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "admin", Payload: map[string]any{"ttl": "8h", "scopes": []any{"*"}}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{allowAdmin}},
		},
		{
			name:   "unlisted payload fields aren't checked",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "admin", Payload: map[string]any{"scopes": []any{"*"}}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{allowAdmin}},
		},
		{
			name:   "wrong reason",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "team_member"},
			got:    testsuite.Outcome{Entries: []testsuite.Got{allowAdmin}},
			want:   []string{"got allow(admin), want allow(team_member)"},
		},
		{
			name:   "wrong payload",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "admin", Payload: map[string]any{"ttl": "1h", "scopes": []any{"a", "b"}}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{allowAdmin}},
			want:   []string{"payload ttl = 8h, want 1h", `payload scopes = ["*"], want ["a", "b"]`},
		},
		{
			name:   "expected payload of the wrong type",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "admin", Payload: map[string]any{"ttl": 3}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{allowAdmin}},
			want:   []string{"expected payload ttl: expected a duration, found a number"},
		},
		{
			name:   "no decision",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "admin"},
			want:   []string{"got no decision, want allow(admin)"},
		},
		{
			name:   "an error instead of a decision",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "admin"},
			got:    testsuite.Outcome{Err: "a conflict (collect one: 2 candidates at the top rank)"},
			want:   []string{"got a conflict (collect one: 2 candidates at the top rank), want allow(admin)"},
		},
		{
			name:   "failing asserts instead of a decision",
			kind:   accessKind,
			expect: testsuite.Expect{Decision: "allow", Reason: "admin"},
			got:    testsuite.Outcome{Asserts: []string{"named"}},
			want:   []string{"got failing asserts named, want allow(admin)"},
		},
		{
			name:   "asserts in any order",
			kind:   accessKind,
			expect: testsuite.Expect{Asserts: []string{"b", "a"}},
			got:    testsuite.Outcome{Asserts: []string{"a", "b", "a"}},
		},
		{
			name:   "different asserts",
			kind:   accessKind,
			expect: testsuite.Expect{Asserts: []string{"b", "a"}},
			got:    testsuite.Outcome{Asserts: []string{"a"}},
			want:   []string{"got failing asserts a, want a, b"},
		},
		{
			name:   "a decision instead of asserts",
			kind:   accessKind,
			expect: testsuite.Expect{Asserts: []string{"named"}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{{Decision: "deny", Reason: "too_old"}}},
			want:   []string{"got deny(too_old), want failing asserts named"},
		},
		{
			name:   "an outcome instead of asserts",
			kind:   rolesKind,
			expect: testsuite.Expect{Asserts: []string{"named"}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{read("all"), write}},
			want:   []string{"got the outcome [read(member), write(owner)], want failing asserts named"},
		},
		{
			name:   "outcome in any order",
			kind:   rolesKind,
			expect: testsuite.Expect{Outcome: &[]testsuite.Entry{{Decision: "write", Reason: "owner"}, {Decision: "read", Reason: "member", Payload: map[string]any{"scope": "docs"}}}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{read("docs"), write}},
		},
		{
			name:   "outcome entries matched by payload",
			kind:   rolesKind,
			expect: testsuite.Expect{Outcome: &[]testsuite.Entry{{Decision: "read", Reason: "member", Payload: map[string]any{"scope": "docs"}}, {Decision: "read", Reason: "member", Payload: map[string]any{"scope": "all"}}}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{read("all"), read("docs")}},
		},
		{
			name:   "missing and unexpected entries",
			kind:   rolesKind,
			expect: testsuite.Expect{Outcome: &[]testsuite.Entry{{Decision: "read", Reason: "member", Payload: map[string]any{"scope": "docs"}}}},
			got:    testsuite.Outcome{Entries: []testsuite.Got{read("all"), write}},
			want: []string{
				"missing from the outcome: read(member) {scope: docs}",
				"not expected in the outcome: read(member) at r.sigil:1:1",
				"not expected in the outcome: write(owner) at r.sigil:2:1",
			},
		},
		{
			name:   "empty outcome",
			kind:   rolesKind,
			expect: testsuite.Expect{Outcome: &[]testsuite.Entry{}},
			got:    testsuite.Outcome{},
		},
		{
			name:   "an error instead of an outcome",
			kind:   rolesKind,
			expect: testsuite.Expect{Outcome: &[]testsuite.Entry{{Decision: "write", Reason: "owner"}}},
			got:    testsuite.Outcome{Err: "a runtime error (boom)"},
			want:   []string{"got a runtime error (boom), want the outcome [write(owner)]"},
		},
		{
			name:   "an error instead of an empty outcome",
			kind:   rolesKind,
			expect: testsuite.Expect{Outcome: &[]testsuite.Entry{}},
			got:    testsuite.Outcome{Err: "a runtime error (boom)"},
			want:   []string{"got a runtime error (boom), want the outcome []"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runner(t, tt.kind, nil)
			c := &testsuite.Case{Name: tt.name, Input: map[string]any{}, Expect: tt.expect}
			got := tt.got
			res := r.RunCase(t.Context(), &testsuite.Suite{File: "t_test.yaml"}, c, func(context.Context, reflect.Value) *testsuite.Outcome { return &got })
			if res.Err != nil {
				t.Fatalf("RunCase() error = %v", res.Err)
			}
			if !reflect.DeepEqual(res.Failures, tt.want) {
				t.Errorf("failures =\n%q\nwant\n%q", res.Failures, tt.want)
			}
			if res.Passed() != (len(tt.want) == 0) {
				t.Errorf("Passed() = %v", res.Passed())
			}
		})
	}
}

// TestRunCaseInput checks that the input reaches the evaluation decoded,
// and that one that can't be read or decoded stops the case.
func TestRunCaseInput(t *testing.T) {
	fsys := fstest.MapFS{"testdata/in.json": {Data: []byte(`{"user": {"name": "ada"}, "age": "2d"}`)}}
	tests := []struct {
		name string
		c    testsuite.Case
		err  string
		help string
	}{
		{name: "inline", c: testsuite.Case{Input: map[string]any{"user": map[string]any{"name": "ada"}, "age": "2d"}}},
		{name: "from a file", c: testsuite.Case{InputFile: "testdata/in.json"}},
		{name: "missing file", c: testsuite.Case{InputFile: "testdata/nope.json"}, err: "input_file testdata/nope.json couldn't be read"},
		{name: "unknown field", c: testsuite.Case{Input: map[string]any{"user": map[string]any{"nme": "ada"}}}, err: `input user.nme: unknown field "nme" on type User`, help: `did you mean "name"?`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := runner(t, accessKind, fsys)
			c := tt.c
			c.Name, c.Line = tt.name, 7
			c.Expect = testsuite.Expect{Decision: "deny", Reason: "too_old"}
			var seen reflect.Value
			res := r.RunCase(t.Context(), &testsuite.Suite{File: "t_test.yaml"}, &c, func(_ context.Context, in reflect.Value) *testsuite.Outcome {
				seen = in
				return &testsuite.Outcome{Entries: []testsuite.Got{{Decision: "deny", Reason: "too_old"}}}
			})
			if tt.err != "" {
				if res.Err == nil || !strings.Contains(res.Err.Error(), tt.err) || res.Passed() {
					t.Fatalf("RunCase() error = %v, want %q", res.Err, tt.err)
				}
				if !strings.HasPrefix(res.Err.Error(), `t_test.yaml:7: case "`+tt.name+`": `) {
					t.Errorf("error = %q, want the file, line and case", res.Err.Error())
				}
				if !strings.Contains(res.Err.Display(), tt.help) {
					t.Errorf("Display() = %q, want %q", res.Err.Display(), tt.help)
				}
				return
			}
			if !res.Passed() {
				t.Fatalf("RunCase() = %v, %v", res.Err, res.Failures)
			}
			age := seen.FieldByIndex(r.Binding.Fields[".age"]).Interface()
			if age != 48*time.Hour {
				t.Errorf("age = %v, want 48h", age)
			}
		})
	}
}

func TestErrorFormat(t *testing.T) {
	tests := []struct {
		err     testsuite.Error
		want    string
		display string
	}{
		{err: testsuite.Error{File: "a_test.yaml", Msg: "bad"}, want: "a_test.yaml: bad", display: "a_test.yaml: bad"},
		{err: testsuite.Error{File: "a_test.yaml", Line: 3, Case: "c", Msg: "bad", Help: "fix"}, want: `a_test.yaml:3: case "c": bad`, display: `a_test.yaml:3: case "c": bad (fix)`},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
		if got := tt.err.Display(); got != tt.display {
			t.Errorf("Display() = %q, want %q", got, tt.display)
		}
		if (len(tt.err.Advice()) == 0) != (tt.err.Help == "") || tt.err.Cause() != nil {
			t.Errorf("Advice() = %v, Cause() = %v", tt.err.Advice(), tt.err.Cause())
		}
	}
}

func runner(t *testing.T, src string, fsys fstest.MapFS) *testsuite.Runner {
	t.Helper()
	k := loadKind(t, src)
	return &testsuite.Runner{Kind: k, Binding: gokind.Synthesize(k), FS: fsys}
}
