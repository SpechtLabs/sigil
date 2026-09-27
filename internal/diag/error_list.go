package diag

import (
	"errors"
	"sort"
	"strings"
)

// ErrorList is every diagnostic a stage produced, in source order.
type ErrorList []*Error

// Error joins the diagnostics one per line.
func (l ErrorList) Error() string {
	msgs := make([]string, len(l))
	for i, e := range l {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Err returns l as an error, or nil when the list is empty. A List is a
// slice, so returning it directly as an error would be non-nil even when
// empty.
func (l ErrorList) Err() error {
	if len(l) == 0 {
		return nil
	}
	return l
}

// Sort orders the list by position, keeping the order of diagnostics at the
// same position.
func (l ErrorList) Sort() {
	sort.SliceStable(l, func(i, j int) bool {
		return l[i].Pos.Offset < l[j].Pos.Offset
	})
}

// From returns the List inside err, or nil when err isn't one.
func From(err error) ErrorList {
	l, _ := errors.AsType[ErrorList](err)
	return l
}
