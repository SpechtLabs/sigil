package lsp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/lsp/protocol"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// reasonArg is the name of a constructor's reason argument.
const reasonArg = "reason"

// keywordClass and keywordDepth sort keywords after the names of their
// rank, however far out those are bound.
const (
	keywordClass = 9
	keywordDepth = 99
)

// The keywords completion offers by name, and what it calls a decision.
const (
	kwLet      = "let"
	kwPubLet   = "pub let"
	kwUse      = "use"
	kwParam    = "param"
	kwWhen     = "when"
	kwAssert   = "assert"
	kwDecision = "decision"
	kwTrue     = "true"
	kwFalse    = "false"
)

// durationUnits are the units of a duration literal, as the lexer reads
// them, largest first.
var durationUnits = []string{"d", "h", "m", "s", "ms"}

// item is one completion, before the server turns it into the
// protocol's.
type item struct {
	typ     types.Type // the type of the value it completes, for ranking; nil for none
	label   string
	detail  string // a one-line description, such as a type or a signature
	desc    string // what the item is, such as "input" or "from deploy.common", next to the label
	doc     string // markdown shown next to the item
	insert  string // the text the item inserts; the label when empty
	snippet string // the snippet it inserts instead, for a client that takes snippets
	kind    protocol.CompletionItemKind
	rank    int  // how well it fits: rankExpected first
	depth   int  // how many scopes out its name is bound, nearest first
	class   int  // sorts items of one rank and depth: names the document declares, then inputs, then the rest, keywords last
	best    bool // the item to preselect
}

// operatorsByType are the operators that follow an operand of each kind
// of type, in the order they're offered; any are the ones for an
// operand whose type isn't known.
var (
	boolOperators    = []string{opAnd, opOr, opXor, "==", "!="}
	stringOperators  = []string{opLike, opMatches, opIn, opNotIn, "==", "!="}
	orderedOperators = []string{"<", "<=", ">", ">=", "==", "!=", opIn, opNotIn}
	numberOperators  = []string{"+", "-"}
	enumOperators    = []string{"==", "!=", opIn, opNotIn}
	listOperators    = []string{opAnyIn, opAllIn, opOneIn, opExclusiveIn}
	anyOperators     = []string{opAnd, opOr, opXor, opIn, opNotIn, opHas, opLike, opMatches, opAllIn, opAnyIn, opOneIn, opExclusiveIn}
)

// complete returns the completions at offset, sorted, and the span of the
// text they replace: the whole name the cursor is in, the part after the
// cursor included, so accepting `teams` at `actor.te|ams` gives
// `actor.teams`, or after `use` the whole dotted name, or a number and
// the cursor for a duration unit.
func (v *view) complete(offset int) ([]item, int, int) {
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
			items = []item{{label: strconv.Itoa(k.Model.Version), kind: protocol.CompletionValue, detail: "the current version of " + k.Model.Name, rank: rankExpected}}
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
		if units, start := v.unitItems(c); units != nil {
			return units, start, offset
		}
		if last := c.last(); (last.Kind == token.Int || last.Kind == token.Float) && last.End.Offset == offset {
			return nil, from, c.end // a number being typed
		}
		items = v.operatorItems(c)
	}
	items = slices.DeleteFunc(items, func(it item) bool { return !strings.HasPrefix(it.label, typed) })
	slices.SortStableFunc(items, func(a, b item) int { return strings.Compare(a.sortKey(), b.sortKey()) })
	items = slices.CompactFunc(items, func(a, b item) bool { return a.label == b.label && a.sortKey() == b.sortKey() })
	if len(items) > 0 && items[0].rank == rankExpected && c.place == atExpr {
		items[0].best = true
	}
	return items, from, c.end
}

// sortKey orders items: by rank, then nearest scope first, then class,
// then label, as a client that ignores case sorts labels, with case
// breaking a tie.
func (it item) sortKey() string {
	return fmt.Sprintf("%d%02d%02d%s\x00%s", it.rank, it.depth, it.class, strings.ToLower(it.label), it.label)
}

// keywordItems returns keywords as completions.
func keywordItems(words ...string) []item {
	out := make([]item, len(words))
	for i, w := range words {
		out[i] = item{label: w, kind: protocol.CompletionKeyword, rank: rankKeyword, depth: keywordDepth, class: keywordClass}
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
		out = append(out, item{label: k.Model.Name, kind: protocol.CompletionInterface, detail: kindHeader(k.Model), rank: rankName})
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
// can start one there, as snippets, in a `when` body the decisions to
// construct, and the imported policies to invoke.
func (v *view) statementItems(c *cursor) []item {
	var words []string
	switch {
	case c.module:
		words = []string{kwUse, kwLet, kwPubLet}
	case c.inBody:
		words = []string{kwLet, kwWhen, kwAssert}
	default:
		words = []string{kwUse, kwParam, kwLet, kwPubLet, kwWhen, kwAssert}
	}
	if !c.inBody && v.pastUses(c) {
		words = slices.DeleteFunc(words, func(w string) bool { return w == kwUse })
	}
	out := keywordItems(words...)
	env := v.env(c)
	for i := range out {
		out[i].snippet = v.statementSnippet(out[i].label, c, env)
	}
	if env == nil || c.module {
		return out
	}
	if c.inBody {
		for _, d := range env.Kind().Decisions {
			out = append(out, v.decisionItem(env.Kind(), d))
		}
	}
	for _, name := range names(env) {
		if b, _ := env.Lookup(name); b.Entity == check.Invocable {
			out = append(out, item{
				label: name, kind: protocol.CompletionModule, detail: "policy " + b.Doc.Name, desc: "invocation",
				doc: code(paramsOf(b.Doc)), snippet: invocationSnippet(name, b.Doc), rank: rankName,
			})
		}
	}
	return out
}

// statementSnippet is what the statement keyword word inserts, for a
// client that takes snippets, with defaults that check as they are: a
// name nothing binds yet, `true` for a condition, `string` for a type,
// and for `use` a document the kind's bundle holds and env doesn't
// import yet. It's "" for `use` when there's none.
func (v *view) statementSnippet(word string, c *cursor, env *check.Env) string {
	switch word {
	case kwWhen:
		return "when ${1:true} {\n\t$0\n}"
	case kwAssert:
		return "assert(\"${1:reason}\", ${2:true})"
	case kwLet, kwPubLet:
		return word + " ${1:" + freeName(env, "name") + "} = ${2:true}"
	case kwParam:
		return "param ${1:" + freeName(env, "name") + "}: ${2:string}"
	case kwUse:
		if path := v.importable(c, env); path != "" {
			return "use ${1:" + path + "}"
		}
	}
	return ""
}

// importable returns a document a `use` at the cursor can import whole:
// the first module of the document's kind, or else the first policy,
// that isn't the document itself and whose last name env doesn't bind
// yet. It returns "" for none.
func (v *view) importable(c *cursor, env *check.Env) string {
	g := v.groupOf(c.kind)
	if g == nil || c.header < 0 {
		return ""
	}
	self := v.docStarting(c.toks[c.header].Pos.Offset)
	var modules, policies []string
	for _, d := range g.Bundle.All() {
		name := d.Name[strings.LastIndexByte(d.Name, '.')+1:]
		if self != nil && d.Name == self.Name || freeName(env, name) != name {
			continue
		}
		if _, ok := d.Node.(*ast.ModuleDoc); ok {
			modules = append(modules, d.Name)
		} else {
			policies = append(policies, d.Name)
		}
	}
	if len(modules) > 0 {
		return modules[0]
	}
	if len(policies) > 0 {
		return policies[0]
	}
	return ""
}

// freeName returns base, or base with a number after it, whichever env
// doesn't bind yet, since a document can't declare a name twice. It
// returns base for a nil env.
func freeName(env *check.Env, base string) string {
	if env == nil {
		return base
	}
	name := base
	for n := 2; ; n++ {
		if _, taken := env.Lookup(name); !taken {
			return name
		}
		name = base + strconv.Itoa(n)
	}
}

// decisionItem is a decision to construct. Its snippet names the reason
// and every payload field without a default.
func (v *view) decisionItem(k *kind.Kind, d *kind.Decision) item {
	return item{
		label: d.Name, kind: protocol.CompletionConstructor, detail: d.Signature(), desc: kwDecision,
		doc:     joinLines(v.kindComment(k.Name, func(decl ast.Decl) *ast.Ident { return decisionDecl(decl, d.Name) }), code(strings.TrimSuffix(d.Source(), "\n"))),
		snippet: constructorSnippet(d), rank: rankName,
	}
}

// constructorSnippet builds `deny(reason: ${1:not_eligible}, field:
// ${2:[]})` for d: its first reason, or its only one as it is, and every
// payload field without a default with its type's zero value, so tabbing
// through leaves a constructor that checks. The reason completes after
// `reason: ` with the others.
func constructorSnippet(d *kind.Decision) string {
	n := 1
	reason := reasonArg + ": " + d.Reasons[0]
	if len(d.Reasons) > 1 {
		reason = reasonArg + ": ${1:" + escapePlaceholder(d.Reasons[0]) + "}"
		n++
	}
	args := []string{reason}
	for _, f := range d.Fields {
		if !f.HasDefault {
			args = append(args, f.Name+": "+placeholder(n, f.Type))
			n++
		}
	}
	return d.Name + "(" + strings.Join(args, ", ") + ")"
}

// invocationSnippet builds `name(param: ${1:0s})` for an invocation of
// the policy e: every param without a default, with its type's zero
// value.
func invocationSnippet(name string, e *check.Exported) string {
	var args []string
	for _, p := range e.Params {
		if p.Required {
			args = append(args, p.Name+": "+placeholder(len(args)+1, p.Type))
		}
	}
	if args == nil {
		return name + "($0)"
	}
	return name + "(" + strings.Join(args, ", ") + ")"
}

// callSnippet builds `split(${1:""}, ${2:""})` for a call of the host
// function f: every parameter with its type's zero value.
func callSnippet(f *kind.Func) string {
	args := make([]string, len(f.Params))
	for i, p := range f.Params {
		args[i] = placeholder(i+1, p)
	}
	if len(args) == 0 {
		return f.Name + "($0)"
	}
	return f.Name + "(" + strings.Join(args, ", ") + ")"
}

// placeholder is a snippet's tabstop n for a value of type t: its zero
// value, so tabbing through leaves code that checks, or a bare tabstop
// for a type without a literal, such as a struct.
func placeholder(n int, t types.Type) string {
	if zero, ok := zeroOf(t); ok {
		return fmt.Sprintf("${%d:%s}", n, escapePlaceholder(zero))
	}
	return fmt.Sprintf("$%d", n)
}

// quantifierSnippet is what `any`, `all` or `filter` inserts: a variable
// nothing binds yet, the nearest list in scope to range over, and `true`
// as the body.
func quantifierSnippet(word string, env *check.Env) string {
	over := "$2"
	if list := nearestList(env); list != "" {
		over = "${2:" + list + "}"
	}
	return word + " ${1:" + freeName(env, "x") + "} in " + over + ": ${3:true}"
}

// nearestList returns the nearest list in env to range over: a name
// whose value is a list, or else a list field of an input or variable,
// nearest scope first, then by name. It returns "" for none.
func nearestList(env *check.Env) string {
	best, depth := "", -1
	take := func(name string, d int) {
		if depth < 0 || d < depth {
			best, depth = name, d
		}
	}
	for _, name := range names(env) {
		b, _ := env.Lookup(name)
		switch b.Entity {
		case check.Input, check.Let, check.Param, check.QuantVar, check.FilterVar:
		default:
			continue
		}
		if _, ok := b.Type.(*types.List); ok {
			take(name, env.Depth(name))
		}
	}
	if best != "" {
		return best
	}
	for _, name := range names(env) {
		b, _ := env.Lookup(name)
		for _, it := range deepItems(name, b, env, nil) {
			if _, ok := it.typ.(*types.List); ok && !strings.Contains(it.label, "?.") {
				take(it.label, it.depth)
			}
		}
	}
	return best
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
		out = append(out, item{label: d.Name, kind: protocol.CompletionModule, detail: describeDoc(d.Node), rank: rankName})
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
			out = append(out, item{label: name, kind: protocol.CompletionVariable, detail: d.Exported.Lets[name].String(), doc: v.letComment(d.Name, name), rank: rankName})
		}
	}
	return out
}

// typeItems completes a param's type: the scalars, list and map, and the
// kind's struct types and enums.
func (v *view) typeItems(c *cursor) []item {
	var out []item
	for _, name := range types.ScalarNames() {
		out = append(out, item{label: name, kind: protocol.CompletionStruct, rank: rankName})
	}
	out = append(out, item{label: typeList, kind: protocol.CompletionStruct, rank: rankName}, item{label: typeMap, kind: protocol.CompletionStruct, rank: rankName})
	if k := v.kindNamed(c.kind); k != nil {
		for _, s := range k.Model.Types {
			out = append(out, item{label: s.Name, kind: protocol.CompletionStruct, doc: code(structSource(s)), rank: rankName})
		}
		for _, e := range k.Model.Enums {
			out = append(out, item{label: e.Name, kind: protocol.CompletionEnum, doc: code(enumSource(e)), rank: rankName})
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
			out = append(out, item{label: reasonArg, insert: reasonArg + ": ", kind: protocol.CompletionField, detail: reasonArg + ": " + strings.Join(d.Reasons, " | "), rank: rankExpected})
		}
		for _, f := range d.Fields {
			if slices.Contains(c.args, f.Name) {
				continue
			}
			it := item{label: f.Name, insert: f.Name + ": ", kind: protocol.CompletionField, detail: fieldSource(f), rank: rankExpected,
				doc: v.kindComment(env.Kind().Name, func(decl ast.Decl) *ast.Ident { return decisionField(decl, d.Name, f.Name) })}
			if f.HasDefault {
				it.rank = rankName
			}
			out = append(out, it)
		}
	case check.Invocable:
		for _, p := range b.Doc.Params {
			if slices.Contains(c.args, p.Name) {
				continue
			}
			it := item{label: p.Name, insert: p.Name + ": ", kind: protocol.CompletionField, detail: paramSource(p), rank: rankExpected}
			if !p.Required {
				it.rank = rankName
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
		out[i] = item{label: r, kind: protocol.CompletionEnumMember, detail: "reason of " + d.Name, rank: rankExpected}
	}
	return out
}

// operandItems completes an operand: every name in scope that's a value,
// the fields of the inputs and variables in scope whose type is the one
// the context expects, the literals of that type, the keywords that start
// an operand, and `outcome` in an assert, only the constants of the
// expected type for a param's default or an invocation's argument, and
// not the operand on the operator's left, which says nothing compared with itself. What fits
// the expected type comes first, the nearest scope first among it.
func (v *view) operandItems(c *cursor) []item {
	out := keywordItems("any", "all", "filter", "not", "present", kwTrue, kwFalse)
	env := v.env(c)
	if env == nil {
		return out
	}
	want := v.expected(c, env)
	for i := range out {
		out[i].typ = keywordTypes[out[i].label]
		if slices.Contains([]string{"any", "all", "filter"}, out[i].label) {
			out[i].snippet = quantifierSnippet(out[i].label, env)
		}
	}
	if env.InAssert {
		out = append(out, item{label: "outcome", kind: protocol.CompletionKeyword, detail: "list<decision>", typ: &types.List{Elem: types.Decision}, rank: rankKeyword, depth: keywordDepth, class: keywordClass})
	}
	lits := literalItems(want)
	invoked := v.invokedParam(c, env)
	if p := invoked; p != nil {
		for i := range lits {
			lits[i].detail = paramSource(p)
		}
	}
	out = append(out, lits...)
	for _, name := range names(env) {
		b, _ := env.Lookup(name)
		for _, it := range v.valueItems(name, b, env) {
			it.depth = env.Depth(name)
			out = append(out, it)
		}
		if want != nil {
			out = append(out, deepItems(name, b, env, want)...)
		}
	}
	for i := range out {
		out[i].rank = fit(out[i].typ, want)
		if out[i].class == keywordClass && out[i].rank != rankExpected {
			out[i].rank = rankKeyword
		}
	}
	if c.constant || invoked != nil {
		// A param's default and bounds, and an invoked policy's
		// arguments, are constants of the param's type.
		out = slices.DeleteFunc(out, func(it item) bool { return !constantItem(it) || want != nil && it.rank != rankExpected })
	}
	if c.left[0] >= 0 {
		self := string(c.text(c.left[0], c.left[1]))
		out = slices.DeleteFunc(out, func(it item) bool { return it.label == self })
	}
	return out
}

// keywordTypes are the types of the values the operand keywords make.
var keywordTypes = map[string]types.Type{
	"any": types.Bool, "all": types.Bool, "not": types.Bool, "present": types.Bool, kwTrue: types.Bool, kwFalse: types.Bool,
}

// literalItems returns the literals of the type want: a duration, a
// string, an empty list or map. A bool's literals are keywords already.
func literalItems(want types.Type) []item {
	lit := func(label, snippet, detail string) []item {
		return []item{{label: label, snippet: snippet, detail: detail, kind: protocol.CompletionValue, typ: want, rank: rankExpected, class: 1}}
	}
	switch t := want.(type) {
	case types.Basic:
		switch t {
		case types.Duration:
			return lit("1h", "${1:1}${2:h}", "a duration")
		case types.String:
			return lit(`""`, `"$0"`, "a string")
		}
	case *types.List:
		return lit("[]", "[$0]", t.String())
	case *types.Map:
		return lit("{}", "{$0}", t.String())
	}
	return nil
}

// valueItems returns the completions a name in scope offers as an
// operand: itself when it's a value, its pub lets' qualifier when it's a
// module, and for an enum value several enums declare, the qualified value
// of each. Imported policies aren't values, and decisions are only in an
// assert.
func (v *view) valueItems(name string, b check.Binding, env *check.Env) []item {
	it := item{label: name, typ: b.Type}
	switch b.Entity {
	case check.Input:
		it.kind, it.detail, it.desc, it.class = protocol.CompletionVariable, b.Type.String(), "input", 1
		it.doc = v.kindComment(env.Kind().Name, func(d ast.Decl) *ast.Ident { return inputDecl(d, name) })
	case check.Let:
		it.kind, it.detail, it.desc = protocol.CompletionVariable, typeName(b.Type), kwLet
		if b.Doc != nil {
			it.desc, it.doc = "from "+b.Doc.Name, joinLines("from "+b.Doc.Name, v.letComment(b.Doc.Name, b.Let))
		}
	case check.Param:
		it.kind, it.detail, it.desc = protocol.CompletionConstant, typeName(b.Type), kwParam
	case check.QuantVar, check.FilterVar:
		it.kind, it.detail, it.desc = protocol.CompletionVariable, typeName(b.Type), "variable"
	case check.Function:
		it.kind, it.detail, it.desc, it.class, it.typ = protocol.CompletionFunction, b.Func.Signature(), "host function", 2, b.Func.Result
		it.snippet = callSnippet(b.Func)
		it.doc = v.kindComment(env.Kind().Name, func(d ast.Decl) *ast.Ident { return fnDecl(d, name) })
	case check.Module:
		it.kind, it.detail, it.desc, it.class, it.typ = protocol.CompletionModule, "module "+b.Doc.Name, "module", 2, nil
	case check.EnumType:
		it.kind, it.detail, it.desc, it.class, it.typ = protocol.CompletionEnum, enumSource(b.Type.(*types.Enum)), "enum", 3, nil
	case check.DecisionName:
		if !env.InAssert {
			return nil
		}
		it.kind, it.detail, it.desc, it.class = protocol.CompletionValue, kwDecision, kwDecision, 3
	case check.EnumValue:
		if b.Type == nil {
			var out []item
			for _, e := range env.Kind().EnumsWith(name) {
				out = append(out, enumValueItem(e.Name+"."+name, e))
			}
			return out
		}
		return []item{enumValueItem(name, b.Type.(*types.Enum))}
	default:
		return nil
	}
	return []item{it}
}

// deepItems returns the fields of the struct name holds, an input's or a
// variable's, whose type is want, or all of them for a nil want:
// `service.name` where a string is expected. A field of an optional struct is read with `?.`.
func deepItems(name string, b check.Binding, env *check.Env, want types.Type) []item {
	switch b.Entity {
	case check.Input, check.Let, check.Param, check.QuantVar, check.FilterVar:
	default:
		return nil
	}
	dot, t := ".", b.Type
	if opt, ok := t.(*types.Optional); ok {
		dot, t = "?.", opt.Elem
	}
	s, ok := t.(*types.Struct)
	if !ok {
		return nil
	}
	if decl := env.Kind().Type(s.Name); decl != nil {
		s = decl
	}
	var out []item
	for _, f := range s.Fields {
		ft := f.Type
		if dot == "?." {
			if _, opt := ft.(*types.Optional); !opt {
				ft = &types.Optional{Elem: ft}
			}
		}
		if want == nil || fit(ft, want) == rankExpected {
			out = append(out, item{label: name + dot + f.Name, typ: ft, kind: protocol.CompletionField, detail: ft.String(), desc: "field of " + s.Name, depth: env.Depth(name), class: 4})
		}
	}
	return out
}

// enumValueItem is a value of e.
func enumValueItem(label string, e *types.Enum) item {
	return item{label: label, typ: e, kind: protocol.CompletionEnumMember, detail: e.Name, desc: "value of " + e.Name, class: 3}
}

// operatorItems completes the operator after an operand: those that apply
// to its type, or every keyword operator when its type isn't known.
func (v *view) operatorItems(c *cursor) []item {
	ops := anyOperators
	if env := v.env(c); env != nil && c.left[0] >= 0 {
		if found := operatorsOf(v.typeOf(c.text(c.left[0], c.left[1]), env)); found != nil {
			ops = found
		}
	}
	out := make([]item, len(ops))
	for i, op := range ops {
		insert := op + " "
		if op == "?." {
			insert = op
		}
		out[i] = item{label: op, insert: insert, kind: protocol.CompletionOperator, rank: rankName, class: i}
	}
	return out
}

// operatorsOf returns the operators that follow an operand of type t, in
// the order they're offered, or nil for a type not worked out.
func operatorsOf(t types.Type) []string {
	switch t := t.(type) {
	case types.Basic:
		switch t {
		case types.Bool:
			return boolOperators
		case types.String:
			return stringOperators
		case types.Int, types.Float, types.Duration:
			return slices.Concat(orderedOperators, numberOperators)
		case types.Timestamp:
			return slices.Concat(orderedOperators, numberOperators)
		case types.Decision:
			return enumOperators
		}
	case *types.Enum:
		return enumOperators
	case *types.List:
		return listOperators
	case *types.Map:
		return []string{opHas}
	case *types.Optional:
		if _, ok := t.Elem.(*types.Struct); ok {
			return []string{"??", "?."}
		}
		return []string{"??"}
	case *types.Struct, *types.Candidate:
		return []string{}
	}
	return nil
}

// unitItems completes the unit of a duration being typed, `24|` to `24h`,
// where a duration is expected, and returns where the number starts. It
// returns nil anywhere else.
func (v *view) unitItems(c *cursor) ([]item, int) {
	last := c.last()
	if last.Kind != token.Int || last.End.Offset != c.offset {
		return nil, 0
	}
	env := v.env(c)
	if env == nil {
		return nil, 0
	}
	// The number is the operand being typed: what it follows, without it,
	// says what it should be.
	before := *c
	before.toks, before.left, before.op = c.toks[:c.lastIndex()], [2]int{-1, -1}, ""
	before.operator(0)
	if v.expected(&before, env) != types.Duration {
		return nil, 0
	}
	out := make([]item, len(durationUnits))
	for i, u := range durationUnits {
		out[i] = item{label: last.Text + u, kind: protocol.CompletionValue, detail: "duration", rank: rankExpected, class: i}
	}
	return out, last.Pos.Offset
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
		if out, ok := v.qualifiedItems(c.toks[first], env); ok {
			return out
		}
	}
	return v.fieldItems(v.typeOf(c.text(first, last), env), env.Kind())
}

// qualifiedItems returns what a single name qualifies after a `.`: the
// decisions after `outcome`, an enum's values, a module's or an imported
// policy's pub lets, or a decision's reasons. It reports false for a name
// that's a value, whose fields follow its type.
func (v *view) qualifiedItems(t token.Token, env *check.Env) ([]item, bool) {
	var out []item
	if t.Kind == token.KwOutcome {
		for _, d := range env.Kind().Decisions {
			out = append(out, item{label: d.Name, kind: protocol.CompletionField, detail: "list<" + d.Name + " candidate>", rank: rankName})
		}
		return out, true
	}
	b, _ := env.Lookup(t.Text)
	switch b.Entity {
	case check.EnumType:
		e := b.Type.(*types.Enum)
		for _, val := range e.Values {
			out = append(out, item{label: val, kind: protocol.CompletionEnumMember, detail: e.Name, rank: rankName})
		}
	case check.Module, check.Invocable:
		for _, name := range b.Doc.LetNames() {
			out = append(out, item{label: name, kind: protocol.CompletionVariable, detail: typeName(b.Doc.Lets[name]), doc: v.letComment(b.Doc.Name, name), rank: rankName})
		}
	case check.DecisionName:
		d := env.Kind().Decision(t.Text)
		for _, r := range d.Reasons {
			out = append(out, item{label: r, kind: protocol.CompletionEnumMember, detail: "reason of " + d.Name, rank: rankName})
		}
	default:
		return nil, false
	}
	return out, true
}

// fieldItems returns the fields a value of type t has: a struct's,
// inside an optional too, a candidate's, or for a list of candidates the
// reasons that narrow it.
func (v *view) fieldItems(t types.Type, k *kind.Kind) []item {
	if opt, ok := t.(*types.Optional); ok {
		t = opt.Elem
	}
	var fields []*types.Field
	owner := ""
	switch t := t.(type) {
	case *types.Struct:
		if decl := k.Type(t.Name); decl != nil {
			t = decl
		}
		fields, owner = t.Fields, t.Name
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
			out[i] = item{label: r, kind: protocol.CompletionEnumMember, detail: "the " + d.Name + " candidates with reason " + r, rank: rankName}
		}
		return out
	}
	out := make([]item, len(fields))
	for i, f := range fields {
		out[i] = item{label: f.Name, kind: protocol.CompletionField, detail: f.Type.String(), rank: rankName}
		if owner != "" {
			name := f.Name
			out[i].doc = v.kindComment(k.Name, func(d ast.Decl) *ast.Ident { return typeField(d, owner, name) })
		}
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

// escapePlaceholder escapes text for a snippet's placeholder: `$`, `}`
// and `\`.
func escapePlaceholder(s string) string {
	return strings.NewReplacer(`\`, `\\`, `$`, `\$`, `}`, `\}`).Replace(s)
}

// constantItem reports whether it can be part of a constant, such as a
// param's default: a literal, an enum or its value, or `true` and
// `false`.
func constantItem(it item) bool {
	switch it.kind {
	case protocol.CompletionValue, protocol.CompletionEnum, protocol.CompletionEnumMember:
		return true
	}
	return it.label == kwTrue || it.label == kwFalse
}

// pastUses reports whether a statement other than a `use` comes before
// the cursor in its document, after which a `use` can't.
func (v *view) pastUses(c *cursor) bool {
	if c.header < 0 {
		return false
	}
	d := v.docStarting(c.toks[c.header].Pos.Offset)
	if d == nil {
		return false
	}
	var first ast.Node
	switch n := d.Node.(type) {
	case *ast.PolicyDoc:
		if len(n.Stmts) > 0 {
			first = n.Stmts[0]
		}
	case *ast.ModuleDoc:
		if len(n.Lets) > 0 {
			first = n.Lets[0]
		}
	}
	return first != nil && first.Pos().Offset < c.start
}
