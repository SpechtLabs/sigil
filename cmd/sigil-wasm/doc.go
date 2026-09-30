// Command sigil-wasm is Sigil as a WebAssembly module: the stock sigil
// CLI's engine, package internal/engine, without a filesystem or a
// terminal, for hosts outside Go. Every host gets Go's semantics exactly,
// RE2 regular expressions, Unicode string comparison, duration arithmetic
// and integer overflow included, because it runs the same code.
//
// Build it with `mise run wasm-build`, which writes dist/wasm/sigil.wasm:
//
//	GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared -trimpath -ldflags='-s -w' -o dist/wasm/sigil.wasm ./cmd/sigil-wasm
//
// The module is a WASI preview 1 reactor: it exports _initialize, which
// the host calls once after instantiating it, and no _start. It imports
// only WASI preview 1 functions and sigil.host_call, below.
//
// # The ABI, version 1
//
// Four exports carry every request:
//
//	sigil_abi_version() -> i32           1; it changes only when these signatures or the rules below do
//	sigil_alloc(size i32) -> i32         allocates size bytes and returns their address
//	sigil_free(ptr i32, size i32)        releases memory from sigil_alloc, or a response
//	sigil_call(ptr i32, len i32) -> i64  handles one request; returns (respPtr << 32) | respLen
//
// A request is a JSON object, and so is its response; package
// internal/engine documents the ops. One call goes:
//
//  1. The host allocates len bytes with sigil_alloc and writes the request there.
//  2. The host calls sigil_call with that address and len.
//  3. The host reads the response at respPtr, respLen bytes long.
//  4. The host frees the request with sigil_free(ptr, len), and the response with sigil_free(respPtr, respLen).
//
// While sigil_call runs, a policy may call a host function the compile
// request named in its functions. The module then calls the import
//
//	sigil.host_call(reqPtr i32, reqLen i32) -> i64
//
// with a request, {"function": "split", "args": [...]}, and the host
// answers with {"result": ...} or {"error": "..."}:
//
//  1. The host reads the request at reqPtr, reqLen bytes long. The module owns it and frees it once host_call returns.
//  2. The host allocates the response with sigil_alloc, from inside host_call, and writes it there.
//  3. The host returns (ptr << 32) | len of the response. The module owns it from then on, and frees it once read.
//
// # Required policies
//
// A host that compiles bundles it doesn't control, such as a team's,
// makes the root invoke its own guardrails the way a Go host does with
// policy.Require(name, policy.From(trusted)): compile takes the host's
// documents as trusted_files, beside the bundle's files, and the
// required policies as require: [{"policy": name}]. The root must invoke
// each at its top level, with arguments in its params' bounds, and no
// file may redefine it; the diagnostics are Go's. Two rules are stricter
// than Go's:
//
//   - With trusted_files, a required policy they don't define fails the
//     compile, even when the bundle defines it. Go would take the bundle's
//     copy, which anyone who writes to the bundle controls.
//   - A path among both the files and the trusted files fails the request,
//     so a file can't pass for a trusted one by its path: trust comes from
//     the list a file is in, never from its name.
//
// # Memory
//
// Every buffer the host sees, a request, a response, or a host function
// call's request or response, is a Go byte slice the module keeps
// reachable until it's freed, so the garbage collector never reclaims or
// reuses memory the host still reads or writes. A buffer that isn't freed
// stays for the life of the instance; freeing one twice, or an address
// sigil_alloc didn't return, does nothing. Compiled policies live in the
// module, as handles, until the release op.
//
// # Concurrency
//
// An instance runs one call at a time, on one thread: a host function
// must not call sigil_call on the instance that's running it. Hosts that
// want parallelism run several instances. An evaluation's timeout_ms is
// checked while it runs, so a policy stuck in a long loop over its input
// stops at its deadline; a host that must bound time strictly, such as a
// browser UI, also runs the module where it can terminate it, like a Web
// Worker.
package main
