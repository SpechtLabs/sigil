package diag

import (
	"errors"
	"sort"
	"strings"
)

// ErrorList is every diagnostic a stage produced, in source order. Use
// [ErrorList.Err] to turn a list into an error value.
type ErrorList []*Error

// Error implements the error interface. It joins the diagnostics' messages,
// as [Error.Error] formats them, one per line.
func (l ErrorList) Error() string {
	msgs := make([]string, len(l))
	for i, e := range l {
		msgs[i] = e.Error()
	}
	return strings.Join(msgs, "\n")
}

// Err returns l as an error, or nil when the list is empty. An ErrorList
// is a slice, so returning it directly as an error would be non-nil even
// when empty.
func (l ErrorList) Err() error {
	if len(l) == 0 {
		return nil
	}
	return l
}

// Sort orders the list in place by byte offset, keeping the order of
// diagnostics at the same offset. It ignores File, so it suits a list from
// one file.
func (l ErrorList) Sort() {
	sort.SliceStable(l, func(i, j int) bool {
		return l[i].Pos.Offset < l[j].Pos.Offset
	})
}

// From returns the ErrorList in err's chain, as [errors.As] finds it, or
// nil when there is none.
func From(err error) ErrorList {
	l, _ := errors.AsType[ErrorList](err)
	return l
}
