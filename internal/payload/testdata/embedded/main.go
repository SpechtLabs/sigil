// Command embedded prints the payload compiled into it, for the tests of
// package payload: "none" when there's none, otherwise the root policy
// and the bundle's digest.
package main

import (
	"fmt"
	"os"

	"github.com/spechtlabs/sigil/internal/payload"
)

func main() {
	p, err := payload.Embedded()
	switch {
	case err != nil:
		fmt.Println(err)
		os.Exit(1)
	case p == nil:
		fmt.Println("none")
	default:
		fmt.Println(p.Bundle.Root, p.Bundle.Digest())
	}
}
