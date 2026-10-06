package compat

import (
	"slices"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/kind"
)

// differ collects the changes between two kinds, section by section.
type differ struct {
	old, next *kind.Kind
	changes   []Change
}

// add records a change.
func (d *differ) add(c Change) {
	d.changes = append(d.changes, c)
}

// reorder records a compatible change when the members old and next both
// hold come in another order. Members only one of them holds are left
// out: their addition or removal is a change of its own. path names the
// list, what is the message, and sep joins the members for Old and New.
func (d *differ) reorder(path, what, sep string, old, next []string) {
	if !reordered(old, next) {
		return
	}
	d.add(Change{Op: Reordered, Class: Compatible, Path: path, Old: strings.Join(old, sep), New: strings.Join(next, sep), Message: what})
}

// header compares the kinds' names. The numbers aren't part of the
// contract; [Report] checks them against the changes.
func (d *differ) header() {
	if d.old.Name == d.next.Name {
		return
	}
	d.add(Change{
		Op: Changed, Class: Breaking, Path: "kind", Old: d.old.Name, New: d.next.Name,
		Message: "kind " + d.old.Name + " is now called " + d.next.Name,
		Why:     "every policy's header names its kind, as in `policy deploy.production: " + d.old.Name + "@" + strconv.Itoa(d.old.Version) + "`, so none of them finds it",
	})
}

// reordered reports whether the members old and next share come in
// another order in next than in old.
func reordered(old, next []string) bool {
	return !slices.Equal(common(old, next), common(next, old))
}

// common returns the members of a that b holds too, in a's order, each
// once.
func common(a, b []string) []string {
	out := make([]string, 0, len(a))
	for _, s := range a {
		if slices.Contains(b, s) && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

// missing returns the members of a that b doesn't hold, in a's order.
func missing(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}
