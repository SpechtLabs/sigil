package test

import (
	"bytes"
	"testing"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

func TestProblem(t *testing.T) {
	th := pretty.New(&bytes.Buffer{}, pretty.WithEnviron(nil)).Theme()
	if got := problem(th, "msg", ""); got != "msg" {
		t.Errorf("problem() without help = %q", got)
	}
	if got := problem(th, "msg", "fix"); got != "msg\n  = help: fix" {
		t.Errorf("problem() with help = %q", got)
	}
}
