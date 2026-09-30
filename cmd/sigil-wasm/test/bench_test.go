package wasmtest

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/result"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// BenchmarkEval measures one evaluation of the deploy-gates example's
// access policy three ways: the evaluator natively, on an input decoded
// once; the engine natively, JSON in and out as the module does; and the
// module through its ABI. The difference between the first two is the
// JSON, and between the last two the WebAssembly runtime and copying
// across the boundary.
func BenchmarkEval(b *testing.B) {
	fs := files(b, gates)
	input := fixture(b, gates+"/access/testdata/admin-platform.json")
	compile := encode(b, map[string]any{"op": "compile", "files": fs, "policy": "access.main"})
	eval := encode(b, map[string]any{"op": "eval", "handle": 1, "input": input})

	b.Run("native", func(b *testing.B) {
		loader := workspace.NewLoader(nil)
		wf := make([]workspace.File, len(fs))
		for i, f := range fs {
			wf[i] = workspace.File{Name: f["path"], Source: []byte(f["source"])}
		}
		p := loader.Load(wf, nil)
		p.Check()
		g := p.Group("access.main")
		prog, errs := p.ScopeOf([]string{"access.main"}).Bundle(g).Compile("access.main", bundle.Options{Binding: g.Kind.Binding})
		if errs != nil {
			b.Fatal(errs)
		}
		dec := json.NewDecoder(bytes.NewReader(input))
		dec.UseNumber()
		var raw any
		if err := dec.Decode(&raw); err != nil {
			b.Fatal(err)
		}
		in, derr := g.Kind.Binding.DecodeInput(g.Kind.Model, raw)
		if derr != nil {
			b.Fatal(derr)
		}
		b.ReportAllocs()
		for b.Loop() {
			if res := result.Evaluate(prog, in.Interface()); res.Failure != nil {
				b.Fatal(res.Failure)
			}
		}
	})
	b.Run("engine", func(b *testing.B) {
		e := newNative()
		ready(b, e.Call(compile), e.Call(eval))
		b.ReportAllocs()
		for b.Loop() {
			e.Call(eval)
		}
	})
	b.Run("wasm", func(b *testing.B) {
		inst := newInstance(b, nil)
		ready(b, inst.Call(compile), inst.Call(eval))
		b.ReportMetric(float64(len(wasm)), "module-bytes")
		for b.Loop() {
			inst.Call(eval)
		}
	})
}

// ready fails the benchmark unless the compile and a first evaluation
// succeeded, so it never measures an error.
func ready(b *testing.B, compiled, evaluated []byte) {
	b.Helper()
	if !bytes.Contains(compiled, []byte(`"ok":true`)) || !bytes.Contains(evaluated, []byte(`"ok":true`)) || bytes.Contains(evaluated, []byte(`"error"`)) {
		b.Fatalf("compile = %s\neval = %s", compiled, evaluated)
	}
}

// BenchmarkRoundTrip measures the ABI alone: a request that does almost
// nothing, a format of an empty source, allocated, sent, answered, read
// and freed, natively and through the module.
func BenchmarkRoundTrip(b *testing.B) {
	req := encode(b, map[string]any{"op": "format", "source": ""})
	b.Run("engine", func(b *testing.B) {
		e := newNative()
		for b.Loop() {
			e.Call(req)
		}
	})
	b.Run("wasm", func(b *testing.B) {
		inst := newInstance(b, nil)
		for b.Loop() {
			inst.Call(req)
		}
	})
}
