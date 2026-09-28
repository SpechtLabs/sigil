package gokind_test

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/types"
)

const decodeKind = `kind D version 1

type S {
  n: int
  f: float
  s: string
  b: bool
  d: duration
  t: timestamp
  o: ?int
  l: list<int>
  m: map<string, int>
  mi: map<int, string>
  mb: map<bool, int>
  md: map<duration, int>
  inner: ?Inner
}

type Inner {
  x: int
}

input s: S
input env: string

decision deny {
  r
}

collect one
precedence deny

default deny(r)
`

func TestDecodeInput(t *testing.T) {
	tests := []struct {
		name   string
		raw    any    // JSON text when a string, a decoded value otherwise
		field  string // the field to compare, as "S.name"; empty for none
		want   any
		err    string
		advice string
	}{
		{name: "int from JSON", raw: `{"s": {"n": 7}}`, field: "S.n", want: int64(7)},
		{name: "int from float64", raw: map[string]any{"s": map[string]any{"n": 3.0}}, field: "S.n", want: int64(3)},
		{name: "int from YAML int", raw: map[string]any{"s": map[string]any{"n": 5}}, field: "S.n", want: int64(5)},
		{name: "int from uint64", raw: map[string]any{"s": map[string]any{"n": uint64(9)}}, field: "S.n", want: int64(9)},
		{name: "int too large", raw: map[string]any{"s": map[string]any{"n": uint64(1 << 63)}}, err: "s.n: expected an int, found a number"},
		{name: "int with a fraction", raw: `{"s": {"n": 3.5}}`, err: "s.n: expected an int, found a number"},
		{name: "int from a string", raw: `{"s": {"n": "7"}}`, err: "s.n: expected an int, found a string"},
		{name: "float from int", raw: `{"s": {"f": 2}}`, field: "S.f", want: 2.0},
		{name: "string", raw: `{"s": {"s": "x"}}`, field: "S.s", want: "x"},
		{name: "bool", raw: `{"s": {"b": true}}`, field: "S.b", want: true},
		{name: "bool from a number", raw: `{"s": {"b": 1}}`, err: "s.b: expected a bool, found a number"},
		{name: "duration", raw: `{"s": {"d": "1h30m"}}`, field: "S.d", want: 90 * time.Minute},
		{name: "duration in days", raw: `{"s": {"d": "2d"}}`, field: "S.d", want: 48 * time.Hour},
		{name: "invalid duration", raw: `{"s": {"d": "90x"}}`, err: `s.d: invalid duration "90x"`, advice: "units are d, h, m, s and ms"},
		{name: "empty duration", raw: `{"s": {"d": ""}}`, err: `s.d: invalid duration ""`},
		{name: "duration as a number", raw: `{"s": {"d": 3}}`, err: "s.d: expected a duration, found a number", advice: `"45m"`},
		{name: "timestamp", raw: `{"s": {"t": "2026-09-28T14:00:00+02:00"}}`, field: "S.t", want: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)},
		{name: "timestamp from YAML", raw: map[string]any{"s": map[string]any{"t": time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}}, field: "S.t", want: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)},
		{name: "invalid timestamp", raw: `{"s": {"t": "yesterday"}}`, err: "s.t: expected a timestamp, found a string", advice: "RFC 3339"},
		{name: "optional present", raw: `{"s": {"o": 4}}`, field: "S.o", want: new(int64(4))},
		{name: "optional null", raw: `{"s": {"o": null}}`, field: "S.o", want: (*int64)(nil)},
		{name: "optional struct", raw: `{"s": {"inner": {"x": 1}}}`, field: "S.inner", want: nil},
		{name: "list", raw: `{"s": {"l": [1, 2]}}`, field: "S.l", want: []int64{1, 2}},
		{name: "list null", raw: `{"s": {"l": null}}`, field: "S.l", want: []int64(nil)},
		{name: "list element path", raw: `{"s": {"l": [1, "two"]}}`, err: "s.l[1]: expected an int, found a string"},
		{name: "list from an object", raw: `{"s": {"l": {}}}`, err: "s.l: expected a list<int>, found an object"},
		{name: "map", raw: `{"s": {"m": {"a": 1}}}`, field: "S.m", want: map[string]int64{"a": 1}},
		{name: "map null", raw: `{"s": {"m": null}}`, field: "S.m", want: map[string]int64(nil)},
		{name: "map with int keys", raw: `{"s": {"mi": {"3": "c"}}}`, field: "S.mi", want: map[int64]string{3: "c"}},
		{name: "map with a bad int key", raw: `{"s": {"mi": {"x": "c"}}}`, err: `s.mi["x"]: expected an int, found a number`},
		{name: "map with bool keys", raw: `{"s": {"mb": {"true": 1}}}`, field: "S.mb", want: map[bool]int64{true: 1}},
		{name: "map with duration keys", raw: `{"s": {"md": {"1h": 2}}}`, field: "S.md", want: map[time.Duration]int64{time.Hour: 2}},
		{name: "missing field is zero", raw: `{"s": {}}`, field: "S.n", want: int64(0)},
		{name: "missing input is zero", raw: `{}`, field: "S.s", want: ""},
		{name: "null scalar", raw: `{"s": {"n": null}}`, err: "s.n: null for an int", advice: "only optionals, lists and maps may be null"},
		{name: "null struct", raw: `{"s": null}`, err: "s: null for a S"},
		{name: "unknown input", raw: `{"ss": {}}`, err: `ss: unknown input "ss"`, advice: `did you mean "s"? declared: s, env`},
		{name: "unknown field", raw: `{"s": {"nn": 1}}`, err: `s.nn: unknown field "nn" on type S`, advice: `did you mean "n"?`},
		{name: "unknown field without a near name", raw: `{"s": {"zzzzz": 1}}`, err: `unknown field "zzzzz" on type S`, advice: "declared: n, f, s, b, d"},
		{name: "not an object", raw: `[1]`, err: "the input must be an object with one key per input, found a list", advice: "the kind declares: s, env"},
		{name: "YAML map with any keys", raw: map[any]any{"s": map[any]any{"n": 2}}, field: "S.n", want: int64(2)},
	}
	k := loadKind(t, decodeKind)
	b := gokind.Synthesize(k)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, err := b.DecodeInput(k, decode(t, tt.raw))
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("DecodeInput() error = %v, want %q", err, tt.err)
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.advice) {
					t.Errorf("advice = %q, want %q", advice, tt.advice)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeInput() error = %v", err)
			}
			if tt.field == "" {
				return
			}
			got := v.Field(0).FieldByIndex(b.Fields[tt.field]).Interface()
			if tt.want == nil {
				if reflect.ValueOf(got).IsNil() {
					t.Errorf("%s = nil, want a value", tt.field)
				}
				return
			}
			if ts, ok := got.(time.Time); ok {
				if !ts.Equal(tt.want.(time.Time)) {
					t.Errorf("%s = %v, want %v", tt.field, ts, tt.want)
				}
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("%s = %#v, want %#v", tt.field, got, tt.want)
			}
		})
	}
}

// TestDecodeHostTypes decodes into a host's own Go types, which aren't
// the canonical ones Synthesize picks.
func TestDecodeHostTypes(t *testing.T) {
	// S is the host's Go type for decodeKind's S, with Go types other
	// than the canonical ones.
	type (
		S struct {
			T time.Time `policy:"t"`
			L []int     `policy:"l"`
			N int       `policy:"n"`
		}
		HostInput struct {
			S S `policy:"s"`
		}
	)
	k := loadKind(t, decodeKind)
	o := gokind.Options{
		Name: "H", Version: 1, Input: reflect.TypeFor[HostInput](),
		Decisions: []gokind.Decision{{Name: "deny", Payload: reflect.TypeFor[None](), Reasons: []string{"r"}}},
		Default:   &gokind.Default{Decision: "deny", Reason: "r"},
	}
	hk, hb, errs := gokind.Build(o)
	if errs != nil {
		t.Fatalf("Build: %v", errs)
	}
	raw := decode(t, `{"s": {"n": 3, "l": [1, 2], "t": "2026-09-28T14:00:00+02:00"}}`)
	v, err := hb.DecodeInput(hk, raw)
	if err != nil {
		t.Fatalf("DecodeInput: %v", err)
	}
	host := v.Interface().(HostInput)
	want := S{N: 3, L: []int{1, 2}, T: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	if host.S.N != want.N || !reflect.DeepEqual(host.S.L, want.L) || !host.S.T.Equal(want.T) {
		t.Errorf("decoded %+v, want %+v", host.S, want)
	}

	// The same document through the synthesized binding has the same
	// canonical form, field by field.
	sb := gokind.Synthesize(k)
	sv, err := sb.DecodeInput(k, raw)
	if err != nil {
		t.Fatalf("DecodeInput (synthesized): %v", err)
	}
	for _, name := range []string{"n", "l", "t"} {
		f := k.Type("S").Field(name)
		got := hb.Canonical(f.Type, v.Field(0).FieldByIndex(hb.Fields["S."+name]))
		wantC := sb.Canonical(f.Type, sv.Field(0).FieldByIndex(sb.Fields["S."+name]))
		if !reflect.DeepEqual(got, wantC) {
			t.Errorf("Canonical(%s) = %#v, synthesized %#v", name, got, wantC)
		}
	}
}

func TestCanonical(t *testing.T) {
	k := loadKind(t, decodeKind)
	b := gokind.Synthesize(k)
	berlin := time.FixedZone("CEST", 2*60*60)
	noon := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		t    types.Type
		a, b any
	}{
		{name: "int from Go int and int64", t: types.Int, a: 3, b: int64(3)},
		{name: "list from []string and []any", t: &types.List{Elem: types.String}, a: []string{"a", "b"}, b: []any{"a", "b"}},
		{name: "map of Go ints and literal map", t: &types.Map{Key: types.String, Value: types.Int}, a: map[string]int{"a": 1}, b: map[any]any{"a": int64(1)}},
		{name: "timestamps in two zones", t: types.Timestamp, a: noon, b: noon.In(berlin)},
		{name: "optional pointer and value", t: &types.Optional{Elem: types.Int}, a: new(int64(4)), b: int64(4)},
		{name: "absent optional", t: &types.Optional{Elem: types.Int}, a: (*int64)(nil), b: nil},
		{name: "duration", t: types.Duration, a: time.Hour, b: 60 * time.Minute},
		{name: "struct by field name", t: k.Type("Inner"), a: reflect.New(b.Structs["Inner"]).Elem().Interface(), b: reflect.New(b.Structs["Inner"]).Elem().Interface()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca, cb := b.Canonical(tt.t, reflect.ValueOf(tt.a)), b.Canonical(tt.t, reflect.ValueOf(tt.b))
			if !reflect.DeepEqual(ca, cb) {
				t.Errorf("Canonical = %#v and %#v, want equal", ca, cb)
			}
		})
	}

	if got := b.Canonical(types.Int, reflect.ValueOf(uint(7))); got != int64(7) {
		t.Errorf("Canonical(uint) = %#v, want int64(7)", got)
	}
	inner := b.Canonical(k.Type("Inner"), reflect.New(b.Structs["Inner"]).Elem())
	if !reflect.DeepEqual(inner, map[any]any{"x": int64(0)}) {
		t.Errorf("Canonical(Inner) = %#v", inner)
	}
}

// decode returns raw, parsing it as JSON with json.Number first when
// it's a string.
func decode(t *testing.T, raw any) any {
	t.Helper()
	s, ok := raw.(string)
	if !ok {
		return raw
	}
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var out any
	if err := dec.Decode(&out); err != nil {
		t.Fatalf("bad test JSON %s: %v", s, err)
	}
	return out
}
