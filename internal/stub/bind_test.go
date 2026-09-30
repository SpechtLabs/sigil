package stub_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/stub"
)

// deployKind declares host functions of struct, list, scalar and
// optional params.
const deployKind = `kind Deploy version 1

type User {
  name: string
  roles: list<string>
}

input user: User
input env: string

fn remove_requestor(User, list<string>) -> list<string>
fn lookup_owner(string) -> string
fn double(int) -> int
fn now() -> timestamp

decision deny {
  reason: no_rule_matched
}

decision allow {
  reason: ok
}

collect one
precedence deny > allow

default deny(reason: no_rule_matched)
`

type (
	User struct {
		Name  string   `policy:"name"`
		Roles []string `policy:"roles"`
	}
	Input struct {
		User User   `policy:"user"`
		Env  string `policy:"env"`
	}
	None struct{}
)

func TestBindErrors(t *testing.T) {
	k := loadKind(t, deployKind)
	tests := []struct {
		name string
		src  string
		errs []string // each problem's Error(), in order
		help string   // the first problem's help
	}{
		{
			name: "unknown function",
			src:  "lookup_ownr: {returns: ada}\n",
			errs: []string{"line 1: the kind has no host function lookup_ownr to stub"},
			help: `did you mean "lookup_owner"? the kind declares: remove_requestor, lookup_owner, double, now`,
		},
		{
			name: "unknown function nothing like the others",
			src:  "zzzzzzzzzz: {returns: ada}\n",
			errs: []string{"line 1: the kind has no host function zzzzzzzzzz to stub"},
			help: "the kind declares: remove_requestor, lookup_owner, double, now",
		},
		{
			name: "too few args",
			src:  "remove_requestor:\n  calls:\n    - {args: [{name: kevin}], returns: []}\n",
			errs: []string{"line 3: stub remove_requestor: call 1 passes 1 arg, but remove_requestor takes 2"},
			help: "the kind declares `fn remove_requestor(User, list<string>) -> list<string>`",
		},
		{
			name: "too many args",
			src:  "now:\n  calls:\n    - {args: [1, 2], returns: '2026-01-01T00:00:00Z'}\n",
			errs: []string{"line 3: stub now: call 1 passes 2 args, but now takes 0"},
		},
		{
			name: "an arg of the wrong type",
			src:  "lookup_owner:\n  calls:\n    - args:\n        - [a]\n      returns: ada\n",
			errs: []string{"line 4: stub lookup_owner: call 1: arg 1: expected a string, found a list"},
			help: "give the value the type the kind declares for it",
		},
		{
			name: "a struct arg with an unknown field",
			src:  "remove_requestor:\n  calls:\n    - {args: [{nme: kevin}, []], returns: []}\n",
			errs: []string{`line 3: stub remove_requestor: call 1: arg 1.nme: unknown field "nme" on type User`},
			help: `did you mean "name"? declared: name, roles`,
		},
		{
			name: "a result of the wrong type",
			src:  "double:\n  returns: two\n",
			errs: []string{"line 2: stub double: returns: expected an int, found a string"},
		},
		{
			name: "a call's result of the wrong type",
			src:  "double:\n  calls:\n    - {args: [1], returns: [2]}\n  returns: 0\n",
			errs: []string{"line 3: stub double: call 1: returns: expected an int, found a list"},
		},
		{
			name: "every problem",
			src:  "double: {returns: x}\nlookup_owner: {returns: [1]}\nnope: {returns: 1}\n",
			errs: []string{
				"line 1: stub double: returns: expected an int, found a string",
				"line 2: stub lookup_owner: returns: expected a string, found a list",
				"line 3: the kind has no host function nope to stub",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set := parse(t, tt.src)
			b := gokind.Synthesize(k)
			got, errs := set.Bind(k, b)
			if got != nil {
				t.Error("Bind returned a binding alongside errors")
			}
			msgs := make([]string, len(errs))
			for i, e := range errs {
				msgs[i] = e.Error()
				if e.Help == "" {
					t.Errorf("%s has no help", e)
				}
			}
			if !reflect.DeepEqual(msgs, tt.errs) {
				t.Fatalf("errors =\n  %s\nwant\n  %s", strings.Join(msgs, "\n  "), strings.Join(tt.errs, "\n  "))
			}
			if tt.help != "" && errs[0].Help != tt.help {
				t.Errorf("help = %q, want %q", errs[0].Help, tt.help)
			}
			if !reflect.DeepEqual(set.Validate(k), errs) {
				t.Errorf("Validate = %v, want Bind's errors", set.Validate(k))
			}
		})
	}
}

func TestBindNoFuncs(t *testing.T) {
	k := loadKind(t, "kind Bare version 1\n\ninput env: string\n\ndecision deny {\n  reason: no_rule_matched\n}\n\ncollect one\nprecedence deny\n\ndefault deny(reason: no_rule_matched)\n")
	errs := parse(t, "owner: {returns: ada}\n").Validate(k)
	if len(errs) != 1 || errs[0].Help != "the kind declares no host functions" {
		t.Fatalf("Validate = %v, want one error saying the kind declares none", errs)
	}
}

// TestBindUnboundFunction covers a binding that lacks a function its
// kind declares, which only a bug produces.
func TestBindUnboundFunction(t *testing.T) {
	k := loadKind(t, deployKind)
	b := gokind.Synthesize(k)
	delete(b.Funcs, "double")
	_, errs := parse(t, "double: {returns: 2}\n").Bind(k, b)
	if len(errs) != 1 || errs[0].Msg != "host function double isn't bound to Go" {
		t.Fatalf("Bind errors = %v", errs)
	}
}

func TestBindEmpty(t *testing.T) {
	k := loadKind(t, deployKind)
	b := gokind.Synthesize(k)
	for _, set := range []stub.Set{nil, {}} {
		got, errs := set.Bind(k, b)
		if got != b || errs != nil {
			t.Errorf("Bind(%v) = %p, %v; want the binding itself", set, got, errs)
		}
		if errs := set.Validate(k); errs != nil {
			t.Errorf("Validate(%v) = %v", set, errs)
		}
	}
}

// TestStubCalls calls the bound functions directly: each result form,
// first-match-wins, args compared as values, and an unmatched call.
func TestStubCalls(t *testing.T) {
	k := loadKind(t, deployKind)
	b := gokind.Synthesize(k)
	set := parse(t, `
remove_requestor:
  calls:
    - args: [{name: kevin, roles: [user]}, [cedric, alice, bob]]
      returns: [cedric, alice, bob]
    - args: [{name: kevin, roles: [user]}, [cedric, alice, bob]]
      returns: [shadowed]
    - args: [{name: bob}, [cedric, bob]]
      error: bob can't approve
  returns: []
lookup_owner:
  error: directory unavailable
double:
  calls:
    - {args: [2], returns: 4}
now:
  returns: 2026-09-30T12:00:00Z
`)
	sb, errs := set.Bind(k, b)
	if errs != nil {
		t.Fatal(stub.Errors(errs))
	}
	user := func(raw map[string]any) reflect.Value {
		v := reflect.New(b.Structs["User"]).Elem()
		if err := b.Decode(k.Type("User"), raw, v, "user"); err != nil {
			t.Fatal(err)
		}
		return v
	}
	tests := []struct {
		name string
		fn   string
		args []reflect.Value
		want any // the canonical result, when the call succeeds
		err  string
	}{
		{
			name: "first matching call",
			fn:   "remove_requestor",
			args: []reflect.Value{user(map[string]any{"name": "kevin", "roles": []any{"user"}}), reflect.ValueOf([]string{"cedric", "alice", "bob"})},
			want: []any{"cedric", "alice", "bob"},
		},
		{
			name: "a call that fails",
			fn:   "remove_requestor",
			args: []reflect.Value{user(map[string]any{"name": "bob"}), reflect.ValueOf([]string{"cedric", "bob"})},
			err:  "bob can't approve",
		},
		{
			name: "no call matches, so the fallback",
			fn:   "remove_requestor",
			args: []reflect.Value{user(map[string]any{"name": "ada"}), reflect.ValueOf([]string{"cedric"})},
			want: []any{},
		},
		{
			name: "error for every call",
			fn:   "lookup_owner",
			args: []reflect.Value{reflect.ValueOf("prod")},
			err:  "directory unavailable",
		},
		{
			name: "a matching int",
			fn:   "double",
			args: []reflect.Value{reflect.ValueOf(int64(2))},
			want: int64(4),
		},
		{
			name: "no call matches and no fallback",
			fn:   "double",
			args: []reflect.Value{reflect.ValueOf(int64(3))},
			err:  "no stubbed call matches double(3)",
		},
		{
			name: "returns for every call",
			fn:   "now",
			want: mustTime(t, "2026-09-30T12:00:00Z"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := sb.Funcs[tt.fn].Call(tt.args)
			if len(out) != 2 {
				t.Fatalf("results = %d, want a value and an error", len(out))
			}
			err, _ := reflect.TypeAssert[error](out[1])
			if tt.err != "" {
				if err == nil || err.Error() != tt.err {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := b.Canonical(k.Func(tt.fn).Result, out[0]); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("result = %#v, want %#v", got, tt.want)
			}
		})
	}
	var unmatched *stub.ErrUnmatched
	err, _ := reflect.TypeAssert[error](sb.Funcs["double"].Call([]reflect.Value{reflect.ValueOf(int64(9))})[1])
	if !errors.As(err, &unmatched) || unmatched.Name != "double" || unmatched.Args != "9" {
		t.Errorf("unmatched error = %#v, want an *ErrUnmatched for double(9)", err)
	}
	err, _ = reflect.TypeAssert[error](sb.Funcs["lookup_owner"].Call([]reflect.Value{reflect.ValueOf("x")})[1])
	if _, ok := errors.AsType[*stub.Failure](err); !ok {
		t.Errorf("error stub = %#v, want a *Failure", err)
	}
	err, _ = reflect.TypeAssert[error](b.Funcs["lookup_owner"].Call([]reflect.Value{reflect.ValueOf("x")})[1])
	if _, ok := errors.AsType[*gokind.ErrUnbound](err); !ok {
		t.Error("Bind changed the binding it was given")
	}
}

// TestStubEvaluates compiles policies with the bound binding, over a
// kind file's synthesized binding and a host's real functions.
func TestStubEvaluates(t *testing.T) {
	real := func(string) string { return "real" }
	host, hb, herrs := gokind.Build(gokind.Options{
		Name:    "Deploy",
		Version: 1,
		Input:   reflect.TypeFor[Input](),
		Decisions: []gokind.Decision{
			{Name: "deny", Payload: reflect.TypeFor[None](), Reasons: []string{"no_rule_matched"}},
			{Name: "allow", Payload: reflect.TypeFor[None](), Reasons: []string{"ok"}},
		},
		Default: &gokind.Default{Decision: "deny", Reason: "no_rule_matched"},
		Funcs: []gokind.Func{
			{Name: "lookup_owner", Fn: real},
			{Name: "remove_requestor", Fn: func(User, []string) []string { return nil }},
		},
	})
	if herrs != nil {
		t.Fatal(herrs)
	}
	file := loadKind(t, deployKind)
	tests := []struct {
		name  string
		kind  *kind.Kind
		b     *gokind.Binding
		stubs string
		rule  string
		input any
		want  string // the winning decision
		err   string // the runtime error, when it fails
		help  string // its help, when checked
	}{
		{
			name:  "kind file, stubbed",
			kind:  file,
			b:     gokind.Synthesize(file),
			stubs: "lookup_owner: {returns: ada}\n",
			rule:  `when lookup_owner(env) == "ada" { allow(reason: ok) }`,
			input: map[string]any{"env": "prod"},
			want:  "allow",
		},
		{
			name:  "kind file, struct args",
			kind:  file,
			b:     gokind.Synthesize(file),
			stubs: "remove_requestor:\n  calls:\n    - {args: [{name: kevin, roles: [user]}, [kevin, ada]], returns: [ada]}\n",
			rule:  `when "ada" in remove_requestor(user, ["kevin", "ada"]) { allow(reason: ok) }`,
			input: map[string]any{"user": map[string]any{"name": "kevin", "roles": []any{"user"}}},
			want:  "allow",
		},
		{
			name:  "kind file, unmatched",
			kind:  file,
			b:     gokind.Synthesize(file),
			stubs: "remove_requestor:\n  calls:\n    - {args: [{name: kevin}, []], returns: [ada]}\n",
			rule:  `when "ada" in remove_requestor(user, ["x"]) { allow(reason: ok) }`,
			input: map[string]any{"user": map[string]any{"name": "ada"}},
			err:   `host function remove_requestor failed: no stubbed call matches remove_requestor({"name": "ada", "roles": []}, ["x"])`,
			help:  "add these args under the stub's calls, or give the stub a returns or error for every other call",
		},
		{
			name:  "kind file, error",
			kind:  file,
			b:     gokind.Synthesize(file),
			stubs: "lookup_owner: {error: directory unavailable}\n",
			rule:  `when lookup_owner(env) == "ada" { allow(reason: ok) }`,
			input: map[string]any{},
			err:   "host function lookup_owner failed: directory unavailable",
			help:  "the stub's error: fails the call, as the host function failing would; a test case expects it with `expect: {error: ...}`",
		},
		{
			name:  "host function replaced",
			kind:  host,
			b:     hb,
			stubs: "lookup_owner: {returns: ada}\n",
			rule:  `when lookup_owner(env) == "ada" { allow(reason: ok) }`,
			input: map[string]any{"env": "prod"},
			want:  "allow",
		},
		{
			name:  "host function kept",
			kind:  host,
			b:     hb,
			rule:  `when lookup_owner(env) == "ada" { allow(reason: ok) }`,
			input: map[string]any{"env": "prod"},
			want:  "deny",
		},
		{
			name:  "host function without an error result fails",
			kind:  host,
			b:     hb,
			stubs: "lookup_owner: {error: down}\n",
			rule:  `when lookup_owner(env) == "ada" { allow(reason: ok) }`,
			input: map[string]any{},
			err:   "host function lookup_owner failed: down",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sb, errs := parse(t, tt.stubs).Bind(tt.kind, tt.b)
			if errs != nil {
				t.Fatal(stub.Errors(errs))
			}
			bu := bundle.New(tt.kind)
			bu.Add("p.sigil", []byte("policy p: Deploy@1\n\n"+tt.rule+"\n"))
			prog, cerrs := bu.Compile("p", bundle.Options{Binding: sb})
			if cerrs != nil {
				t.Fatalf("Compile: %v", cerrs)
			}
			in, derr := sb.DecodeInput(tt.kind, tt.input)
			if derr != nil {
				t.Fatal(derr)
			}
			out, rerr := prog.Eval(in.Interface())
			if tt.err != "" {
				if rerr == nil || !strings.HasPrefix(rerr.Msg, tt.err) {
					t.Fatalf("Eval error = %v, want %q", rerr, tt.err)
				}
				if tt.help != "" && rerr.Help != tt.help {
					t.Errorf("Eval help = %q, want %q", rerr.Help, tt.help)
				}
				return
			}
			if rerr != nil {
				t.Fatal(rerr)
			}
			got := prog.Default().Decision.Name
			if len(out.Top) > 0 {
				got = out.Top[0].Decision.Name
			}
			if got != tt.want {
				t.Errorf("decision = %s, want %s", got, tt.want)
			}
		})
	}
	if out := hb.Funcs["lookup_owner"].Call([]reflect.Value{reflect.ValueOf("x")}); out[0].String() != "real" {
		t.Error("stubbing changed the host's binding")
	}
}

func TestErrorTexts(t *testing.T) {
	tests := []struct {
		err        interface{ Help() string }
		text, help string
	}{
		{err: &stub.Failure{Msg: "down"}, text: "down", help: "the stub's error: fails the call, as the host function failing would; a test case expects it with `expect: {error: ...}`"},
		{err: &stub.ErrUnmatched{Name: "f", Args: `1, "a"`}, text: `no stubbed call matches f(1, "a")`, help: "add these args under the stub's calls, or give the stub a returns or error for every other call"},
	}
	for _, tt := range tests {
		if got := tt.err.(error).Error(); got != tt.text {
			t.Errorf("Error() = %q, want %q", got, tt.text)
		}
		if got := tt.err.Help(); got != tt.help {
			t.Errorf("Help() = %q, want %q", got, tt.help)
		}
	}
}

func parse(t *testing.T, src string) stub.Set {
	t.Helper()
	set, errs := stub.ParseDocument([]byte(src))
	if errs != nil {
		t.Fatal(stub.Errors(errs))
	}
	return set
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

func loadKind(t *testing.T, src string) *kind.Kind {
	t.Helper()
	k, errs := check.LoadKind("kind.sigil", []byte(src))
	if errs != nil {
		t.Fatalf("LoadKind: %v", errs)
	}
	return k
}

// typedKind has string params and results that YAML would read as other
// types when unquoted.
const typedKind = `kind Typed version 1

type Build {
  tag: string
  at: timestamp
  note: ?string
}

input env: string

fn by_time(string) -> string
fn tags() -> list<string>
fn labels() -> map<string, string>
fn build(Build) -> string
fn maybe(?string) -> string

decision allow {
  reason: ok
}

collect one
precedence allow

default allow(reason: ok)
`

// TestStubTyping checks that a stub's values are read against their
// types: an unquoted date or decimal where a string belongs is the text
// as written.
func TestStubTyping(t *testing.T) {
	k := loadKind(t, typedKind)
	b := gokind.Synthesize(k)
	set := parse(t, `
by_time:
  calls:
    - args: [2026-01-01]
      returns: 1.10
tags:
  returns: [2026-01-01, 1.10, yes]
labels:
  returns: {2026-01-01: 1.10}
build:
  calls:
    - args: [{tag: 1.10, at: 2026-01-01T00:00:00Z, note: 2026-01-01}]
      returns: found
maybe:
  calls:
    - args: [null]
      returns: none
`)
	sb, errs := set.Bind(k, b)
	if errs != nil {
		t.Fatal(stub.Errors(errs))
	}
	flag, ferr := stub.ParseFlag("by_time=2026-01-01")
	if ferr != nil {
		t.Fatal(ferr)
	}
	fb, errs := flag.Bind(k, b)
	if errs != nil {
		t.Fatal(stub.Errors(errs))
	}
	build := reflect.New(b.Structs["Build"]).Elem()
	if err := b.Decode(k.Type("Build"), map[string]any{"tag": "1.10", "at": "2026-01-01T00:00:00Z", "note": "2026-01-01"}, build, "build"); err != nil {
		t.Fatal(err)
	}
	var none *string
	tests := []struct {
		name string
		b    *gokind.Binding
		fn   string
		args []reflect.Value
		want any // the canonical result
	}{
		{name: "an arg and a result", b: sb, fn: "by_time", args: []reflect.Value{reflect.ValueOf("2026-01-01")}, want: "1.10"},
		{name: "a list", b: sb, fn: "tags", want: []any{"2026-01-01", "1.10", "yes"}},
		{name: "a map", b: sb, fn: "labels", want: map[any]any{"2026-01-01": "1.10"}},
		{name: "a struct", b: sb, fn: "build", args: []reflect.Value{build}, want: "found"},
		{name: "a null optional", b: sb, fn: "maybe", args: []reflect.Value{reflect.ValueOf(none)}, want: "none"},
		{name: "a flag", b: fb, fn: "by_time", args: []reflect.Value{reflect.ValueOf("x")}, want: "2026-01-01"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := tt.b.Funcs[tt.fn].Call(tt.args)
			if err, _ := reflect.TypeAssert[error](out[1]); err != nil {
				t.Fatal(err)
			}
			if got := b.Canonical(k.Func(tt.fn).Result, out[0]); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("result = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestStubNull checks that a null where the type can't be null gets
// advice about the stub, not about an input.
func TestStubNull(t *testing.T) {
	k := loadKind(t, typedKind)
	b := gokind.Synthesize(k)
	flag, ferr := stub.ParseFlag("by_time=")
	if ferr != nil {
		t.Fatal(ferr)
	}
	tests := []struct {
		name string
		set  stub.Set
		want string
	}{
		{name: "an empty flag", set: flag, want: "stub by_time: returns is null, but its type is string"},
		{name: "a null result", set: parse(t, "by_time: {returns: null}\n"), want: "line 1: stub by_time: returns is null, but its type is string"},
		{name: "a null arg", set: parse(t, "by_time:\n  calls:\n    - {args: [~], returns: x}\n"), want: "line 3: stub by_time: call 1: arg 1 is null, but its type is string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, errs := tt.set.Bind(k, b)
			if len(errs) != 1 || errs[0].Error() != tt.want {
				t.Fatalf("Bind errors = %v, want %q", errs, tt.want)
			}
			if want := "write a value of that type; only an optional, a list or a map may be null"; errs[0].Help != want {
				t.Errorf("help = %q, want %q", errs[0].Help, want)
			}
		})
	}
}

// Tier is a host's enum type, for TestStubEnums.
type Tier string

// enumKind is a kind file with a host function that takes an enum and
// one that returns one.
const enumKind = `kind Deploy version 1

enum Tier: critical | standard

input env: string

fn tier_of(string) -> Tier
fn owner_for(Tier) -> string

decision allow {
  reason: ok
}

collect one
precedence allow

default allow(reason: ok)
`

// TestStubEnums checks that a stub takes and returns enum values, by
// their names, over a kind file's binding and over a host's own enum
// type, and that a name the enum doesn't declare is an error.
func TestStubEnums(t *testing.T) {
	const stubs = `
tier_of:
  calls:
    - args: [ledger]
      returns: critical
  returns: standard
owner_for:
  calls:
    - args: [critical]
      returns: ada
  returns: nobody
`
	host, hb, herrs := gokind.Build(gokind.Options{
		Name:    "Deploy",
		Version: 1,
		Input: reflect.TypeFor[struct {
			Env string `policy:"env"`
		}](),
		Enums:     []gokind.Enum{{Type: reflect.TypeFor[Tier](), Values: []string{"critical", "standard"}}},
		Decisions: []gokind.Decision{{Name: "allow", Payload: reflect.TypeFor[None](), Reasons: []string{"ok"}}},
		Default:   &gokind.Default{Decision: "allow", Reason: "ok"},
		Funcs: []gokind.Func{
			{Name: "tier_of", Fn: func(string) Tier { return "standard" }},
			{Name: "owner_for", Fn: func(Tier) string { return "real" }},
		},
	})
	if herrs != nil {
		t.Fatal(herrs)
	}
	file := loadKind(t, enumKind)
	for _, tt := range []struct {
		name string
		kind *kind.Kind
		b    *gokind.Binding
	}{
		{name: "kind file", kind: file, b: gokind.Synthesize(file)},
		{name: "host enum", kind: host, b: hb},
	} {
		t.Run(tt.name, func(t *testing.T) {
			sb, errs := parse(t, stubs).Bind(tt.kind, tt.b)
			if errs != nil {
				t.Fatal(stub.Errors(errs))
			}
			for env, want := range map[string]string{"ledger": "ada", "billing": "nobody"} {
				tier := sb.Funcs["tier_of"].Call([]reflect.Value{reflect.ValueOf(env)})[0]
				out := sb.Funcs["owner_for"].Call([]reflect.Value{tier})
				if err, _ := reflect.TypeAssert[error](out[1]); err != nil {
					t.Fatalf("owner_for(tier_of(%q)): %v", env, err)
				}
				if got := out[0].String(); got != want {
					t.Errorf("owner_for(tier_of(%q)) = %q, want %q", env, got, want)
				}
			}
		})
	}
	_, errs := parse(t, "tier_of: {returns: crit}\n").Bind(file, gokind.Synthesize(file))
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), `"crit" is not a value of Tier`) {
		t.Errorf("Bind errors = %v, want crit rejected", errs)
	}
}
