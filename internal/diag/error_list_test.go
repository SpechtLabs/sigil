package diag_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/spechtlabs/sigil/internal/diag"
)

func TestErrorList(t *testing.T) {
	a := &diag.Error{File: "f", Msg: "first", Pos: at(0, 1, 1)}
	b := &diag.Error{File: "f", Msg: "second", Pos: at(9, 2, 3)}
	c := &diag.Error{File: "f", Msg: "third at same offset as second", Pos: at(9, 2, 3)}

	t.Run("Error joins lines", func(t *testing.T) {
		l := diag.ErrorList{a, b}
		if got, want := l.Error(), "f:1:1: first\nf:2:3: second"; got != want {
			t.Errorf("Error() = %q, want %q", got, want)
		}
	})

	t.Run("Err is nil when empty", func(t *testing.T) {
		if err := (diag.ErrorList{}).Err(); err != nil {
			t.Errorf("Err() = %v, want nil", err)
		}
		if err := diag.ErrorList(nil).Err(); err != nil {
			t.Errorf("Err() = %v, want nil", err)
		}
		if err := (diag.ErrorList{a}).Err(); err == nil {
			t.Error("Err() = nil, want the list")
		}
	})

	t.Run("Sort is by offset and stable", func(t *testing.T) {
		l := diag.ErrorList{b, c, a}
		l.Sort()
		if l[0] != a || l[1] != b || l[2] != c {
			t.Errorf("Sort() = %v, want [first second third]", l)
		}
	})

	t.Run("From unwraps", func(t *testing.T) {
		wrapped := fmt.Errorf("load failed: %w", diag.ErrorList{a})
		if got := diag.From(wrapped); len(got) != 1 || got[0] != a {
			t.Errorf("From(wrapped) = %v, want [first]", got)
		}
		if got := diag.From(errors.New("plain")); got != nil {
			t.Errorf("From(plain) = %v, want nil", got)
		}
		if got := diag.From(nil); got != nil {
			t.Errorf("From(nil) = %v, want nil", got)
		}
	})
}
