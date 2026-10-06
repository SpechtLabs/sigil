package compat

import (
	"slices"
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
)

// exclusiveSet is one `exclusive` declaration: its outcomes in declared
// order, and as a key that doesn't depend on the order.
type exclusiveSet struct {
	key     string // the outcomes, sorted, joined by ", "
	decl    string // the declaration, as the kind file writes it
	members []string
}

// resolution compares how candidates resolve: `collect`, the decision
// `precedence`, each scoped `precedence`, the `exclusive` sets, the
// `default` and the `conflict` outcome. Apart from `collect`, every
// change here leaves every policy compiling and changes what some of
// them decide, so it's breaking in behavior.
func (d *differ) resolution() {
	if d.old.Collect != d.next.Collect {
		d.add(Change{
			Op: Changed, Class: Breaking, Path: "collect", Old: collectDecl(d.old.Collect), New: collectDecl(d.next.Collect),
			Message: "`" + collectDecl(d.old.Collect) + "` changed to `" + collectDecl(d.next.Collect) + "`",
			Why:     "the host gets " + collected(d.next.Collect) + " instead of " + collected(d.old.Collect) + ", and a policy written for one doesn't decide the same under the other",
		})
	}
	d.ranking("precedence", "precedence", "decisions", d.old.Precedence, d.next.Precedence)
	for _, old := range d.old.Decisions {
		if next := d.next.Decision(old.Name); next != nil {
			d.ranking("precedence "+old.Name, "the precedence of "+old.Name+"'s reasons", "reasons of "+old.Name, old.Ranked, next.Ranked)
		}
	}
	d.exclusive()
	d.outcome("default", "evaluations where no rule fires", d.old.Default, d.next.Default)
	d.outcome("conflict", "evaluations that end in a conflict", d.old.Conflict, d.next.Conflict)
}

// ranking compares a `precedence` declaration, of decisions or of one
// decision's reasons. Members one of the kinds doesn't rank were added or
// removed, which is a change of its own, so only the members both rank
// can be reordered: a new decision ranked anywhere is compatible, since
// no policy written against the old kind constructs it.
func (d *differ) ranking(path, decl, what string, old, next []string) {
	c := Change{Class: Behavior, Path: path, Old: strings.Join(old, " > "), New: strings.Join(next, " > "), Fix: behaviorFix}
	switch {
	case len(old) == 0 && len(next) == 0:
		return
	case len(old) == 0:
		c.Op, c.Message = Added, decl+" was added"
		c.Why = "every policy still compiles, but the " + what + " are ranked now, so fewer candidates reach the host"
	case len(next) == 0:
		c.Op, c.Message = Removed, decl+" was removed"
		c.Why = "every policy still compiles, but the " + what + " aren't ranked any more, so candidates that lost before now tie"
	case reordered(old, next):
		c.Op, c.Message = Changed, decl+" changed"
		c.Why = "every policy still compiles, but the " + what + " rank differently"
	default:
		return
	}
	d.add(c)
}

// exclusive compares the `exclusive` sets. A set is the same set when it
// names the same outcomes, in any order, and a kind may declare one set
// twice, so each set of one kind pairs with at most one of the other's.
// Adding one turns evaluations where two of its outcomes fire into
// conflicts; removing one lets them both through, which can grant what
// the host said must never be granted together.
func (d *differ) exclusive() {
	old, next := exclusiveSets(d.old.Exclusive), exclusiveSets(d.next.Exclusive)
	pair := make([]int, len(old)) // the index in next of each old set's partner, or -1
	taken := make([]bool, len(next))
	for i, s := range old {
		pair[i] = -1
		for j, n := range next {
			if !taken[j] && n.key == s.key {
				pair[i], taken[j] = j, true
				break
			}
		}
	}
	for i, s := range old {
		if pair[i] < 0 {
			d.add(Change{
				Op: Removed, Class: Behavior, Path: "exclusive " + s.key, Old: s.decl,
				Message: s.decl + " was removed",
				Why:     "every policy still compiles, but evaluations where " + list(s.members) + " fire together no longer fail with a conflict",
				Fix:     behaviorFix,
			})
		}
	}
	for j, s := range next {
		if !taken[j] {
			d.add(Change{
				Op: Added, Class: Behavior, Path: "exclusive " + s.key, New: s.decl,
				Message: s.decl + " was added",
				Why:     "every policy still compiles, but evaluations where " + list(s.members) + " fire together fail with a conflict now",
				Fix:     behaviorFix,
			})
		}
	}
	var oldKeys, nextKeys []string
	for i, s := range old {
		if j := pair[i]; j >= 0 {
			oldKeys = append(oldKeys, s.key)
			if n := next[j]; !slices.Equal(s.members, n.members) {
				d.add(Change{Op: Reordered, Class: Compatible, Path: "exclusive " + s.key, Old: strings.Join(s.members, ", "), New: strings.Join(n.members, ", "), Message: s.decl + " was reordered"})
			}
		}
	}
	for j, s := range next {
		if taken[j] {
			nextKeys = append(nextKeys, s.key)
		}
	}
	if !slices.Equal(oldKeys, nextKeys) {
		d.add(Change{Op: Reordered, Class: Compatible, Path: "exclusive", Old: strings.Join(oldKeys, "; "), New: strings.Join(nextKeys, "; "), Message: "the exclusive sets were reordered"})
	}
}

// outcome compares the `default` or the `conflict` outcome, which is
// what decl names, by the outcome the host gets: the decision, the
// reason, and the value of every payload field both kinds declare,
// whether it's passed or left to its default. A field only one of them
// declares was added or removed, which is a change of its own, and
// spelling out a field's default changes nothing.
func (d *differ) outcome(decl, when string, old, next *kind.Default) {
	c := Change{Class: Behavior, Path: decl, Fix: behaviorFix}
	var oldDec, nextDec *kind.Decision
	if old != nil {
		oldDec = d.old.Decision(old.Decision)
		c.Old = old.Call(oldDec)
	}
	if next != nil {
		nextDec = d.next.Decision(next.Decision)
		c.New = next.Call(nextDec)
	}
	switch {
	case old == nil && next == nil:
		return
	case old == nil:
		c.Op, c.Message = Added, decl+" "+c.New+" was added"
		c.Why = "every policy still compiles, but " + when + " return " + c.New + " now"
	case next == nil:
		c.Op, c.Message = Removed, decl+" "+c.Old+" was removed"
		c.Why = "every policy still compiles, but " + when + " don't return " + c.Old + " any more"
	case !sameOutcome(old, oldDec, next, nextDec):
		c.Op, c.Message = Changed, decl+" changed"
		c.Why = "every policy still compiles, but " + when + " return " + c.New + " instead"
	default:
		return
	}
	d.add(c)
}

func exclusiveSets(sets [][]kind.Outcome) []exclusiveSet {
	out := make([]exclusiveSet, len(sets))
	for i, set := range sets {
		members := make([]string, len(set))
		for j, o := range set {
			members[j] = o.String()
		}
		sorted := slices.Sorted(slices.Values(members))
		out[i] = exclusiveSet{key: strings.Join(sorted, ", "), decl: "exclusive " + strings.Join(members, ", "), members: members}
	}
	return out
}

// sameOutcome reports whether two default or conflict declarations give
// the host the same outcome. oldDec and nextDec are the decisions they
// construct, in their kinds, or nil.
func sameOutcome(old *kind.Default, oldDec *kind.Decision, next *kind.Default, nextDec *kind.Decision) bool {
	if old.Decision != next.Decision || old.Reason != next.Reason {
		return false
	}
	if oldDec == nil || nextDec == nil {
		return true // only an invalid kind constructs a decision it doesn't declare
	}
	for _, f := range oldDec.Fields {
		nf := nextDec.Field(f.Name)
		if nf == nil {
			continue
		}
		if argValue(old, f) != argValue(next, nf) {
			return false
		}
	}
	return true
}

// argValue renders the value an outcome gives field f: the argument it
// passes, or else f's default. A field without either renders empty,
// which only an invalid kind has.
func argValue(o *kind.Default, f *kind.Field) string {
	if v, ok := o.Args[f.Name]; ok {
		return constant.Format(v)
	}
	if f.HasDefault {
		return constant.Format(f.Default)
	}
	return ""
}

func collectDecl(c kind.Collect) string {
	switch c {
	case kind.CollectOne:
		return "collect one"
	case kind.CollectAll:
		return "collect all"
	}
	return "no collect"
}

func collected(c kind.Collect) string {
	if c == kind.CollectAll {
		return "every candidate at the top rank"
	}
	return "one winner"
}
