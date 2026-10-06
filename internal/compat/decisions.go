package compat

import (
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
)

// decisions compares the decisions, their reasons and their payload
// fields.
func (d *differ) decisions() {
	for _, old := range d.old.Decisions {
		next := d.next.Decision(old.Name)
		if next == nil {
			d.add(Change{
				Op: Removed, Class: Breaking, Path: "decision " + old.Name, Old: "decision " + old.Name,
				Message: "decision " + old.Name + " was removed",
				Why:     "policies that construct " + old.Name + "(...) or read outcome." + old.Name + " no longer compile",
			})
			continue
		}
		d.reasons(old, next)
		d.payload(old, next)
	}
	for _, next := range d.next.Decisions {
		if d.old.Decision(next.Name) == nil {
			d.add(Change{Op: Added, Class: Compatible, Path: "decision " + next.Name, New: "decision " + next.Name, Message: "decision " + next.Name + " was added"})
		}
	}
	names := func(ds []*kind.Decision) []string {
		out := make([]string, len(ds))
		for i, dec := range ds {
			out[i] = dec.Name
		}
		return out
	}
	d.reorder("decisions", "the decisions were reordered", ", ", names(d.old.Decisions), names(d.next.Decisions))
}

// reasons compares the reasons of a decision both kinds declare.
func (d *differ) reasons(old, next *kind.Decision) {
	for _, r := range missing(old.Reasons, next.Reasons) {
		d.add(Change{
			Op: Removed, Class: Breaking, Path: "decision " + old.Name + " reason " + r, Old: r,
			Message: "decision " + old.Name + " lost reason `" + r + "`",
			Why:     "policies that construct " + old.Name + "(reason: " + r + ") no longer compile",
		})
	}
	for _, r := range missing(next.Reasons, old.Reasons) {
		d.add(Change{Op: Added, Class: Compatible, Path: "decision " + old.Name + " reason " + r, New: r, Message: "decision " + old.Name + " gained reason `" + r + "`"})
	}
	d.reorder("decision "+old.Name+" reasons", "decision "+old.Name+" reordered its reasons", " | ", old.Reasons, next.Reasons)
}

// payload compares the payload fields of a decision both kinds declare.
// A field's default matters to every constructor that leaves it out:
// dropping it breaks them, adding one is compatible, and changing it
// changes what they decide.
func (d *differ) payload(old, next *kind.Decision) {
	prefix := "decision " + old.Name + " field "
	for _, f := range old.Fields {
		nf := next.Field(f.Name)
		c := Change{Op: Changed, Path: prefix + f.Name, Old: payloadDecl(f)}
		switch {
		case nf == nil:
			c.Op, c.Class = Removed, Breaking
			c.Message = "decision " + old.Name + " lost field `" + f.Name + "`"
			c.Why = "policies that pass `" + f.Name + ":` to " + old.Name + "(...) or read it from outcome." + old.Name + " no longer compile"
		case f.Type.String() != nf.Type.String():
			c.Class = Breaking
			c.Message = "decision " + old.Name + " field `" + f.Name + "` changed type from " + f.Type.String() + " to " + nf.Type.String()
			c.Why = "constructors that pass `" + f.Name + ":` stop type-checking"
		case f.HasDefault && !nf.HasDefault:
			c.Class = Breaking
			c.Message = "decision " + old.Name + " field `" + f.Name + "` lost its default"
			c.Why = "every " + old.Name + "(...) that leaves out `" + f.Name + ":` no longer compiles"
			c.Fix = "or keep the default"
		case !f.HasDefault && nf.HasDefault:
			c.Class = Compatible
			c.Message = "decision " + old.Name + " field `" + f.Name + "` gained a default"
		case f.HasDefault && constant.Format(f.Default) != constant.Format(nf.Default):
			c.Class = Behavior
			c.Message = "decision " + old.Name + " field `" + f.Name + "` changed its default from " + constant.Format(f.Default) + " to " + constant.Format(nf.Default)
			c.Why = "every policy still compiles, but every " + old.Name + "(...) that leaves out `" + f.Name + ":` gets " + constant.Format(nf.Default) + " instead"
			c.Fix = behaviorFix
		default:
			continue
		}
		if nf != nil {
			c.New = payloadDecl(nf)
		}
		d.add(c)
	}
	for _, f := range next.Fields {
		if old.Field(f.Name) != nil {
			continue
		}
		c := Change{Op: Added, Class: Compatible, Path: prefix + f.Name, New: payloadDecl(f), Message: "decision " + old.Name + " gained field `" + f.Name + "` with a default"}
		if !f.HasDefault {
			c.Class = Breaking
			c.Message = "decision " + old.Name + " gained field `" + f.Name + "` without a default"
			c.Why = "every " + old.Name + "(...) that doesn't pass `" + f.Name + ":` no longer compiles"
			c.Fix = "or give `" + f.Name + "` a default"
		}
		d.add(c)
	}
	names := func(fs []*kind.Field) []string {
		out := make([]string, len(fs))
		for i, f := range fs {
			out[i] = f.Name
		}
		return out
	}
	d.reorder("decision "+old.Name+" fields", "decision "+old.Name+" reordered its fields", ", ", names(old.Fields), names(next.Fields))
}

// payloadDecl renders a payload field as a kind file declares it.
func payloadDecl(f *kind.Field) string {
	s := f.Name + ": " + f.Type.String()
	if f.HasDefault {
		s += " = " + constant.Format(f.Default)
	}
	return s
}
