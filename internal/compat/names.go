package compat

import (
	"strings"

	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// enums compares the enums and their values. An added value is
// compatible unless it makes a bare name ambiguous: the old kind declared
// it in exactly one enum, so a policy may write it without context, and
// the new kind declares it in several. A value no enum of the old kind
// declared can't be in a policy yet, so two new enums may share it.
func (d *differ) enums() {
	for _, old := range d.old.Enums {
		next := d.next.Enum(old.Name)
		if next == nil {
			d.add(Change{
				Op: Removed, Class: Breaking, Path: "enum " + old.Name, Old: enumDecl(old),
				Message: "enum " + old.Name + " was removed",
				Why:     "policies that name " + old.Name + " or one of its values no longer compile",
			})
			continue
		}
		for _, v := range missing(old.Values, next.Values) {
			d.add(Change{
				Op: Removed, Class: Breaking, Path: "enum " + old.Name + " value " + v, Old: v,
				Message: "enum " + old.Name + " lost value `" + v + "`",
				Why:     "policies that name `" + v + "` no longer compile",
			})
		}
		for _, v := range missing(next.Values, old.Values) {
			if !d.ambiguous(next, v) {
				d.add(Change{Op: Added, Class: Compatible, Path: "enum " + old.Name + " value " + v, New: v, Message: "enum " + old.Name + " gained value `" + v + "`"})
			}
		}
		d.reorder("enum "+old.Name+" values", "enum "+old.Name+" reordered its values", " | ", old.Values, next.Values)
	}
	for _, next := range d.next.Enums {
		if d.old.Enum(next.Name) != nil {
			continue
		}
		d.add(Change{Op: Added, Class: Compatible, Path: "enum " + next.Name, New: enumDecl(next), Message: "enum " + next.Name + " was added"})
		for _, v := range next.Values {
			d.ambiguous(next, v)
		}
	}
	d.reorder("enums", "the enums were reordered", ", ", enumNames(d.old.Enums), enumNames(d.next.Enums))
}

// ambiguous records the ambiguity e's value v brings, when the old kind
// declared v in exactly one other enum and the new kind declares it in
// several, and reports whether it did.
func (d *differ) ambiguous(e *types.Enum, v string) bool {
	was := d.old.EnumsWith(v)
	if len(was) != 1 || was[0].Name == e.Name {
		return false
	}
	now := d.next.EnumsWith(v)
	if len(now) < 2 {
		return false
	}
	var others []string
	for _, o := range now {
		if o.Name != e.Name {
			others = append(others, o.Name)
		}
	}
	c := Change{
		Op: Ambiguous, Class: Breaking, Path: "enum " + e.Name + " value " + v, New: v,
		Message: "enum " + e.Name + " declares `" + v + "`, which " + list(others) + " declares too",
		Why:     "a bare `" + v + "` without context becomes ambiguous",
	}
	if owner := d.next.Enum(was[0].Name); owner != nil && owner.Has(v) {
		c.Fix = "and qualify it as `" + owner.Name + "." + v + "`"
	}
	d.add(c)
	return true
}

// types compares the struct types and their fields.
func (d *differ) types() {
	for _, old := range d.old.Types {
		next := d.next.Type(old.Name)
		if next == nil {
			d.add(Change{
				Op: Removed, Class: Breaking, Path: "type " + old.Name, Old: "type " + old.Name,
				Message: "type " + old.Name + " was removed",
				Why:     "policies that read a value of type " + old.Name + " no longer compile",
			})
			continue
		}
		for _, f := range old.Fields {
			nf := next.Field(f.Name)
			path := "type " + old.Name + " field " + f.Name
			switch {
			case nf == nil:
				d.add(Change{
					Op: Removed, Class: Breaking, Path: path, Old: fieldDecl(f),
					Message: "type " + old.Name + " lost field `" + f.Name + "`",
					Why:     "policies that read `." + f.Name + "` of a " + old.Name + " no longer compile",
				})
			case f.Type.String() != nf.Type.String():
				d.add(Change{
					Op: Changed, Class: Breaking, Path: path, Old: fieldDecl(f), New: fieldDecl(nf),
					Message: "type " + old.Name + " field `" + f.Name + "` changed type from " + f.Type.String() + " to " + nf.Type.String(),
					Why:     retyped("`."+f.Name+"`", f.Type, nf.Type),
				})
			}
		}
		for _, f := range next.Fields {
			if old.Field(f.Name) == nil {
				d.add(Change{Op: Added, Class: Compatible, Path: "type " + old.Name + " field " + f.Name, New: fieldDecl(f), Message: "type " + old.Name + " gained field `" + f.Name + "`"})
			}
		}
		d.reorder("type "+old.Name+" fields", "type "+old.Name+" reordered its fields", ", ", fieldNames(old.Fields), fieldNames(next.Fields))
	}
	for _, next := range d.next.Types {
		if d.old.Type(next.Name) == nil {
			d.add(Change{Op: Added, Class: Compatible, Path: "type " + next.Name, New: "type " + next.Name, Message: "type " + next.Name + " was added"})
		}
	}
	d.reorder("types", "the struct types were reordered", ", ", structNames(d.old.Types), structNames(d.next.Types))
}

// inputs compares the inputs.
func (d *differ) inputs() {
	for _, old := range d.old.Inputs {
		next := d.next.Input(old.Name)
		path := "input " + old.Name
		switch {
		case next == nil:
			d.add(Change{
				Op: Removed, Class: Breaking, Path: path, Old: inputDecl(old),
				Message: "input " + old.Name + " was removed",
				Why:     "policies that read `" + old.Name + "` no longer compile",
			})
		case old.Type.String() != next.Type.String():
			d.add(Change{
				Op: Changed, Class: Breaking, Path: path, Old: inputDecl(old), New: inputDecl(next),
				Message: "input " + old.Name + " changed type from " + old.Type.String() + " to " + next.Type.String(),
				Why:     retyped("`"+old.Name+"`", old.Type, next.Type),
			})
		}
	}
	for _, next := range d.next.Inputs {
		if d.old.Input(next.Name) == nil {
			d.add(Change{Op: Added, Class: Compatible, Path: "input " + next.Name, New: inputDecl(next), Message: "input " + next.Name + " was added"})
		}
	}
	names := func(ins []*kind.Input) []string {
		out := make([]string, len(ins))
		for i, in := range ins {
			out[i] = in.Name
		}
		return out
	}
	d.reorder("inputs", "the inputs were reordered", ", ", names(d.old.Inputs), names(d.next.Inputs))
}

// funcs compares the host functions' signatures.
func (d *differ) funcs() {
	for _, old := range d.old.Funcs {
		next := d.next.Func(old.Name)
		path := "fn " + old.Name
		switch {
		case next == nil:
			d.add(Change{
				Op: Removed, Class: Breaking, Path: path, Old: old.Signature(),
				Message: "function " + old.Name + " was removed",
				Why:     "policies that call " + old.Name + "() no longer compile",
			})
		case old.Signature() != next.Signature():
			d.add(Change{
				Op: Changed, Class: Breaking, Path: path, Old: old.Signature(), New: next.Signature(),
				Message: "function " + old.Name + " changed its signature",
				Why:     "calls to " + old.Name + "() stop type-checking",
			})
		}
	}
	for _, next := range d.next.Funcs {
		if d.old.Func(next.Name) == nil {
			d.add(Change{Op: Added, Class: Compatible, Path: "fn " + next.Name, New: next.Signature(), Message: "function " + next.Name + " was added"})
		}
	}
	names := func(fns []*kind.Func) []string {
		out := make([]string, len(fns))
		for i, f := range fns {
			out[i] = f.Name
		}
		return out
	}
	d.reorder("functions", "the host functions were reordered", ", ", names(d.old.Funcs), names(d.next.Funcs))
}

// retyped says what a type change does to policies that use what, which
// has the type old and now next. A string that became an enum gets its
// own advice, since it's the usual case and has a mechanical fix.
func retyped(what string, old, next types.Type) string {
	if e, ok := next.(*types.Enum); ok && old == types.String {
		return "policies that compare " + what + " with a string no longer compile; write the bare value of " + e.Name + " instead of the string"
	}
	return "expressions that use " + what + " as a " + old.String() + " stop type-checking"
}

// enumDecl renders an enum as a kind file declares it.
func enumDecl(e *types.Enum) string {
	return "enum " + e.Name + ": " + strings.Join(e.Values, " | ")
}

// fieldDecl renders a struct field as a kind file declares it.
func fieldDecl(f *types.Field) string {
	return f.Name + ": " + f.Type.String()
}

// inputDecl renders an input as a kind file declares it.
func inputDecl(in *kind.Input) string {
	return "input " + in.Name + ": " + in.Type.String()
}

func enumNames(es []*types.Enum) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func structNames(ts []*types.Struct) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func fieldNames(fs []*types.Field) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Name
	}
	return out
}
