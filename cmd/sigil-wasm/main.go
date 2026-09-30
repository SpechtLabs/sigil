//go:build wasip1

package main

import (
	"unsafe"

	"github.com/spechtlabs/sigil/internal/engine"
)

// abiVersion is the version of the exports and the import below, which
// changes only when one of them does. The requests and responses carry
// their own compatibility: an op or a field is added, never changed.
const abiVersion = 1

var (
	// pinned holds every buffer the host can see: requests it allocated
	// with sigil_alloc, responses, and the requests of host function
	// calls. A Go slice's memory is reclaimed once nothing references it,
	// so each one stays here, keyed by its address, until it's freed.
	pinned = map[uint32][]byte{}

	// sigil is the one engine: its handles live as long as the module
	// instance.
	sigil = engine.New(engine.WithHost(callHost), engine.WithVersion(version))
)

// version is set with -ldflags "-X main.version=..." by a release build.
var version string

//go:wasmexport sigil_abi_version
func sigilABIVersion() int32 { return abiVersion }

//go:wasmexport sigil_alloc
func sigilAlloc(size int32) uint32 {
	return pin(make([]byte, max(size, 0)))
}

//go:wasmexport sigil_free
func sigilFree(ptr, _ uint32) {
	delete(pinned, ptr)
}

//go:wasmexport sigil_call
func sigilCall(ptr, size uint32) uint64 {
	req, ok := pinned[ptr]
	var resp []byte
	if !ok || int(size) > len(req) {
		resp = engine.Malformed("the request isn't in memory from sigil_alloc: allocate it with sigil_alloc, write it, and pass that pointer and the length written")
	} else {
		resp = sigil.Call(req[:size])
	}
	return uint64(pin(resp))<<32 | uint64(len(resp))
}

//go:wasmimport sigil host_call
func hostCall(ptr, size uint32) uint64

func main() {}

// callHost sends one host function request to the host and returns its
// response, which the host wrote into memory from sigil_alloc.
func callHost(req []byte) []byte {
	ptr := pin(req)
	packed := hostCall(ptr, uint32(len(req))) //nolint:gosec // a request is far below 4 GiB, the size of the module's memory
	delete(pinned, ptr)
	rptr, rsize := uint32(packed>>32), uint32(packed) //nolint:gosec // the two halves of the packed pointer and length
	resp, ok := pinned[rptr]
	delete(pinned, rptr)
	if !ok || int(rsize) > len(resp) {
		return engine.HostMisbehaved("host_call returned memory it didn't allocate with sigil_alloc")
	}
	return resp[:rsize]
}

// pin keeps b reachable until the host frees it, and returns its address.
// An empty buffer gets a byte of capacity, so it has an address of its own.
func pin(b []byte) uint32 {
	if cap(b) == 0 {
		b = make([]byte, 0, 1)
	}
	ptr := uint32(uintptr(unsafe.Pointer(unsafe.SliceData(b)))) //nolint:gosec // wasm32 addresses fit in 32 bits
	pinned[ptr] = b
	return ptr
}
