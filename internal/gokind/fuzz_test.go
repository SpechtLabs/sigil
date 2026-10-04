package gokind_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/gokind"
)

func FuzzSynthesize(f *testing.F) {
	for _, src := range []string{synthKind, decodeKind, "kind K version 1\ndecision d { reason: r }\ncollect all"} {
		f.Add(src)
	}
	f.Fuzz(func(t *testing.T, src string) {
		k, errs := check.LoadKind("fuzz.sigil", []byte(src))
		if errs != nil {
			return
		}
		b := gokind.Synthesize(k)
		v, err := b.DecodeInput(k, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range k.Inputs {
			field := v.FieldByIndex(b.Fields["."+in.Name])
			if _, ok := b.TypeOf(field.Type()); !ok {
				t.Fatalf("no binding for input %s", in.Name)
			}
			_ = b.Canonical(in.Type, field)
		}
	})
}

func FuzzDecodeInput(f *testing.F) {
	k, errs := check.LoadKind("kind.sigil", []byte(decodeKind))
	if errs != nil {
		f.Fatal(errs)
	}
	b := gokind.Synthesize(k)
	for _, src := range []string{`{}`, `null`, `{"s":{"n":9223372036854775807,"o":null,"l":[1,2],"m":{"x":3}}}`, `{"s":{"d":"1h30m","t":"2026-09-28T12:00:00Z","inner":{"x":2}}}`, "s:\n  n: 2\n  mb: {true: 1}\n", "s: {f: .nan}", "&a [*a]", `{"s":{"tier":"critical","tiers":["internal"],"mt":{"standard":1},"ot":null}}`, `{"s":{"tier":"critcal","mt":{"gold":1}}}`} {
		f.Add([]byte(src), true)
		f.Add([]byte(src), false)
	}
	f.Fuzz(func(t *testing.T, src []byte, asJSON bool) {
		var raw any
		if asJSON {
			d := json.NewDecoder(bytes.NewReader(src))
			d.UseNumber()
			if err := d.Decode(&raw); err != nil {
				return
			}
		} else if err := yaml.Unmarshal(src, &raw); err != nil {
			return
		}
		// fmt sorts map keys by type and value. JSON can't: YAML may yield the
		// keys 1 and "1" in one map, and both encode as "1" in random order.
		before := fmt.Sprintf("%#v", raw)
		first, err := b.DecodeInput(k, raw)
		second, again := b.DecodeInput(k, raw)
		if (err == nil) != (again == nil) || err != nil && err.Error() != again.Error() {
			t.Fatal("decode errors are not deterministic")
		}
		if err == nil {
			for _, in := range k.Inputs {
				path := b.Fields["."+in.Name]
				x, y := b.Canonical(in.Type, first.FieldByIndex(path)), b.Canonical(in.Type, second.FieldByIndex(path))
				// YAML permits NaN, which is unequal to itself. Compare its
				// stable representation along with the other canonical values.
				if fmt.Sprintf("%#v", x) != fmt.Sprintf("%#v", y) {
					t.Fatalf("decoded input %s is not repeatable: %v != %v", in.Name, x, y)
				}
			}
		}
		if fmt.Sprintf("%#v", raw) != before {
			t.Fatal("decoder modified the input document")
		}
	})
}

// roundTripPayload is the payload FuzzGoKindRoundTrip's kinds decide. The
// count and text defaults vary per input, so SetDefault replaces these
// after Build rather than each input writing its own into the tags: a
// payload type per input is one reflect.StructOf would keep for the life
// of the fuzz worker, which then grows until it runs out of memory.
type roundTripPayload struct {
	Count int64  `policy:"count,default=0"`
	Text  string `policy:"text,default=\"\""`
	Level Tier   `policy:"level,default=standard"`
}

func FuzzGoKindRoundTrip(f *testing.F) {
	f.Add(int64(0), "", false)
	f.Add(int64(-9223372036854775808), "\"\n世界", true)
	f.Fuzz(func(t *testing.T, n int64, s string, all bool) {
		opts := gokind.Options{Name: "Generated", Version: 2, Accepts: new(1), Input: reflect.TypeFor[struct{}](), Collect: all,
			Enums:     []gokind.Enum{{Type: reflect.TypeFor[Tier](), Values: []string{"critical", "standard"}}},
			Decisions: []gokind.Decision{{Name: "allow", Payload: reflect.TypeFor[roundTripPayload](), Reasons: []string{"ok"}}},
			Default:   &gokind.Default{Decision: "allow", Reason: "ok"},
		}
		if !all {
			// Only a `collect one` kind may declare a conflict outcome.
			opts.Decisions = append(opts.Decisions, gokind.Decision{Name: "deny", Payload: reflect.TypeFor[struct{}](), Reasons: []string{"conflicting_rules"}})
			opts.Conflict = &gokind.Default{Decision: "deny", Reason: "conflicting_rules"}
		}
		k, _, errs := gokind.Build(opts)
		if errs != nil {
			t.Fatal(errs)
		}
		// The defaults the tags `policy:"count,default=<n>"` and
		// `policy:"text,default=<s>"` would give.
		allow := k.Decision("allow")
		for name, want := range map[string]any{"count": n, "text": s} {
			field := allow.Field(name)
			src := constant.Format(want)
			if derrs := gokind.SetDefault(field, "allow", src); derrs != nil {
				t.Fatalf("default=%s doesn't parse: %v", src, derrs)
			}
			if !reflect.DeepEqual(field.Default, want) {
				t.Fatalf("default=%s is %#v, want %#v", src, field.Default, want)
			}
		}
		again, errs := check.LoadKind("export.sigil", []byte(k.Source()))
		if errs != nil {
			t.Fatalf("Go kind export does not load: %v\n%s", errs, k.Source())
		}
		if !reflect.DeepEqual(k, again) {
			t.Fatalf("Go kind changed on import:\n%s\nthen:\n%s", k.Source(), again.Source())
		}
	})
}
