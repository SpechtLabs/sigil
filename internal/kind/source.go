package kind

import (
	"sort"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
)

// Source renders the kind as a kind file in canonical form: what a host
// exports with Schema(), and what a loaded kind file prints back as. The
// round trip is the property the exporter is tested by.
func (k *Kind) Source() string {
	var b strings.Builder
	b.WriteString("kind " + k.Name + " version " + strconv.Itoa(k.Version) + "\n")

	if len(k.Types) > 0 {
		b.WriteString("\n")
	}
	for _, t := range k.Types {
		b.WriteString("type " + t.Name + " {\n")
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

	if len(k.Decisions) > 0 {
		b.WriteString("\n")
	}
	for _, d := range k.Decisions {
		b.WriteString(d.Signature() + "\n")
	}

	b.WriteString("\n")
	if k.Collect {
		b.WriteString("collect all\n")
	} else {
		b.WriteString("precedence " + strings.Join(k.Precedence, " > ") + "\n")
	}
	if k.Default != nil {
		b.WriteString(k.Default.Source(k.Decision(k.Default.Decision)) + "\n")
	}
	return b.String()
}

// Source renders the default declaration. The decision, when known,
// orders the arguments as its fields are declared.
func (d *Default) Source(decl *Decision) string {
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
	args = append(args, strconv.Quote(d.Reason))
	for _, name := range names {
		args = append(args, name+": "+constant.Format(d.Args[name]))
	}
	return "default " + d.Decision + "(" + strings.Join(args, ", ") + ")"
}
