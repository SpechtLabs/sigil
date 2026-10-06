package lsp

import (
	"slices"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// reasonArg is the name of a constructor's reason argument.
const reasonArg = "reason"

// The sort keys: what the context expects comes first, then names, then
// keywords.
const (
	sortExpected = "0"
	sortName     = "1"
	sortKeyword  = "2"
)

// expressionKeywords are the keywords that start an operand.
var expressionKeywords = []string{"any", "all", "filter", "not", "present", "true", "false"}

// operators are the keyword operators that follow an operand.
var operators = []string{"and", "or", "xor", "in", "not in", "has", "like", "matches", "all in", "any in", "one in", "exclusive in"}

// item is one completion, before the server turns it into the
// protocol's.
type item struct {
	label  string
	detail string // a one-line description, such as a type or a signature
	doc    string // markdown shown next to the item
	insert string // the text the item inserts; the label when empty
	sort   string // sorts before items with a greater key; the label sorts within one
	kind   protocol.CompletionItemKind
}

// complete returns the completions at offset, sorted, and the offset where
// the text they replace starts; it ends at offset.
func (v *view) complete(offset int) ([]item, int) {
	c := scan(v.src, offset)
	from, typed := c.start, c.prefix
	var items []item
	switch c.place {
	case atDocStart:
		items = keywordItems("policy", "module")
	case atHeaderKind:
		items = v.kindItems()
	case atHeaderPin:
		if k := v.kindNamed(c.path); k != nil {
			items = []item{{label: strconv.Itoa(k.Model.Version), kind: protocol.CompletionValue, detail: "the current version of " + k.Model.Name}}
		}
	case atStatement:
		items = v.statementItems(c)
	case atUsePath:
		items, from, typed = v.usePathItems(c), c.from, c.path
	case atUseItem:
		items = v.useItems(c)
	case atType:
		items = v.typeItems(c)
	case atBound:
		items = []item{{label: "min", insert: "min: ", kind: protocol.CompletionKeyword}, {label: "max", insert: "max: ", kind: protocol.CompletionKeyword}}
	case atArgName:
		items = v.argItems(c)
	case atReason:
		items = v.reasonItems(c)
	case atExpr:
		items = v.operandItems(c)
	case atMember:
		items = v.memberItems(c)
	case atOperator:
		items = keywordItems(operators...)
	}
	items = slices.DeleteFunc(items, func(it item) bool { return !strings.HasPrefix(it.label, typed) })
	slices.SortStableFunc(items, func(a, b item) int {
		if n := strings.Compare(a.sort, b.sort); n != 0 {
			return n
		}
		return strings.Compare(a.label, b.label)
	})
	return slices.CompactFunc(items, func(a, b item) bool { return a.label == b.label && a.sort == b.sort }), from
}

// keywordItems returns keywords as completions.
func keywordItems(words ...string) []item {
	out := make([]item, len(words))
	for i, w := range words {
		out[i] = item{label: w, kind: protocol.CompletionKeyword, sort: sortKeyword}
	}
	return out
}

// kindItems completes the kind a header names: every kind the project
// knows.
func (v *view) kindItems() []item {
	if v.proj == nil {
		return nil
	}
	var out []item
	for _, k := range v.proj.Kinds() {
		out = append(out, item{label: k.Model.Name, kind: protocol.CompletionInterface, detail: kindHeader(k.Model)})
	}
	return out
}

// env returns the names visible at the cursor: the checker's scope, with
// the variables of the quantifiers and filters around the cursor added,
// and `outcome` readable in an assert. It returns nil when the document's
// kind isn't known.
func (v *view) env(c *cursor) *check.Env {
	var env *check.Env
	if c.header >= 0 {
		env = v.scopeOf(v.docStarting(c.toks[c.header].Pos.Offset), c.kind, c.start)
	}
	if env == nil {
		return nil
	}
	if c.assert && !env.InAssert {
		env = env.Child()
		env.InAssert = true
	}
	for _, b := range c.binders {
		if _, taken := env.Lookup(b.name); taken || b.colon <= b.from {
			continue
		}
		l, ok := v.typeOf(c.text(b.from, b.colon-1), env).(*types.List)
		if !ok {
			continue
		}
		env = env.Child()
		entity := check.QuantVar
		if b.filter {
			entity = check.FilterVar
		}
		env.Declare(b.name, check.Binding{Entity: entity, Type: l.Elem})
	}
	return env
}

// text returns the source of the tokens from index first to last.
func (c *cursor) text(first, last int) []byte {
	return c.src[c.toks[first].Pos.Offset:c.toks[last].End.Offset]
}

// statementItems completes the start of a statement: the keywords that
// can start one there, and in a `when` body the decisions to construct,
// and the imported policies to invoke.
func (v *view) statementItems(c *cursor) []item {
	var out []item
	switch {
	case c.module:
		out = keywordItems("use", "let", "pub let")
	case c.inBody:
		out = keywordItems("let", "when", "assert")
	default:
		out = keywordItems("use", "param", "let", "pub let", "when", "assert")
	}
	env := v.env(c)
	if env == nil || c.module {
		return out
	}
	if c.inBody {
		for _, d := range env.Kind().Decisions {
			out = append(out, decisionItem(d))
		}
	}
	for _, name := range names(env) {
		if b, _ := env.Lookup(name); b.Entity == check.Invocable {
			out = append(out, item{label: name, kind: protocol.CompletionModule, detail: "policy " + b.Doc.Name, doc: code(paramsOf(b.Doc)), sort: sortName})
		}
	}
	return out
}

// decisionItem is a decision to construct.
func decisionItem(d *kind.Decision) item {
	return item{label: d.Name, kind: protocol.CompletionConstructor, detail: d.Signature(), doc: code(strings.TrimSuffix(d.Source(), "\n")), sort: sortName}
}

// usePathItems completes the dotted name after `use`: every policy and
// module of the document's kind but the document itself.
func (v *view) usePathItems(c *cursor) []item {
	g := v.groupOf(c.kind)
	if g == nil || c.header < 0 {
		return nil
	}
	self := v.docStarting(c.toks[c.header].Pos.Offset)
	var out []item
	for _, d := range g.Bundle.All() {
		if self != nil && d.Name == self.Name {
			continue
		}
		out = append(out, item{label: d.Name, kind: protocol.CompletionModule, detail: describeDoc(d.Node), sort: sortName})
	}
	return out
}

// useItems completes a name in `use path.{`: the pub lets of the document
// the path names that the import doesn't list yet.
func (v *view) useItems(c *cursor) []item {
	d := v.document(c.path)
	if d == nil || d.Exported == nil {
		return nil
	}
	var out []item
	for _, name := range d.Exported.LetNames() {
		if !slices.Contains(c.given, name) {
			out = append(out, item{label: name, kind: protocol.CompletionVariable, detail: d.Exported.Lets[name].String(), sort: sortName})
		}
	}
	return out
}

// typeItems completes a param's type: the scalars, list and map, and the
// kind's struct types and enums.
func (v *view) typeItems(c *cursor) []item {
	var out []item
	for _, name := range types.ScalarNames() {
		out = append(out, item{label: name, kind: protocol.CompletionStruct, sort: sortName})
	}
	out = append(out, item{label: "list", kind: protocol.CompletionStruct, sort: sortName}, item{label: "map", kind: protocol.CompletionStruct, sort: sortName})
	if k := v.kindNamed(c.kind); k != nil {
		for _, s := range k.Model.Types {
			out = append(out, item{label: s.Name, kind: protocol.CompletionStruct, doc: code(structSource(s)), sort: sortName})
		}
		for _, e := range k.Model.Enums {
			out = append(out, item{label: e.Name, kind: protocol.CompletionEnum, doc: code(enumSource(e)), sort: sortName})
		}
	}
	return out
}

// argItems completes an argument's name: a constructor's reason and the
// payload fields it doesn't give yet, those without a default first, or
// an invocation's params, those without a default first.
func (v *view) argItems(c *cursor) []item {
	env := v.env(c)
	if env == nil {
		return nil
	}
	b, _ := env.Lookup(c.call)
	var out []item
	switch b.Entity {
	case check.DecisionName:
		d := env.Kind().Decision(c.call)
		if !slices.Contains(c.args, reasonArg) {
			out = append(out, item{label: reasonArg, insert: reasonArg + ": ", kind: protocol.CompletionField, detail: reasonArg + ": " + strings.Join(d.Reasons, " | "), sort: sortExpected})
		}
		for _, f := range d.Fields {
			if slices.Contains(c.args, f.Name) {
				continue
			}
			it := item{label: f.Name, insert: f.Name + ": ", kind: protocol.CompletionField, detail: fieldSource(f), sort: sortExpected}
			if f.HasDefault {
				it.sort = sortName
			}
			out = append(out, it)
		}
	case check.Invocable:
		for _, p := range b.Doc.Params {
			if slices.Contains(c.args, p.Name) {
				continue
			}
			it := item{label: p.Name, insert: p.Name + ": ", kind: protocol.CompletionField, detail: paramSource(p), sort: sortExpected}
			if !p.Required {
				it.sort = sortName
			}
			out = append(out, it)
		}
	}
	return out
}

// reasonItems completes a constructor's reason: the reasons its decision
// declares.
func (v *view) reasonItems(c *cursor) []item {
	k := v.kindNamed(c.kind)
	if k == nil {
		return nil
	}
	d := k.Model.Decision(c.call)
	if d == nil {
		return nil
	}
	out := make([]item, len(d.Reasons))
	for i, r := range d.Reasons {
		out[i] = item{label: r, kind: protocol.CompletionEnumMember, detail: "reason of " + d.Name, sort: sortExpected}
	}
	return out
}

// operandItems completes an operand: every name in scope that's a value,
// the keywords that start an operand, and `outcome` in an assert. The
// values of the enum the context expects, such as a payload field's, come
// first.
func (v *view) operandItems(c *cursor) []item {
	out := keywordItems(expressionKeywords...)
	env := v.env(c)
	if env == nil {
		return out
	}
	if env.InAssert {
		out = append(out, item{label: "outcome", kind: protocol.CompletionKeyword, detail: "list<decision>", sort: sortKeyword})
	}
	expected := v.expected(c, env)
	for _, name := range names(env) {
		b, _ := env.Lookup(name)
		out = append(out, valueItems(name, b, env, expected)...)
	}
	return out
}

// expected returns the enum the operand at the cursor takes, or nil: the
// enum of the operand it's compared with, or of the argument it's the
// value of.
func (v *view) expected(c *cursor, env *check.Env) *types.Enum {
	var t types.Type
	if c.left[0] >= 0 {
		t = v.typeOf(c.text(c.left[0], c.left[1]), env)
	}
	b, _ := env.Lookup(c.call)
	switch {
	case c.call == "" || c.arg == "":
	case b.Entity == check.DecisionName:
		if f := env.Kind().Decision(c.call).Field(c.arg); f != nil {
			t = f.Type
		}
	case b.Entity == check.Invocable:
		if p := b.Doc.Param(c.arg); p != nil {
			t = p.Type
		}
	}
	if opt, ok := t.(*types.Optional); ok {
		t = opt.Elem
	}
	e, _ := t.(*types.Enum)
	return e
}

// valueItems returns the completions a name in scope offers as an
// operand: itself when it's a value, its pub lets' qualifier when it's a
// module, and for an enum value several enums declare, the qualified value
// of each. Imported policies aren't values, and decisions are only in an
// assert.
func valueItems(name string, b check.Binding, env *check.Env, expected *types.Enum) []item {
	it := item{label: name, sort: sortName}
	switch b.Entity {
	case check.Input:
		it.kind, it.detail = protocol.CompletionVariable, b.Type.String()
	case check.Let:
		it.kind, it.detail = protocol.CompletionVariable, typeName(b.Type)
		if b.Doc != nil {
			it.doc = "from " + b.Doc.Name
		}
	case check.Param:
		it.kind, it.detail = protocol.CompletionConstant, typeName(b.Type)
	case check.QuantVar, check.FilterVar:
		it.kind, it.detail = protocol.CompletionVariable, typeName(b.Type)
	case check.Function:
		it.kind, it.detail = protocol.CompletionFunction, b.Func.Signature()
	case check.Module:
		it.kind, it.detail = protocol.CompletionModule, "module "+b.Doc.Name
	case check.EnumType:
		it.kind, it.detail = protocol.CompletionEnum, enumSource(b.Type.(*types.Enum))
	case check.DecisionName:
		if !env.InAssert {
			return nil
		}
		it.kind, it.detail = protocol.CompletionValue, "decision"
	case check.EnumValue:
		if b.Type == nil {
			var out []item
			for _, e := range env.Kind().EnumsWith(name) {
				out = append(out, enumValueItem(e.Name+"."+name, e, expected))
			}
			return out
		}
		return []item{enumValueItem(name, b.Type.(*types.Enum), expected)}
	default:
		return nil
	}
	return []item{it}
}

// enumValueItem is a value of e, sorted first when e is the enum the
// context expects.
func enumValueItem(label string, e *types.Enum, expected *types.Enum) item {
	it := item{label: label, kind: protocol.CompletionEnumMember, detail: e.Name, sort: sortName}
	if expected != nil && expected.Name == e.Name {
		it.sort = sortExpected
	}
	return it
}

// memberItems completes the name after `.` or `?.`: a struct's fields, a
// candidate's, the reasons of a list of candidates, the decisions after
// `outcome`, an enum's values, a module's pub lets, or a decision's
// reasons.
func (v *view) memberItems(c *cursor) []item {
	env := v.env(c)
	if env == nil {
		return nil
	}
	first, last := c.recv[0], c.recv[1]
	if first == last {
		if out, ok := qualifiedItems(c.toks[first], env); ok {
			return out
		}
	}
	return fieldItems(v.typeOf(c.text(first, last), env), env.Kind())
}

// qualifiedItems returns what a single name qualifies after a `.`: the
// decisions after `outcome`, an enum's values, a module's or an imported
// policy's pub lets, or a decision's reasons. It reports false for a name
// that's a value, whose fields follow its type.
func qualifiedItems(t token.Token, env *check.Env) ([]item, bool) {
	var out []item
	if t.Kind == token.KwOutcome {
		for _, d := range env.Kind().Decisions {
			out = append(out, item{label: d.Name, kind: protocol.CompletionField, detail: "list<" + d.Name + " candidate>", sort: sortName})
		}
		return out, true
	}
	b, _ := env.Lookup(t.Text)
	switch b.Entity {
	case check.EnumType:
		e := b.Type.(*types.Enum)
		for _, val := range e.Values {
			out = append(out, item{label: val, kind: protocol.CompletionEnumMember, detail: e.Name, sort: sortName})
		}
	case check.Module, check.Invocable:
		for _, name := range b.Doc.LetNames() {
			out = append(out, item{label: name, kind: protocol.CompletionVariable, detail: typeName(b.Doc.Lets[name]), sort: sortName})
		}
	case check.DecisionName:
		d := env.Kind().Decision(t.Text)
		for _, r := range d.Reasons {
			out = append(out, item{label: r, kind: protocol.CompletionEnumMember, detail: "reason of " + d.Name, sort: sortName})
		}
	default:
		return nil, false
	}
	return out, true
}

// fieldItems returns the fields a value of type t has: a struct's,
// inside an optional too, a candidate's, or for a list of candidates the
// reasons that narrow it.
func fieldItems(t types.Type, k *kind.Kind) []item {
	if opt, ok := t.(*types.Optional); ok {
		t = opt.Elem
	}
	var fields []*types.Field
	switch t := t.(type) {
	case *types.Struct:
		if decl := k.Type(t.Name); decl != nil {
			t = decl
		}
		fields = t.Fields
	case *types.Candidate:
		fields = t.Fields
	case *types.List:
		cand, ok := t.Elem.(*types.Candidate)
		if !ok {
			return nil
		}
		d := k.Decision(cand.Decision)
		if d == nil {
			return nil
		}
		out := make([]item, len(d.Reasons))
		for i, r := range d.Reasons {
			out[i] = item{label: r, kind: protocol.CompletionEnumMember, detail: "the " + d.Name + " candidates with reason " + r, sort: sortName}
		}
		return out
	}
	out := make([]item, len(fields))
	for i, f := range fields {
		out[i] = item{label: f.Name, kind: protocol.CompletionField, detail: f.Type.String(), sort: sortName}
	}
	return out
}

// names returns every name in env once, sorted.
func names(env *check.Env) []string {
	return slices.Compact(env.Names())
}

// typeName names a type for a detail, or "" for one not worked out.
func typeName(t types.Type) string {
	if t == nil || t == types.Invalid {
		return ""
	}
	return t.String()
}

// fieldSource renders a payload field as a kind file declares it.
func fieldSource(f *kind.Field) string {
	s := f.Name + ": " + f.Type.String()
	if f.HasDefault && f.Default != nil {
		s += " = " + constant.Format(f.Default)
	}
	return s
}
