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
	for _, src := range []string{synthKind, decodeKind, "kind K version 1\ndecision d { r }\ncollect all"} {
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
	for _, src := range []string{`{}`, `null`, `{"s":{"n":9223372036854775807,"o":null,"l":[1,2],"m":{"x":3}}}`, `{"s":{"d":"1h30m","t":"2026-09-28T12:00:00Z","inner":{"x":2}}}`, "s:\n  n: 2\n  mb: {true: 1}\n", "s: {f: .nan}", "&a [*a]"} {
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
		before, _ := json.Marshal(raw)
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
		after, _ := json.Marshal(raw)
		if !bytes.Equal(before, after) {
			t.Fatal("decoder modified the input document")
		}
	})
}

func FuzzGoKindRoundTrip(f *testing.F) {
	f.Add(int64(0), "", false)
	f.Add(int64(-9223372036854775808), "\"\n世界", true)
	f.Fuzz(func(t *testing.T, n int64, s string, all bool) {
		payload := reflect.StructOf([]reflect.StructField{
			{Name: "Count", Type: reflect.TypeFor[int64](), Tag: reflect.StructTag(`policy:` + constant.Format("count,default="+constant.Format(n)))},
			{Name: "Text", Type: reflect.TypeFor[string](), Tag: reflect.StructTag(`policy:` + constant.Format("text,default="+constant.Format(s)))},
		})
		opts := gokind.Options{Name: "Generated", Version: 2, Accepts: new(1), Input: reflect.TypeFor[struct{}](), Collect: all,
			Decisions: []gokind.Decision{{Name: "allow", Payload: payload, Reasons: []string{"ok"}}},
			Default:   &gokind.Default{Decision: "allow", Reason: "ok"},
		}
		k, _, errs := gokind.Build(opts)
		if errs != nil {
			t.Fatal(errs)
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
