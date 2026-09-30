//go:build !wasip1

package main

import (
	"fmt"
	"os"
)

// main explains that the command is a WebAssembly module, and how to
// build it, when it's built for anything else.
func main() {
	fmt.Fprintln(os.Stderr, "sigil-wasm is a WebAssembly module: build it with `mise run wasm-build`, or GOOS=wasip1 GOARCH=wasm go build -buildmode=c-shared ./cmd/sigil-wasm")
	os.Exit(2)
}
