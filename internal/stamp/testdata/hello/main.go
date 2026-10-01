// hello prints the payload in its reserved area. stamp's integration tests
// build it for several platforms and patch it.
package main

import (
	"bytes"
	"fmt"
)

// area is the reserved area: the marker the tests pass to Patch, then the
// payload. The marker's bytes make it data rather than BSS.
var area = [1 << 20]byte{'S', 'T', 'A', 'M', 'P', '-', 'M', 'A', 'R', 'K', 'E', 'R'}

func main() {
	payload := area[12:]
	if n := bytes.IndexByte(payload, 0); n >= 0 {
		payload = payload[:n]
	}
	if len(payload) == 0 {
		fmt.Println("no payload")
		return
	}
	fmt.Printf("payload: %s\n", payload)
}
