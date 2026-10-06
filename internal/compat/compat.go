// Package compat compares two versions of a kind and classifies every
// change between them by the compatibility table at
// https://sigil.specht-labs.de/reference/kind-files/#versioning: a change
// is [Compatible], [Breaking] because a policy written against the old
// kind may stop compiling, or breaking in [Behavior] because every policy
// still compiles but decisions may change.
//
// [Compare] walks the two kinds' declarations in canonical order and
// returns a [Report]: the changes, and the header rules the new kind's
// `version` and `accepts` break. The rules are the two the reference
// states, every change to the contract bumps `version` and a breaking
// change raises `accepts` to the new version, plus the two that follow
// from them: `version` never goes down, and neither does `accepts`.
//
// A change is judged on the model, not on the text of the kind file, so
// comments and layout never count. Order counts only as far as the
// canonical form prints it: reordering an enum's values is a compatible
// change, not no change, and reordering `precedence` is a change in
// behavior. A `default` or `conflict` that passes a payload field its own
// default means the same as one that leaves it out. A rename is a removal
// and an addition; nothing guesses that two names mean the same thing.
package compat

import (
	"fmt"
	"strings"

	"github.com/spechtlabs/sigil/internal/kind"
)

// behaviorFix ends the help of a change in behavior, which raising
// `accepts` is the only protection against.
const behaviorFix = "so policies pinned to older versions are reviewed before they load"

// How a change affects the policies written against the old kind.
const (
	Compatible Class = iota // every policy that compiled against the old kind compiles and decides as before
	Breaking                // a policy that compiled against the old kind may stop compiling
	Behavior                // every policy still compiles, but decisions may change
)

// What happened to the declaration a [Change] names.
const (
	Added     Op = "added"     // the new kind declares it and the old one doesn't
	Removed   Op = "removed"   // the old kind declares it and the new one doesn't
	Changed   Op = "changed"   // both declare it, differently
	Reordered Op = "reordered" // both declare the same members, in another order
	Ambiguous Op = "ambiguous" // a value the old kind declared once is now declared by several enums
)

// How serious a [Problem] is.
const (
	Error Severity = iota // the header breaks a rule, which fails the comparison
	Note                  // worth knowing, and fine
)

// The header rules a [Problem] reports.
const (
	VersionDecreased Rule = "version_decreased"  // `version` went down
	VersionUnchanged Rule = "version_unchanged"  // the contract changed, but `version` didn't
	AcceptsNotRaised Rule = "accepts_not_raised" // a change breaks, but `accepts` wasn't raised to the new version
	AcceptsLowered   Rule = "accepts_lowered"    // `accepts` went down
	VersionOnly      Rule = "version_only"       // `version` went up, but the contract didn't change
)

// Class is how a change affects the policies written against the old
// kind.
type Class int

// Op is what happened to the declaration a [Change] names.
type Op string

// Severity is how serious a [Problem] is.
type Severity int

// Rule names a header rule.
type Rule string

// Change is one difference between the old kind and the new one.
type Change struct {
	Op Op
	// Path names the declaration, as a kind file spells it: `enum Tier
	// value standard`, `decision deny reason no_release`, `precedence`,
	// `precedence deny` or `exclusive approve, deny`. A reorder names the
	// list: `enum Tier values`, `decision approve fields` or `inputs`.
	Path string
	// Old and New render the declaration, or the list a reorder is about,
	// on one line as the old and the new kind file write it, such as `bake:
	// duration = 1h` or `deny > review > approve`. Each is empty when that
	// kind doesn't declare it.
	Old, New string
	Message  string // what changed, like "decision deny lost reason `no_release`"
	// Why says what the change does to the policies written against the
	// old kind, for a change that isn't compatible, like "policies that
	// construct deny(reason: no_release) no longer compile".
	Why string
	// Fix is what to do besides raising `accepts`, appended to the help:
	// "and qualify it as `Tier.standard`". Empty when there's nothing else.
	Fix   string
	Class Class
}

// Help is the advice for a change that isn't compatible, when `accepts`
// doesn't cover it yet: why it breaks, and to raise `accepts` to the
// version given.
func (c Change) Help(accepts int) string {
	raise := fmt.Sprintf("raise `accepts` to %d", accepts)
	if c.Fix != "" {
		raise += ", " + c.Fix
	}
	if c.Why == "" {
		return raise
	}
	return c.Why + "; " + raise
}

// Header is what a kind header declares.
type Header struct {
	Name    string
	Version int
	Accepts int
}

// Problem is a header rule the new kind breaks, or a note about its
// header.
type Problem struct {
	Rule     Rule
	Message  string
	Help     string // empty for a note that needs nothing done
	Severity Severity
}

// Report is what [Compare] found.
type Report struct {
	// Changes holds every change, in the order the kinds declare what
	// changed: the header, enums, struct types, inputs, host functions,
	// decisions, then collect, precedence, exclusive, default and conflict.
	Changes []Change
	// Problems holds the header rules the new kind breaks, then the notes.
	Problems []Problem
	Old, New Header
	// MinVersion is the lowest `version` the new kind may declare: the old
	// version when the contract didn't change, and one more otherwise.
	MinVersion int
	// MinAccepts is the lowest `accepts` the new kind may declare: the
	// new version, at least MinVersion, when a change breaks, and the old
	// `accepts` otherwise.
	MinAccepts int
}

// Compare compares the old kind with next, the new one, which should both
// be valid, as [kind.Kind.Validate] checks. Compare doesn't fail on an
// invalid kind, but what it reports about one isn't meaningful.
func Compare(old, next *kind.Kind) *Report {
	d := &differ{old: old, next: next}
	d.header()
	d.enums()
	d.types()
	d.inputs()
	d.funcs()
	d.decisions()
	d.resolution()
	r := &Report{
		Old:     Header{Name: old.Name, Version: old.Version, Accepts: old.Accepts},
		New:     Header{Name: next.Name, Version: next.Version, Accepts: next.Accepts},
		Changes: d.changes,
	}
	r.check()
	return r
}

// String implements [fmt.Stringer]. It returns the class's name as JSON
// and text output print it: compatible, breaking or behavior.
func (c Class) String() string {
	switch c {
	case Breaking:
		return "breaking"
	case Behavior:
		return "behavior"
	}
	return "compatible"
}

// String implements [fmt.Stringer]. It returns error or note.
func (s Severity) String() string {
	if s == Note {
		return "note"
	}
	return "error"
}

// Breaking returns how many changes aren't compatible, the ones breaking
// in behavior included.
func (r *Report) Breaking() int {
	n := 0
	for _, c := range r.Changes {
		if c.Class != Compatible {
			n++
		}
	}
	return n
}

// Covered reports whether the new kind's `accepts` covers the changes
// that aren't compatible: it's at least [Report.MinAccepts], so every
// policy pinned to a version the old kind had is rejected until its team
// reviews the changes and raises the pin.
func (r *Report) Covered() bool {
	return r.New.Accepts >= r.MinAccepts
}

// OK reports whether the new kind's header breaks no rule.
func (r *Report) OK() bool {
	for _, p := range r.Problems {
		if p.Severity == Error {
			return false
		}
	}
	return true
}

// check works out the lowest version and accepts the new kind may
// declare, and records every header rule it breaks.
func (r *Report) check() {
	changed, breaking := len(r.Changes) > 0, r.Breaking()
	r.MinVersion = r.Old.Version
	if changed {
		r.MinVersion++
	}
	r.MinAccepts = r.Old.Accepts
	if breaking > 0 {
		r.MinAccepts = max(r.New.Version, r.MinVersion)
	}

	switch {
	case r.New.Version < r.Old.Version:
		r.problem(VersionDecreased, Error,
			fmt.Sprintf("`version` went down from %d to %d", r.Old.Version, r.New.Version),
			fmt.Sprintf("a kind's version only goes up; set `version` to %d, or check that OLD_KIND_FILE comes first", r.MinVersion))
	case changed && r.New.Version == r.Old.Version:
		r.problem(VersionUnchanged, Error,
			fmt.Sprintf("the contract changed, but `version` is still %d", r.Old.Version),
			fmt.Sprintf("bump `version` to %d", r.MinVersion))
	}
	switch {
	case breaking > 0 && !r.Covered():
		r.problem(AcceptsNotRaised, Error,
			fmt.Sprintf("%s, but `accepts` is %d", count(breaking, "breaking change"), r.New.Accepts),
			fmt.Sprintf("raise `accepts` to %d, so policies written against version %d or earlier are reviewed before they load", r.MinAccepts, r.Old.Version))
	case r.New.Accepts < r.Old.Accepts:
		r.problem(AcceptsLowered, Error,
			fmt.Sprintf("`accepts` went down from %d to %d", r.Old.Accepts, r.New.Accepts),
			fmt.Sprintf("policies pinned below %d were rejected for changes made since; keep `accepts` at %d or above", r.Old.Accepts, r.Old.Accepts))
	}
	if !changed && r.New.Version > r.Old.Version {
		r.problem(VersionOnly, Note,
			fmt.Sprintf("`version` went from %d to %d, but the contract didn't change", r.Old.Version, r.New.Version), "")
	}
}

func (r *Report) problem(rule Rule, sev Severity, msg, help string) {
	r.Problems = append(r.Problems, Problem{Rule: rule, Severity: sev, Message: msg, Help: help})
}

// count spells `1 breaking change` or `2 breaking changes`.
func count(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}

// list joins names for a message: `a`, `a and b`, `a, b and c`.
func list(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
