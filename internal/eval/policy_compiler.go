package eval

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Source is a checked document the compiler can compile as the root,
// instantiate for an invocation, or read pub lets from.
type Source struct {
	Doc  ast.Doc
	Info *check.Info // what the checker recorded for Doc
	File string
	Src  []byte // the whole file's source, for the text of conditions and arguments
}

// Linker finds the documents a policy imports or invokes, by name.
type Linker interface {
	// Source returns the checked document called name, or false when
	// there is none to link.
	Source(name string) (*Source, bool)
}

// Options configures [CompilePolicy].
type Options struct {
	Params map[string]Value // the root's params bound by the host
	// Static compiles the structure only: rules, conditions and chains
	// as text, with no closures, so a policy can be explained without
	// host functions or a binding.
	Static bool
}

// instance is one document as compiled for one use: the root, one
// invocation with its params bound, or a document imported for its pub
// lets. Each has its own scope, and its own frame per evaluation.
type instance struct {
	scope   *Scope
	info    *check.Info
	imports map[string]*instance // documents whose pub lets this one reads
	name    string
	file    string
	src     []byte
	chain   []Site
	body    []*node
	index   int // slot in each evaluation's frame table
}

type policyCompiler struct {
	compiler
	policy  *Policy
	inst    *instance
	link    Linker
	binding *gokind.Binding
	shared  map[string]*instance // documents imported for their lets, one instance each
	stack   []string             // the invocation chain being compiled, for cycles
	static  bool
}

// CompilePolicy compiles root against k over the binding b, instantiating
// every policy it invokes and linking every document it imports through
// link. Every document must have checked cleanly against k. b can be nil
// for a static compile. A problem the checker couldn't see, such as an
// invocation cycle, a required param left unbound or a param out of its
// bounds, comes back as the error.
func CompilePolicy(root *Source, k *kind.Kind, b *gokind.Binding, link Linker, o Options) (*Policy, *diag.Error) {
	doc, ok := root.Doc.(*ast.PolicyDoc)
	if !ok {
		return nil, &diag.Error{File: root.File, Msg: "the root must be a policy"}
	}
	p := &Policy{kind: k, Name: doc.Name.String(), File: root.File, ranks: map[string]int{}, static: o.Static}
	pc := &policyCompiler{policy: p, link: link, binding: b, shared: map[string]*instance{}, static: o.Static}
	err := catch(func() {
		pc.ranks()
		p.root = pc.instantiate(root, o.Params, nil, nil, nil)
		p.def = pc.defaultCandidate()
	})
	if err != nil {
		if err.File == "" {
			err.File = root.File
		}
		return nil, err
	}
	return p, nil
}

// instantiate compiles src as an instance: params bound (given, or
// their defaults), imports linked, and the body under the enclosing
// conditions and call chain of the invocation that made it. at is the
// invocation statement, nil for the root.
func (pc *policyCompiler) instantiate(src *Source, params map[string]Value, chain []Site, conds []*Cond, at *ast.CallStmt) *instance {
	doc := src.Doc.(*ast.PolicyDoc)
	inst := pc.newInstance(src, doc.Name.String())
	inst.chain = chain
	sub := pc.for_(inst)
	sub.params(doc.Stmts, params, at)
	sub.imports(doc.Uses)
	inst.body = sub.nodes(doc.Stmts, conds)
	return inst
}

// newInstance sets up an instance's scope, with every top-level and
// scoped let declared and compiled on first reference.
func (pc *policyCompiler) newInstance(src *Source, name string) *instance {
	inst := &instance{scope: NewScope(pc.binding), info: src.Info, imports: map[string]*instance{}, name: name, file: src.File, src: src.Src}
	inst.index = pc.policy.nframes
	pc.policy.nframes++
	inst.scope.inst = inst
	for _, l := range src.Info.Lets {
		inst.scope.Let(l.Name.Name)
		inst.scope.decls[l.Name.Name] = l
	}
	sub := pc.for_(inst)
	inst.scope.compile = func(l *ast.LetStmt) Expr { return sub.exprOrNil(l.Value) }
	return inst
}

// for_ returns a compiler over inst, sharing everything else.
func (pc *policyCompiler) for_(inst *instance) *policyCompiler {
	return &policyCompiler{
		compiler{info: inst.info, scope: inst.scope},
		pc.policy, inst, pc.link, pc.binding, pc.shared, pc.stack, pc.static,
	}
}

// exprOrNil compiles x, or nothing in a static compile.
func (pc *policyCompiler) exprOrNil(x ast.Expr) Expr {
	if pc.static {
		return nil
	}
	return pc.expr(x)
}

// imports links the documents the instance reads pub lets from. Each
// is compiled once per root, as an instance with no params and no body.
func (pc *policyCompiler) imports(uses []*ast.UseStmt) {
	for _, u := range uses {
		name := u.Path.String()
		if _, ok := pc.inst.imports[name]; ok {
			continue
		}
		pc.inst.imports[name] = pc.imported(name, u)
	}
}

// imported returns the shared instance of the document called name,
// compiling it on first use.
func (pc *policyCompiler) imported(name string, at ast.Node) *instance {
	if inst, ok := pc.shared[name]; ok {
		return inst
	}
	src, ok := pc.link.Source(name)
	if !ok {
		throwf(at, "document %s isn't in the bundle", name)
	}
	inst := pc.newInstance(src, name)
	pc.shared[name] = inst
	var uses []*ast.UseStmt
	switch d := src.Doc.(type) {
	case *ast.ModuleDoc:
		uses = d.Uses
	case *ast.PolicyDoc:
		uses = d.Uses
	}
	pc.for_(inst).imports(uses)
	return inst
}

// params binds every param as a constant: the given value when there
// is one, the declared default otherwise. A required param left unbound
// is reported at the invocation, or at the declaration for the root.
func (pc *policyCompiler) params(stmts []ast.Stmt, bound map[string]Value, at *ast.CallStmt) {
	for _, s := range stmts {
		p, ok := s.(*ast.ParamStmt)
		if !ok {
			continue
		}
		name := p.Name.Name
		if v, bound := bound[name]; bound {
			pc.scope.Bind(name, v)
			continue
		}
		if p.Default == nil {
			if at != nil {
				pc.failf(at, fmt.Sprintf("bind it in the invocation: `%s(%s: ...)`", at.Name.Name, name), "invocation of %s doesn't bind param `%s`", pc.inst.name, name)
			}
			if pc.static {
				// Explained on its own: the param stands for itself.
				pc.scope.Bind(name, reflect.ValueOf(symbol(name)))
				continue
			}
			pc.failf(p, fmt.Sprintf("bind it with policy.Params{%q: ...} when compiling, or give it a default", name), "param `%s` has no value", name)
		}
		t, ok := pc.info.Params[p]
		if !ok {
			throwf(p, "param `%s` wasn't checked", name)
		}
		v, err := constant.Eval(p.Default, t)
		if err != nil {
			panic(err) //nolint:nopanic // compile errors unwind to CompilePolicy, which returns them
		}
		pc.scope.Bind(name, reflect.ValueOf(v))
	}
}

// failf raises a compile error at n, in the instance's file.
func (pc *policyCompiler) failf(n ast.Node, help, format string, args ...any) {
	panic(&diag.Error{File: pc.inst.file, Msg: fmt.Sprintf(format, args...), Help: help, Pos: n.Pos(), End: n.End()}) //nolint:nopanic // compile errors unwind to CompilePolicy, which returns them
}

// ranks assigns each decision its sort rank: its position in the
// precedence when the kind has one, and its declaration order otherwise,
// which only orders the outcome, since without a precedence every
// candidate is at the top.
func (pc *policyCompiler) ranks() {
	order := pc.policy.kind.Precedence
	if len(order) == 0 {
		order = make([]string, len(pc.policy.kind.Decisions))
		for i, d := range pc.policy.kind.Decisions {
			order[i] = d.Name
		}
	}
	for i, name := range order {
		pc.policy.ranks[name] = i
	}
}

// defaultCandidate builds the kind's default as a candidate: constant
// payload values from the declaration, field defaults for the rest.
func (pc *policyCompiler) defaultCandidate() *Candidate {
	def := pc.policy.kind.Default
	if def == nil {
		return nil
	}
	d := pc.policy.kind.Decision(def.Decision)
	r := &Rule{Decision: d, Reason: def.Reason, rank: pc.policy.ranks[d.Name], rrank: d.ReasonRank(def.Reason)}
	pc.payloadType(r)
	for _, f := range d.Fields {
		v, ok := def.Args[f.Name]
		if !ok {
			v = f.Default
		}
		r.fields = append(r.fields, pc.field(r, f.Name, nil, reflect.ValueOf(v)))
	}
	return fire(r, nil)
}

// payloadType records the Go payload struct of the rule's decision, when
// the binding has one.
func (pc *policyCompiler) payloadType(r *Rule) {
	if pc.binding != nil {
		r.payload = pc.binding.Payloads[r.Decision.Name]
	}
}

// field describes how a payload field gets its value, and where it lands
// in the Go payload struct.
func (pc *policyCompiler) field(r *Rule, name string, e Expr, v Value) payloadField {
	f := payloadField{name: name, expr: e, value: v}
	if r.payload != nil {
		f.index = pc.binding.Fields["decision "+r.Decision.Name+"."+name]
		f.typ = r.payload.FieldByIndex(f.index).Type
	}
	return f
}

// nodes compiles a statement list under the enclosing conditions. Lets
// in a body were declared with the document's lets, so only blocks,
// constructors, invocations and asserts remain.
func (pc *policyCompiler) nodes(ss []ast.Stmt, conds []*Cond) []*node {
	var out []*node
	for _, s := range ss {
		switch s := s.(type) {
		case *ast.WhenStmt:
			out = append(out, &node{block: pc.when(s, conds)})
		case *ast.CallStmt:
			if name, ok := pc.info.Invocations[s]; ok {
				out = append(out, &node{invoke: pc.invoke(s, name, conds)})
			} else {
				out = append(out, &node{rule: pc.constructor(s, conds)})
			}
		case *ast.AssertStmt:
			out = append(out, &node{assert: pc.assert(s, conds)})
		}
	}
	return out
}

// when compiles a rule block, recording what's beneath it so the phases
// with nothing there skip it without evaluating its condition.
func (pc *policyCompiler) when(s *ast.WhenStmt, conds []*Cond) *block {
	c := &Cond{Text: pc.text(s.Cond), Pos: s.Cond.Pos(), End: s.Cond.End()}
	b := &block{cond: pc.exprOrNil(s.Cond), text: c, id: pc.scope.Cond()}
	b.body = pc.nodes(s.Body, append(slices.Clip(conds), c))
	b.summary = summarize(b.body)
	return b
}

// invoke compiles a policy invocation: the arguments are evaluated now,
// since they can only hold constants and this instance's params, checked
// against the invoked policy's bounds, and the policy is instantiated
// with them under the enclosing conditions.
func (pc *policyCompiler) invoke(s *ast.CallStmt, name string, conds []*Cond) *invocation {
	if slices.Contains(pc.stack, name) || name == pc.inst.name {
		cycle := append(append(append([]string{}, pc.stack...), pc.inst.name), name)
		pc.failf(s, "a policy can't invoke itself, directly or through other policies", "invocation cycle: %s", strings.Join(cycle, " -> "))
	}
	src, ok := pc.link.Source(name)
	if !ok {
		throwf(s, "policy %s isn't in the bundle", name)
	}
	site := Site{Policy: pc.inst.name, File: pc.inst.file, Pos: s.Pos(), End: s.End()}
	params := map[string]Value{}
	for _, a := range s.Args {
		v := pc.static_(a.Value)
		pc.checkBounds(src, a, v)
		params[a.Name.Name] = v
	}
	sub := pc.for_(pc.inst)
	sub.stack = append(append([]string{}, pc.stack...), pc.inst.name)
	inst := sub.instantiate(src, params, append(slices.Clip(pc.inst.chain), site), conds, s)
	return &invocation{inst: inst, summary: summarize(inst.body), Site: site}
}

// symbol is a param's value in a static compile of a policy whose params
// aren't bound: the text that stands for it, its own name or the
// argument expression it was passed as.
type symbol string

var symbolType = reflect.TypeFor[symbol]()

// static_ evaluates an invocation argument at compile time. The checker
// allows only constants and the instance's own params in it, both known
// now, so no input is needed. In a static compile an argument that reads
// an unbound param stays symbolic, as its own text.
func (pc *policyCompiler) static_(x ast.Expr) Value {
	if pc.static && pc.symbolicIn(x) {
		return reflect.ValueOf(symbol(pc.text(x)))
	}
	e := pc.expr(x)
	v, err := Run(e, newFrame(Value{}, pc.scope))
	if err != nil {
		panic(err) //nolint:nopanic // compile errors unwind to CompilePolicy, which returns them
	}
	return v
}

// symbolicIn reports whether x reads a param that stands for itself.
func (pc *policyCompiler) symbolicIn(x ast.Expr) bool {
	found := false
	ast.Inspect(x, func(n ast.Expr) bool {
		if id, ok := n.(*ast.Ident); ok {
			if v, isParam := pc.scope.consts[id.Name]; isParam && v.Type() == symbolType {
				found = true
			}
		}
		return !found
	})
	return found
}

// checkBounds checks a bound value against the param's `min` and `max`.
func (pc *policyCompiler) checkBounds(src *Source, a *ast.NamedArg, v Value) {
	if v.Type() == symbolType {
		return
	}
	callee := src.Doc.(*ast.PolicyDoc)
	var decl *ast.ParamStmt
	for _, s := range callee.Stmts {
		if p, ok := s.(*ast.ParamStmt); ok && p.Name.Name == a.Name.Name {
			decl = p
		}
	}
	if decl == nil || (decl.Min == nil && decl.Max == nil) {
		return
	}
	t := src.Info.Params[decl]
	got := constant.Ordered(iface(v))
	declares := fmt.Sprintf("%s declares `%s`", callee.Name, strings.Join(strings.Fields(string(src.Src[decl.Pos().Offset:decl.End().Offset])), " "))
	if lo, err := constant.Eval(decl.Min, t); decl.Min != nil && err == nil && constant.Compare(got, lo) < 0 {
		pc.failf(a.Value, declares, "%s: %s is below the minimum %s", a.Name.Name, constant.Format(got), constant.Format(lo))
	}
	if hi, err := constant.Eval(decl.Max, t); decl.Max != nil && err == nil && constant.Compare(got, hi) > 0 {
		pc.failf(a.Value, declares, "%s: %s is above the maximum %s", a.Name.Name, constant.Format(got), constant.Format(hi))
	}
}

// text returns the source of x with runs of whitespace collapsed and
// every param replaced by its bound value, for the trace and explain.
func (pc *policyCompiler) text(x ast.Expr) string {
	from, to := x.Pos().Offset, x.End().Offset
	if from < 0 || to > len(pc.inst.src) || from > to {
		return ast.Sprint(x)
	}
	type span struct {
		text     string
		from, to int
	}
	var subs []span
	ast.Inspect(x, func(n ast.Expr) bool {
		if id, ok := n.(*ast.Ident); ok {
			if v, isParam := pc.scope.consts[id.Name]; isParam {
				subs = append(subs, span{text: formatValue(v), from: id.Pos().Offset, to: id.End().Offset})
			}
		}
		return true
	})
	sort.Slice(subs, func(i, j int) bool { return subs[i].from < subs[j].from })
	var b strings.Builder
	at := from
	for _, s := range subs {
		b.Write(pc.inst.src[at:s.from])
		b.WriteString(s.text)
		at = s.to
	}
	b.Write(pc.inst.src[at:to])
	return strings.Join(strings.Fields(b.String()), " ")
}

// formatValue renders a bound value as a Sigil literal, or a symbolic
// param as its text.
func formatValue(v Value) string {
	if v.IsValid() && v.Type() == symbolType {
		return v.String()
	}
	return constant.Format(literal(norm(v)))
}

// literal converts a Go value to the constant representation, so it can
// be formatted as source.
func literal(v Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Type() == durationType {
		return time.Duration(v.Int())
	}
	if v.Type() == timeType {
		return v.Interface()
	}
	switch v.Kind() {
	case reflect.Bool:
		return v.Bool()
	case reflect.Int, reflect.Int64:
		return v.Int()
	case reflect.Float64:
		return v.Float()
	case reflect.String:
		return v.String()
	case reflect.Slice:
		out := make([]any, v.Len())
		for i := range v.Len() {
			out[i] = literal(norm(v.Index(i)))
		}
		return out
	case reflect.Map:
		out := make(map[any]any, v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out[literal(norm(iter.Key()))] = literal(norm(iter.Value()))
		}
		return out
	case reflect.Pointer:
		if v.IsNil() {
			return nil
		}
		return literal(v.Elem())
	}
	return v.Interface()
}

var durationType = reflect.TypeFor[time.Duration]()

// constructor compiles a decision constructor into a rule.
func (pc *policyCompiler) constructor(s *ast.CallStmt, conds []*Cond) *Rule {
	d := pc.info.Constructors[s]
	if d == nil {
		throwf(s, "constructor `%s` wasn't checked; compile only checked policies", s.Name.Name)
	}
	reason, ok := s.Positional.(*ast.Ident)
	if !ok {
		throwf(s, "constructor `%s` has no reason name", s.Name.Name)
	}
	r := &Rule{
		Decision: d, Reason: reason.Name, Policy: pc.inst.name, File: pc.inst.file, Chain: pc.inst.chain,
		Conds: conds, Pos: s.Pos(), End: s.End(), rank: pc.policy.ranks[d.Name], rrank: d.ReasonRank(reason.Name),
	}
	pc.payloadType(r)
	given := map[string]Expr{}
	for _, a := range s.Args {
		given[a.Name.Name] = pc.exprOrNil(a.Value)
		r.Args = append(r.Args, Arg{Name: a.Name.Name, Text: pc.text(a.Value)})
	}
	for _, f := range d.Fields {
		if e, ok := given[f.Name]; ok {
			r.fields = append(r.fields, pc.field(r, f.Name, e, Value{}))
		} else {
			r.fields = append(r.fields, pc.field(r, f.Name, nil, reflect.ValueOf(f.Default)))
		}
	}
	return r
}

// assert compiles an assertion. Whether its condition reads `outcome`
// decides the phase it's checked in.
func (pc *policyCompiler) assert(s *ast.AssertStmt, conds []*Cond) *Assert {
	a := &Assert{
		cond: pc.exprOrNil(s.Cond), Text: pc.text(s.Cond), Policy: pc.inst.name, File: pc.inst.file, Chain: pc.inst.chain,
		Conds: conds, Pos: s.Pos(), End: s.End(), ReadsOutcome: readsOutcome(s.Cond),
	}
	if s.Reason != nil {
		a.Reason = s.Reason.Value
	}
	return a
}

// readsOutcome reports whether x mentions `outcome`.
func readsOutcome(x ast.Expr) bool {
	found := false
	ast.Inspect(x, func(n ast.Expr) bool {
		if _, ok := n.(*ast.Outcome); ok {
			found = true
		}
		return !found
	})
	return found
}
