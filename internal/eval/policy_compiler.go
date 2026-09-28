package eval

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
)

type policyCompiler struct {
	compiler
	policy *Policy
	src    []byte
}

// CompilePolicy compiles doc, checked into info against k, over the
// binding b. params holds the values bound to the policy's params by the
// host; params with defaults may be left out. The source is kept for the
// text of conditions in the trace.
func CompilePolicy(doc *ast.PolicyDoc, file string, src []byte, info *check.Info, k *kind.Kind, b *gokind.Binding, params map[string]Value) (*Policy, *diag.Error) {
	pc := &policyCompiler{
		compiler: compiler{info: info, scope: NewScope(b)},
		policy:   &Policy{kind: k, Name: doc.Name.String(), File: file, ranks: map[string]int{}},
		src:      src,
	}
	pc.policy.scope = pc.scope
	err := catch(func() {
		pc.params(doc.Stmts, params)
		pc.lets(info.Lets)
		pc.ranks()
		pc.policy.def = pc.defaultCandidate()
		pc.policy.body = pc.nodes(doc.Stmts, nil)
	})
	if err != nil {
		err.File = file
		return nil, err
	}
	return pc.policy, nil
}

// params binds every param as a constant: the host's value when given,
// the declared default otherwise.
func (pc *policyCompiler) params(stmts []ast.Stmt, bound map[string]Value) {
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
			panic(&diag.Error{ //nolint:nopanic // compile errors unwind to CompilePolicy, which returns them
				Msg: fmt.Sprintf("param `%s` has no value", name), Pos: p.Pos(), End: p.End(),
				Help: fmt.Sprintf("bind it with policy.Params{%q: ...} when compiling, or give it a default", name),
			})
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

// lets declares every let before compiling any, so an expression can
// read a let compiled later; the frame evaluates each on first use.
func (pc *policyCompiler) lets(lets []*ast.LetStmt) {
	for _, l := range lets {
		pc.scope.Let(l.Name.Name)
	}
	for _, l := range lets {
		pc.scope.SetLet(pc.scope.Let(l.Name.Name), pc.expr(l.Value))
	}
}

// ranks assigns each decision its sort rank: its position in the
// precedence, or its declaration order for a collecting kind.
func (pc *policyCompiler) ranks() {
	order := pc.policy.kind.Precedence
	if pc.policy.Collect() {
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
	r := &Rule{Decision: d, Reason: def.Reason, rank: pc.policy.ranks[d.Name]}
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
	if b := pc.scope.binding; b != nil {
		r.payload = b.Payloads[r.Decision.Name]
	}
}

// field describes how a payload field gets its value, and where it lands
// in the Go payload struct.
func (pc *policyCompiler) field(r *Rule, name string, e Expr, v Value) payloadField {
	f := payloadField{name: name, expr: e, value: v}
	if r.payload != nil {
		f.index = pc.scope.binding.Fields["decision "+r.Decision.Name+"."+name]
		f.typ = r.payload.FieldByIndex(f.index).Type
	}
	return f
}

// nodes compiles a statement list under the enclosing conditions. Lets
// in a body were compiled with the document's lets, so only blocks,
// constructors and asserts remain.
func (pc *policyCompiler) nodes(ss []ast.Stmt, conds []*Cond) []*node {
	var out []*node
	for _, s := range ss {
		switch s := s.(type) {
		case *ast.WhenStmt:
			out = append(out, &node{block: pc.when(s, conds)})
		case *ast.CallStmt:
			out = append(out, &node{rule: pc.constructor(s, conds)})
		case *ast.AssertStmt:
			out = append(out, &node{assert: pc.assert(s, conds)})
		}
	}
	return out
}

// when compiles a rule block, recording which phases have work beneath
// it so the others skip it without evaluating its condition.
func (pc *policyCompiler) when(s *ast.WhenStmt, conds []*Cond) *block {
	c := &Cond{Text: pc.text(s.Cond), Pos: s.Cond.Pos(), End: s.Cond.End()}
	b := &block{cond: pc.expr(s.Cond), text: c, id: pc.scope.Cond()}
	b.body = pc.nodes(s.Body, append(conds[:len(conds):len(conds)], c))
	for _, n := range b.body {
		switch {
		case n.rule != nil:
			b.hasRules = true
		case n.assert != nil:
			b.note(n.assert)
		default:
			b.hasRules = b.hasRules || n.block.hasRules
			for _, a := range n.block.asserts {
				b.note(a)
			}
		}
	}
	return b
}

// note records an assert beneath the block.
func (b *block) note(a *Assert) {
	b.asserts = append(b.asserts, a)
	if a.ReadsOutcome {
		b.hasOut = true
	} else {
		b.hasInput = true
	}
}

// text returns the source of x with runs of whitespace collapsed, for
// the trace.
func (pc *policyCompiler) text(x ast.Expr) string {
	from, to := x.Pos().Offset, x.End().Offset
	if from < 0 || to > len(pc.src) || from > to {
		return ast.Sprint(x)
	}
	return strings.Join(strings.Fields(string(pc.src[from:to])), " ")
}

// constructor compiles a decision constructor into a rule.
func (pc *policyCompiler) constructor(s *ast.CallStmt, conds []*Cond) *Rule {
	d := pc.info.Constructors[s]
	if d == nil {
		throwf(s, "constructor `%s` wasn't checked; compile only checked policies", s.Name.Name)
	}
	reason, ok := s.Positional.(*ast.StringLit)
	if !ok {
		throwf(s, "constructor `%s` has no literal reason", s.Name.Name)
	}
	r := &Rule{
		Decision: d, Reason: reason.Value, Policy: pc.policy.Name, File: pc.policy.File,
		Conds: conds, Pos: s.Pos(), End: s.End(), rank: pc.policy.ranks[d.Name],
	}
	pc.payloadType(r)
	given := map[string]Expr{}
	for _, a := range s.Args {
		given[a.Name.Name] = pc.expr(a.Value)
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
		cond: pc.expr(s.Cond), Policy: pc.policy.Name, File: pc.policy.File,
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
