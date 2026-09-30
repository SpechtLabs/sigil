package kind

import (
	"sort"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
)

// Source renders the kind as a kind file in canonical form: what a host
// exports with Schema(), and what a loaded kind file prints back as. The
// round trip is the property the exporter is tested by, and two kinds
// with the same Source are the same contract, which is how a stale
// exported kind file is detected. The layout is the one `sigil fmt`
// writes: a blank line after the header, around every type and decision
// and before the default, with the conflict outcome on the line after it,
// and enums, inputs, functions and the collect, precedence and exclusive
// lines each grouped together. Enums come first, each on one line, since
// struct fields refer to them.
func (k *Kind) Source() string {
	var b strings.Builder
	b.WriteString("kind " + k.Name + " version " + strconv.Itoa(k.Version))
	if k.Accepts > 1 {
		b.WriteString(", accepts: " + strconv.Itoa(k.Accepts))
	}
	b.WriteString("\n")

	if len(k.Enums) > 0 {
		b.WriteString("\n")
	}
	for _, e := range k.Enums {
		b.WriteString("enum " + e.Name + ": " + strings.Join(e.Values, " | ") + "\n")
	}

	for _, t := range k.Types {
		b.WriteString("\ntype " + t.Name + " {")
		if len(t.Fields) > 0 {
			b.WriteString("\n")
		}
		for _, f := range t.Fields {
			b.WriteString("  " + f.Name + ": " + f.Type.String() + "\n")
		}
		b.WriteString("}\n")
	}

	if len(k.Inputs) > 0 {
		b.WriteString("\n")
	}
	for _, in := range k.Inputs {
		b.WriteString("input " + in.Name + ": " + in.Type.String() + "\n")
	}

	if len(k.Funcs) > 0 {
		b.WriteString("\n")
	}
	for _, f := range k.Funcs {
		b.WriteString(f.Signature() + "\n")
	}

	for _, d := range k.Decisions {
		b.WriteString("\n" + d.Source())
	}

	k.writeResolution(&b)
	if k.Default != nil {
		b.WriteString("\ndefault " + k.Default.Call(k.Decision(k.Default.Decision)) + "\n")
	}
	if k.Conflict != nil {
		if k.Default == nil {
			b.WriteString("\n") // only an invalid kind, which Validate reports, has no default to follow
		}
		b.WriteString("conflict " + k.Conflict.Call(k.Decision(k.Conflict.Decision)) + "\n")
	}
	return b.String()
}

// Call renders the constructor call of a default or conflict declaration,
// like `deny(reason: no_rule_matched)`, without the keyword before it. The
// reason comes first. decl is the decision it constructs, or nil. When
// given, it orders the other arguments as its fields are declared;
// arguments it doesn't declare, and all of them without it, follow sorted
// by name.
func (d *Default) Call(decl *Decision) string {
	names := make([]string, 0, len(d.Args))
	if decl != nil {
		for _, f := range decl.Fields {
			if _, ok := d.Args[f.Name]; ok {
				names = append(names, f.Name)
			}
		}
	}
	extra := make([]string, 0, len(d.Args))
	for name := range d.Args {
		if decl == nil || decl.Field(name) == nil {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	names = append(names, extra...)
	args := make([]string, 0, 1+len(names))
	args = append(args, "reason: "+d.Reason)
	for _, name := range names {
		args = append(args, name+": "+constant.Format(d.Args[name]))
	}
	return d.Decision + "(" + strings.Join(args, ", ") + ")"
}

// writeResolution writes the lines that say how candidates resolve to b:
// `collect`, the decision `precedence`, each scoped `precedence` and each
// `exclusive` set, one per line, after a blank line. A kind that declares
// none of them writes nothing.
func (k *Kind) writeResolution(b *strings.Builder) {
	first := true
	line := func(s string) {
		if first {
			b.WriteString("\n")
			first = false
		}
		b.WriteString(s)
		b.WriteString("\n")
	}
	switch k.Collect {
	case CollectOne:
		line("collect one")
	case CollectAll:
		line("collect all")
	}
	if len(k.Precedence) > 0 {
		line("precedence " + strings.Join(k.Precedence, " > "))
	}
	for _, d := range k.Decisions {
		if len(d.Ranked) > 0 {
			line("precedence " + d.Name + ": " + strings.Join(d.Ranked, " > "))
		}
	}
	for _, set := range k.Exclusive {
		names := make([]string, len(set))
		for i, o := range set {
			names[i] = o.String()
		}
		line("exclusive " + strings.Join(names, ", "))
	}
}
