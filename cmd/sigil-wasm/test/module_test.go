// Package wasmtest drives sigil.wasm, the WebAssembly module, through its
// ABI with the wazero runtime, as a host would: every op, host functions
// through sigil.host_call, stubs, errors, handles and memory, parity with
// the CLI and with the engine running natively, and what an evaluation
// costs through the ABI.
//
// TestMain builds the module from the checkout, as `mise run wasm-build`
// does, unless SIGIL_WASM names one already built.
package wasmtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

// root is the sigil checkout, from this directory.
const root = "../../.."

var (
	// wasm is the module's binary, which TestMain builds or reads.
	wasm []byte
	// cache keeps the compiled module across the runtimes of the tests,
	// each an instance of its own.
	cache = wazero.NewCompilationCache()
)

// instance is one instance of the module, in a runtime of its own.
type instance struct {
	t      testing.TB
	ctx    context.Context
	rt     wazero.Runtime
	mod    api.Module
	alloc  api.Function
	free   api.Function
	call   api.Function
	stderr bytes.Buffer
	// host answers the module's host function calls: it gets the request
	// and returns the response, which the instance writes into memory
	// from sigil_alloc. nil answers every call with an error.
	host func(req []byte) []byte
	// hostRaw, when set, replaces the whole host_call import: it gets the
	// request and returns the packed pointer and length itself.
	hostRaw func(req []byte) uint64
}

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(code)
}

// run builds the module, or reads SIGIL_WASM, and runs the tests.
func run(m *testing.M) (int, error) {
	path := os.Getenv("SIGIL_WASM")
	if path == "" {
		dir, err := os.MkdirTemp("", "sigil-wasm-")
		if err != nil {
			return 0, err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		path = filepath.Join(dir, "sigil.wasm")
		build := exec.Command("go", "build", "-buildmode=c-shared", "-trimpath", "-ldflags=-s -w", "-o", path, "./cmd/sigil-wasm")
		build.Dir = root
		build.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if out, err := build.CombinedOutput(); err != nil {
			return 0, fmt.Errorf("building sigil.wasm: %v\n%s", err, out)
		}
	}
	var err error
	if wasm, err = os.ReadFile(path); err != nil {
		return 0, err
	}
	return m.Run(), nil
}

// newInstance instantiates the module, initialized, with host answering
// its host function calls.
func newInstance(t testing.TB, host func(req []byte) []byte) *instance {
	t.Helper()
	ctx := context.Background()
	inst := &instance{t: t, ctx: ctx, host: host}
	inst.rt = wazero.NewRuntimeWithConfig(ctx, wazero.NewRuntimeConfig().WithCompilationCache(cache))
	t.Cleanup(func() { _ = inst.rt.Close(ctx) })
	wasi_snapshot_preview1.MustInstantiate(ctx, inst.rt)
	_, err := inst.rt.NewHostModuleBuilder("sigil").NewFunctionBuilder().
		WithFunc(func(ctx context.Context, m api.Module, ptr, size uint32) uint64 {
			req, ok := m.Memory().Read(ptr, size)
			if !ok {
				t.Errorf("host_call(%d, %d) is outside the module's memory", ptr, size)
				return 0
			}
			req = bytes.Clone(req)
			if inst.hostRaw != nil {
				return inst.hostRaw(req)
			}
			resp := []byte(`{"error": "this test attached no host"}`)
			if inst.host != nil {
				resp = inst.host(req)
			}
			return inst.write(ctx, m, resp)
		}).Export("host_call").Instantiate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	code, err := inst.rt.CompileModule(ctx, wasm)
	if err != nil {
		t.Fatal(err)
	}
	cfg := wazero.NewModuleConfig().WithStartFunctions("_initialize").WithStderr(&inst.stderr).WithSysWalltime().WithSysNanotime().WithSysNanosleep()
	if inst.mod, err = inst.rt.InstantiateModule(ctx, code, cfg); err != nil {
		t.Fatal(err)
	}
	inst.alloc = inst.mod.ExportedFunction("sigil_alloc")
	inst.free = inst.mod.ExportedFunction("sigil_free")
	inst.call = inst.mod.ExportedFunction("sigil_call")
	return inst
}

// write allocates b in the module, from inside a call, and returns its
// packed pointer and length.
func (inst *instance) write(ctx context.Context, m api.Module, b []byte) uint64 {
	res, err := m.ExportedFunction("sigil_alloc").Call(ctx, uint64(len(b)))
	if err != nil {
		inst.t.Errorf("sigil_alloc from host_call: %v", err)
		return 0
	}
	if !m.Memory().Write(uint32(res[0]), b) {
		inst.t.Errorf("sigil_alloc returned %d, outside the module's memory", res[0])
	}
	return res[0]<<32 | uint64(len(b))
}

// Call sends a raw request through the ABI, frees both buffers, and
// returns the response.
func (inst *instance) Call(req []byte) []byte {
	inst.t.Helper()
	res, err := inst.alloc.Call(inst.ctx, uint64(len(req)))
	if err != nil {
		inst.t.Fatalf("sigil_alloc: %v\n%s", err, inst.stderr.String())
	}
	ptr := uint32(res[0])
	inst.mod.Memory().Write(ptr, req)
	packed, err := inst.call.Call(inst.ctx, uint64(ptr), uint64(len(req)))
	if err != nil {
		inst.t.Fatalf("sigil_call: %v\n%s", err, inst.stderr.String())
	}
	rptr, rlen := uint32(packed[0]>>32), uint32(packed[0])
	resp, ok := inst.mod.Memory().Read(rptr, rlen)
	if !ok {
		inst.t.Fatalf("sigil_call returned %d bytes at %d, outside the module's memory", rlen, rptr)
	}
	resp = bytes.Clone(resp)
	inst.freeBuf(ptr, uint32(len(req)))
	inst.freeBuf(rptr, rlen)
	return resp
}

// Request encodes req, sends it, and decodes the response.
func (inst *instance) Request(req map[string]any) map[string]any {
	inst.t.Helper()
	var resp map[string]any
	if err := json.Unmarshal(inst.Call(encode(inst.t, req)), &resp); err != nil {
		inst.t.Fatalf("the response isn't JSON: %v", err)
	}
	return resp
}

// memory returns the size of the module's memory, in bytes.
func (inst *instance) memory() uint32 { return inst.mod.Memory().Size() }

func (inst *instance) freeBuf(ptr, size uint32) {
	if _, err := inst.free.Call(inst.ctx, uint64(ptr), uint64(size)); err != nil {
		inst.t.Fatalf("sigil_free: %v", err)
	}
}

// encode encodes a request.
func encode(t testing.TB, req map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
