package gokind_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// synthKind uses a struct before declaring it, nests structs, and covers
// every type constructor Synthesize maps.
const synthKind = `kind Synth version 1

enum Tier: critical | standard

type Outer {
  inner: Inner
  maybe: ?Inner
  tags: list<string>
  counts: map<int, float>
  flags: map<bool, duration>
  seen: ?timestamp
  tier: Tier
}

type Inner {
  name: string
  id: int
}

input outer: Outer
input env: string

fn lookup(string, int) -> list<string>

decision deny {
  reason: no_rule_matched
}

decision allow {
  reason: ok
  ttl: duration = 1h
  who: Inner
}

collect one
precedence deny > allow

default deny(reason: no_rule_matched)
`

func TestSynthesizeFieldPaths(t *testing.T) {
	b := gokind.Synthesize(loadKind(t, synthKind))

	tests := []struct {
		key  string
		want []int
	}{
		{key: ".outer", want: []int{0}},
		{key: ".env", want: []int{1}},
		{key: "Outer.inner", want: []int{0}},
		{key: "Outer.seen", want: []int{5}},
		{key: "Inner.id", want: []int{1}},
		{key: "decision allow.ttl", want: []int{0}},
		{key: "decision allow.who", want: []int{1}},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			if got := b.Fields[tt.key]; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Fields[%q] = %v, want %v", tt.key, got, tt.want)
			}
		})
	}
}

func TestSynthesizeTypes(t *testing.T) {
	b := gokind.Synthesize(loadKind(t, synthKind))
	outer, inner := b.Structs["Outer"], b.Structs["Inner"]
	if outer == nil || inner == nil {
		t.Fatalf("Structs = %v, want Outer and Inner", b.Structs)
	}

	tests := []struct {
		name string
		got  reflect.Type
		want reflect.Type
	}{
		{name: "input outer", got: b.Input.Field(0).Type, want: outer},
		{name: "input env", got: b.Input.Field(1).Type, want: reflect.TypeFor[string]()},
		{name: "struct before its declaration", got: outer.Field(0).Type, want: inner},
		{name: "optional struct", got: outer.Field(1).Type, want: reflect.PointerTo(inner)},
		{name: "list", got: outer.Field(2).Type, want: reflect.TypeFor[[]string]()},
		{name: "map with int keys", got: outer.Field(3).Type, want: reflect.TypeFor[map[int64]float64]()},
		{name: "map with bool keys", got: outer.Field(4).Type, want: reflect.TypeFor[map[bool]time.Duration]()},
		{name: "optional timestamp", got: outer.Field(5).Type, want: reflect.TypeFor[*time.Time]()},
		{name: "enum", got: outer.Field(6).Type, want: reflect.TypeFor[string]()},
		{name: "payload duration", got: b.Payloads["allow"].Field(0).Type, want: reflect.TypeFor[time.Duration]()},
		{name: "payload struct", got: b.Payloads["allow"].Field(1).Type, want: inner},
		{name: "empty payload", got: b.Payloads["deny"], want: reflect.TypeFor[struct{}]()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("type = %v, want %v", tt.got, tt.want)
			}
		})
	}

	if tag := outer.Field(0).Tag.Get("policy"); tag != "inner" {
		t.Errorf(`Outer.inner tag = %q, want "inner"`, tag)
	}
	if got := b.Funcs["lookup"].Type(); got != reflect.TypeFor[func(string, int64) ([]string, error)]() {
		t.Errorf("Funcs[lookup] type = %v", got)
	}
}

func TestSynthesizedFunctionFails(t *testing.T) {
	b := gokind.Synthesize(loadKind(t, synthKind))
	out := b.Funcs["lookup"].Call([]reflect.Value{reflect.ValueOf("x"), reflect.ValueOf(int64(1))})
	if !out[0].IsNil() {
		t.Errorf("result = %v, want the zero list", out[0])
	}
	var unbound *gokind.ErrUnbound
	if err, _ := reflect.TypeAssert[error](out[1]); !errors.As(err, &unbound) || unbound.Name != "lookup" {
		t.Fatalf("error = %v, want *ErrUnbound for lookup", out[1].Interface())
	}
	want := "this sigil binary has only lookup's signature from the kind file; stub it with `stubs:` in the test file or `--stub lookup=VALUE` on sigil eval, or evaluate with a host binary built with sigil's pkg/cli, which links the real function in"
	if got := unbound.Help(); got != want {
		t.Errorf("Help() = %q, want %q", got, want)
	}
}

// TestSynthesizedEvaluation compiles and evaluates a policy over the
// synthesized binding: rules that don't call a function evaluate, and one
// that calls an unbound function fails with a runtime error naming it.
func TestSynthesizedEvaluation(t *testing.T) {
	k := loadKind(t, synthKind)
	b := gokind.Synthesize(k)

	tests := []struct {
		name   string
		policy string
		input  any
		want   string // the winning decision
		err    string
	}{
		{
			name:   "no function call",
			policy: `when outer.inner.name == "ada" and outer.counts[2] > 1.5 { allow(reason: ok, who: outer.inner) }`,
			input:  map[string]any{"outer": map[string]any{"inner": map[string]any{"name": "ada"}, "counts": map[string]any{"2": 2.0}}},
			want:   "allow",
		},
		{
			name:   "optional absent",
			policy: `when present outer.maybe { allow(reason: ok, who: outer.inner) }`,
			input:  map[string]any{},
			want:   "deny",
		},
		{
			name:   "enum",
			policy: `when outer.tier == critical { allow(reason: ok, who: outer.inner) }`,
			input:  map[string]any{"outer": map[string]any{"tier": "critical"}},
			want:   "allow",
		},
		{
			name:   "unbound function",
			policy: `when "x" in lookup(env, 1) { allow(reason: ok, who: outer.inner) }`,
			input:  map[string]any{"env": "prod"},
			err:    "host function lookup failed: no implementation in this sigil binary",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bu := bundle.New(k)
			bu.Add("p.sigil", []byte("policy p: Synth@1\n\n"+tt.policy+"\n"))
			prog, errs := bu.Compile("p", bundle.Options{Binding: b})
			if errs != nil {
				t.Fatalf("Compile: %v", errs)
			}
			in, derr := b.DecodeInput(k, tt.input)
			if derr != nil {
				t.Fatalf("DecodeInput: %v", derr)
			}
			out, rerr := prog.Eval(in.Interface())
			if tt.err != "" {
				if rerr == nil || !strings.Contains(rerr.Msg, tt.err) {
					t.Fatalf("Eval error = %v, want %q", rerr, tt.err)
				}
				return
			}
			if rerr != nil {
				t.Fatalf("Eval: %v", rerr)
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
}

func loadKind(t *testing.T, src string) *kind.Kind {
	t.Helper()
	k, errs := check.LoadKind("kind.sigil", []byte(src))
	if errs != nil {
		t.Fatalf("LoadKind: %v", errs)
	}
	return k
}

// hostError is a host's own error with a Help method, which isn't a
// stand-in's advice.
type hostError struct{}

func (hostError) Error() string { return "no quota" }

func (hostError) Help() string { return "raise the quota" }

func TestStandInHelp(t *testing.T) {
	unbound := &gokind.ErrUnbound{Name: "owner"}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "an unbound function", err: unbound, want: unbound.Help()},
		{name: "wrapped", err: fmt.Errorf("calling owner: %w", unbound), want: unbound.Help()},
		{name: "a plain error", err: errors.New("boom")},
		{name: "a host's error with a Help method", err: hostError{}},
		{name: "nil"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gokind.StandInHelp(tt.err); got != tt.want {
				t.Errorf("StandInHelp() = %q, want %q", got, tt.want)
			}
		})
	}
}
