package gogen

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/types"
)

// docWidth is how wide the generated doc comments wrap, "// " included.
const docWidth = 76

// goType spells t as the Go type policy.NewKind maps back to it: int64
// for `int`, float64 for `float`, a pointer for an optional, and the
// kind's own name for a struct type or an enum.
func (g *generator) goType(t types.Type) string {
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.Bool:
			return "bool"
		case types.Int:
			return "int64"
		case types.Float:
			return "float64"
		case types.Duration:
			return g.time + ".Duration"
		case types.String:
			return "string"
		case types.Timestamp:
			return g.time + ".Time"
		}
	case *types.List:
		return "[]" + g.goType(t.Elem)
	case *types.Map:
		return "map[" + g.goType(t.Key) + "]" + g.goType(t.Value)
	case *types.Optional:
		return "*" + g.goType(t.Elem)
	case *types.Enum:
		return g.exact[t.Name]
	case *types.Struct:
		return g.exact[t.Name]
	}
	return "any"
}

// printf writes formatted source.
func (g *generator) printf(format string, args ...any) {
	fmt.Fprintf(&g.b, format, args...)
}

// doc writes text as a doc comment, wrapped at docWidth. A line of text
// that starts with a tab is code, and stays on its own line.
func (g *generator) doc(text string) {
	for para := range strings.SplitSeq(text, "\n") {
		if para == "" || para[0] == '\t' {
			g.printf("//%s\n", para)
			continue
		}
		line := "//"
		for word := range strings.FieldsSeq(para) {
			if len(line)+1+len(word) > docWidth && line != "//" {
				g.printf("%s\n", line)
				line = "//"
			}
			line += " " + word
		}
		g.printf("%s\n", line)
	}
}

// file writes the whole file: header, imports, then the declarations in
// the order of a kind file, and the constructor.
func (g *generator) file() {
	k := g.k
	g.printf("%s\n//\n// Kind %s version %d", Header, k.Name, k.Version)
	if k.Accepts > 1 {
		g.printf(", accepts: %d", k.Accepts)
	}
	g.printf(".\n\npackage %s\n\n", g.name)
	if g.time != "" {
		g.printf("import (\n%s\n\n%s\n)\n", importSpec(g.time, "time"), strconv.Quote(policyPath))
	} else {
		g.printf("import %s\n", strconv.Quote(policyPath))
	}
	for _, e := range k.Enums {
		g.enum(e)
	}
	for _, t := range k.Types {
		g.structType(t)
	}
	g.inputStruct()
	for _, d := range k.Decisions {
		g.payloadStruct(d)
	}
	g.decisions()
	if g.funcs != "" {
		g.funcsStruct()
	}
	g.constructor()
}

// importSpec writes one import, named only when the name isn't the
// package's own.
func importSpec(name, path string) string {
	if path[strings.LastIndex(path, "/")+1:] == name {
		return strconv.Quote(path)
	}
	return name + " " + strconv.Quote(path)
}

// enum writes an enum as a named string type with a constant per value,
// in declaration order.
func (g *generator) enum(e *types.Enum) {
	if e == nil {
		return
	}
	name := g.exact[e.Name]
	g.printf("\n")
	g.doc(fmt.Sprintf("%s is the enum %s of kind %s:\n\n\tenum %s: %s", name, e.Name, g.k.Name, e.Name, strings.Join(e.Values, " | ")))
	g.printf("type %s string\n", name)
	g.writeAlias(e.Name, "enum")
	g.printf("\n")
	g.doc("The values of enum " + e.Name + ", in declaration order.")
	g.printf("const (\n")
	for _, v := range e.Values {
		g.printf("%s %s = %s\n", g.values[e.Name][v], name, strconv.Quote(v))
	}
	g.printf(")\n")
}

// writeAlias writes the exported alias of a struct type or enum whose
// name Go doesn't export.
func (g *generator) writeAlias(name, what string) {
	if a, ok := g.alias[name]; ok {
		g.printf("\n")
		g.doc(fmt.Sprintf("%s is %s %s, under a name other packages can use.", a, what, name))
		g.printf("type %s = %s\n", a, g.exact[name])
	}
}

// structType writes a struct type with one tagged field per field.
func (g *generator) structType(t *types.Struct) {
	name := g.exact[t.Name]
	g.printf("\n")
	g.doc(fmt.Sprintf("%s is the struct type %s of kind %s.", name, t.Name, g.k.Name))
	g.printf("type %s struct {", name)
	g.newline(len(t.Fields))
	for i, f := range t.Fields {
		g.field(g.fields[t.Name][i], f.Type, tag(f.Name, ""), f.Name+": "+f.Type.String())
	}
	g.printf("}\n")
	g.writeAlias(t.Name, "struct type")
}

// inputStruct writes the input struct, NewKind's type parameter: one
// field per input.
func (g *generator) inputStruct() {
	g.printf("\n")
	g.doc(fmt.Sprintf("%s is the input of kind %s, the struct its policies evaluate: a policy reads each field by the name in its tag.", g.input, g.k.Name))
	g.printf("type %s struct {", g.input)
	g.newline(len(g.k.Inputs))
	for i, in := range g.k.Inputs {
		g.field(g.inFlds[i], in.Type, tag(in.Name, ""), "input "+in.Name+": "+in.Type.String())
	}
	g.printf("}\n")
}

// payloadStruct writes a decision's payload struct, the type a type
// switch on a result matches it by. A field's default goes in its tag.
func (g *generator) payloadStruct(d *kind.Decision) {
	if d == nil {
		return
	}
	name := g.payload[d.Name]
	g.printf("\n")
	g.doc(fmt.Sprintf("%s is the payload of decision %s, the case of a type switch on [policy.Result.Value] that matches it.", name, d.Name))
	g.printf("type %s struct {", name)
	g.newline(len(d.Fields))
	for i, f := range d.Fields {
		option, comment := "", f.Name+": "+f.Type.String()
		if f.HasDefault {
			option = constant.Format(f.Default)
			comment += " = " + option
		}
		g.field(g.payFlds[d.Name][i], f.Type, tag(f.Name, option), comment)
	}
	g.printf("}\n")
}

// newline ends the line that opens a struct with n fields. An empty
// struct stays on one line, as gofmt writes `struct{}`.
func (g *generator) newline(n int) {
	if n > 0 {
		g.printf("\n")
	}
}

// field writes one struct field with its tag, commented with the Sigil
// declaration it stands for.
func (g *generator) field(name string, t types.Type, tag, comment string) {
	g.printf("%s %s %s // %s\n", name, g.goType(t), tag, comment)
}

// tag spells the struct tag `policy:"name"`, or `policy:"name,default=d"`
// with a default, as a Go string literal: a raw one unless the default
// holds a backquote.
func tag(name, def string) string {
	value := name
	if def != "" {
		value += ",default=" + def
	}
	t := "policy:" + strconv.Quote(value)
	if strings.Contains(t, "`") {
		return strconv.Quote(t)
	}
	return "`" + t + "`"
}

// decisions writes a variable per decision, in the kind's order, and a
// variable per reason, the handles a switch on [policy.Result.Why]
// compares against.
func (g *generator) decisions() {
	k := g.k
	order := "in precedence order, highest first"
	if k.Collect == kind.CollectAll {
		order = "in declaration order"
	}
	g.printf("\n")
	g.doc(fmt.Sprintf("The decisions of kind %s, %s.", k.Name, order))
	g.printf("var (\n")
	for _, d := range k.Decisions {
		args := make([]string, 0, 1+len(d.Reasons))
		args = append(args, strconv.Quote(d.Name))
		for _, r := range d.Reasons {
			args = append(args, strconv.Quote(r))
		}
		g.doc(fmt.Sprintf("%s is decision %s, with reason: %s.", g.decVar[d.Name], d.Name, strings.Join(d.Reasons, " | ")))
		g.printf("%s = policy.NewDecision[%s](%s)\n", g.decVar[d.Name], g.payload[d.Name], strings.Join(args, ", "))
	}
	g.printf(")\n\n")
	g.doc("The reasons of the decisions, the handles a switch on [policy.Result.Why] compares against.")
	g.printf("var (\n")
	for _, d := range k.Decisions {
		for _, r := range d.Reasons {
			g.printf("%s = %s.Reason(%s) // %s(reason: %s)\n", g.reasons[d.Name][r], g.decVar[d.Name], strconv.Quote(r), d.Name, r)
		}
	}
	g.printf(")\n")
}

// funcsStruct writes Funcs, which holds an implementation for every host
// function the kind declares.
func (g *generator) funcsStruct() {
	g.printf("\n")
	g.doc(fmt.Sprintf("%s holds the implementations of the host functions kind %s declares, which %s takes. Every field must be set.", g.funcs, g.k.Name, g.newKind))
	g.printf("type %s struct {\n", g.funcs)
	for i, f := range g.k.Funcs {
		params := make([]string, len(f.Params))
		for j, p := range f.Params {
			params[j] = g.goType(p)
		}
		g.doc(fmt.Sprintf("%s implements %s.", g.fnFlds[i], f.Signature()))
		g.printf("%s func(%s) (%s, error)\n", g.fnFlds[i], strings.Join(params, ", "), g.goType(f.Result))
	}
	g.printf("}\n")
}

// constructor writes NewKind, which builds the kind with the options
// that declare it. A kind with host functions takes Funcs, and NewKind
// panics on a nil field, as policy.NewKind panics on a kind it can't
// build: a missing implementation is a programming error, and it stops
// the program where the kind is built rather than at the first policy
// that calls the function.
func (g *generator) constructor() {
	k := g.k
	params := "opts ...policy.Option"
	text := fmt.Sprintf("%s builds kind %s, whose Schema() is the kind file this code was generated from, so it loads policies from a bundle that holds that file. Build it once.\n\n", g.newKind, k.Name)
	if g.funcs != "" {
		params = "funcs " + g.funcs + ", " + params
		text += fmt.Sprintf("funcs implements the host functions, and %s panics when a field is nil. ", g.newKind)
	}
	text += "opts follow the options that declare the kind, for host behavior such as policy.WithRecoverHostPanics; an option that declares contract makes Schema() differ from the kind file."
	g.printf("\n")
	g.doc(text)
	g.printf("func %s(%s) *policy.Kind[%s] {\n", g.newKind, params, g.input)
	if g.funcs != "" {
		g.printf("missing := \"\"\n")
		for i := range k.Funcs {
			g.printf("if funcs.%s == nil {\nmissing += \", %s.%s\"\n}\n", g.fnFlds[i], g.funcs, g.fnFlds[i])
		}
		g.printf("if missing != \"\" {\npanic(%s + missing[2:])\n}\n", strconv.Quote(g.name+"."+g.newKind+": no implementation for host functions: "))
	}
	g.printf("return policy.NewKind[%s](%s, append([]policy.Option{\n", g.input, strconv.Quote(k.Name))
	for _, opt := range g.options() {
		g.printf("%s,\n", opt)
	}
	g.printf("}, opts...)...)\n}\n")
}

// options lists the options that declare the kind, each as Go source,
// in the order a kind file declares what they stand for.
func (g *generator) options() []string {
	k := g.k
	opts := []string{fmt.Sprintf("policy.WithVersion(%d)", k.Version)}
	if k.Accepts > 1 {
		opts = append(opts, fmt.Sprintf("policy.WithAccepts(%d)", k.Accepts))
	}
	for _, e := range k.Enums {
		vals := make([]string, len(e.Values))
		for i, v := range e.Values {
			vals[i] = g.values[e.Name][v]
		}
		opts = append(opts, "policy.WithEnum("+strings.Join(vals, ", ")+")")
	}
	if k.Collect == kind.CollectAll {
		opts = append(opts, "policy.WithCollect("+g.decisionList(decisionNames(k))+")")
		if len(k.Precedence) > 0 {
			opts = append(opts, "policy.WithPrecedence("+g.decisionList(k.Precedence)+")")
		}
	} else {
		opts = append(opts, "policy.WithDecisions("+g.decisionList(decisionNames(k))+")")
	}
	for _, d := range k.Decisions {
		if len(d.Ranked) == 0 {
			continue
		}
		handles := make([]string, len(d.Ranked))
		for i, r := range d.Ranked {
			handles[i] = g.reasons[d.Name][r]
		}
		opts = append(opts, "policy.WithReasonPrecedence("+strings.Join(handles, ", ")+")")
	}
	for _, set := range k.Exclusive {
		refs := make([]string, len(set))
		for i, o := range set {
			refs[i] = g.decVar[o.Decision]
			if o.Reason != "" {
				refs[i] = g.reasons[o.Decision][o.Reason]
			}
		}
		opts = append(opts, "policy.WithExclusive("+strings.Join(refs, ", ")+")")
	}
	if k.Default != nil {
		opts = append(opts, "policy.WithDefault("+g.reasons[k.Default.Decision][k.Default.Reason]+")")
	}
	if k.Conflict != nil {
		opts = append(opts, "policy.WithConflict("+g.reasons[k.Conflict.Decision][k.Conflict.Reason]+")")
	}
	for i, f := range k.Funcs {
		opts = append(opts, fmt.Sprintf("policy.WithFunc(%s, funcs.%s)", strconv.Quote(f.Name), g.fnFlds[i]))
	}
	return opts
}

// decisionList spells the decisions called names as their variables.
func (g *generator) decisionList(names []string) string {
	vars := make([]string, len(names))
	for i, name := range names {
		vars[i] = g.decVar[name]
	}
	return strings.Join(vars, ", ")
}
