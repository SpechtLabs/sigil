package check

import (
	"fmt"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// kindLoader builds a kind.Kind from one kind document, reporting through
// the checker.
type kindLoader struct {
	c     *Checker
	kind  *kind.Kind
	decls map[*ast.TypeDecl]*types.Struct // the struct built for each declaration
	loc   map[string]span                 // validation key to source span
	// The declarations that may appear once.
	precedenceAt, collectAt, defaultAt, conflictAt ast.Node
}

type span struct {
	start, end token.Pos
}

// set records where the thing named by key is, for kind.Validate.
func (l *kindLoader) set(key string, n ast.Node) {
	l.loc[key] = span{n.Pos(), n.End()}
}

func (l *kindLoader) declareType(d *ast.TypeDecl) {
	if d == nil {
		return
	}
	s := &types.Struct{Name: d.Name.Name}
	l.kind.Types = append(l.kind.Types, s)
	l.decls[d] = s
	l.set("type "+s.Name, d.Name)
}

// enum loads an enum. It has no types to resolve, so it's complete as
// soon as it's declared.
func (l *kindLoader) enum(d *ast.EnumDecl) {
	if d == nil {
		return
	}
	key := "enum " + d.Name.Name
	l.set(key, d.Name)
	e := &types.Enum{Name: d.Name.Name, Values: make([]string, len(d.Values))}
	for i, v := range d.Values {
		l.set(key+".value "+v.Name, v)
		e.Values[i] = v.Name
	}
	l.kind.Enums = append(l.kind.Enums, e)
}

func (l *kindLoader) typeFields(d *ast.TypeDecl) {
	if d == nil {
		return
	}
	s := l.decls[d]
	for _, f := range d.Fields {
		key := "type " + s.Name + ".field " + f.Name.Name
		l.set(key, f.Name)
		l.set(key+".type", f.Type)
		s.Fields = append(s.Fields, &types.Field{Name: f.Name.Name, Type: l.resolve(f.Type)})
	}
}

func (l *kindLoader) input(d *ast.InputDecl) {
	if d == nil {
		return
	}
	key := "input " + d.Name.Name
	l.set(key, d.Name)
	l.set(key+".type", d.Type)
	l.kind.Inputs = append(l.kind.Inputs, &kind.Input{Name: d.Name.Name, Type: l.resolve(d.Type)})
}

func (l *kindLoader) fn(d *ast.FnDecl) {
	if d == nil {
		return
	}
	key := "fn " + d.Name.Name
	l.set(key, d.Name)
	l.set(key+".result", d.Result)
	f := &kind.Func{Name: d.Name.Name, Result: l.resolve(d.Result)}
	for i, p := range d.Params {
		l.set(fmt.Sprintf("%s.param %d", key, i+1), p)
		f.Params = append(f.Params, l.resolve(p))
	}
	l.kind.Funcs = append(l.kind.Funcs, f)
}

// decision loads a decision: its reasons and its payload fields with
// their constant defaults. A decision in the old syntax loads the same,
// and is an error whose help is the declaration in the current syntax.
func (l *kindLoader) decision(d *ast.DecisionDecl) {
	if d == nil {
		return
	}
	key := "decision " + d.Name.Name
	l.set(key, d.Name)
	dec := &kind.Decision{Name: d.Name.Name}

	for _, f := range d.Fields {
		fkey := key + ".field " + f.Name.Name
		l.set(fkey, f.Name)
		l.set(fkey+".type", f.Type)
		pf := &kind.Field{Name: f.Name.Name, Type: l.resolve(f.Type)}
		if f.Default != nil {
			l.set(fkey+".default", f.Default)
			// A default that fails to evaluate still counts as one, so the
			// field isn't also reported as required by the default decision.
			pf.HasDefault = true
			if v, err := constant.Eval(f.Default, pf.Type); err != nil {
				err.File = l.c.file
				l.c.errs = append(l.c.errs, err)
			} else {
				pf.Default = v
			}
		}
		dec.Fields = append(dec.Fields, pf)
	}
	for _, r := range d.Reasons {
		l.set(key+".reason "+r.Name, r)
		dec.Reasons = append(dec.Reasons, r.Name)
	}
	l.kind.Decisions = append(l.kind.Decisions, dec)
	if d.Legacy {
		l.legacy(d, dec)
	}
}

// legacy reports a decision in the old syntax, `decision name(fields) {
// reasons }`. The message sketches the new shape with the decision's own
// field names. The help is the whole declaration in the new syntax on one
// line, fields two spaces apart and defaults as written, which is what
// `sigil fmt --write` rewrites it to, one field per line.
func (l *kindLoader) legacy(d *ast.DecisionDecl, dec *kind.Decision) {
	shape := make([]string, 0, 1+len(d.Fields))
	shape = append(shape, "reason: …")
	fields := make([]string, 0, 1+len(d.Fields))
	fields = append(fields, "reason: "+strings.Join(dec.Reasons, " | "))
	for i, f := range d.Fields {
		shape = append(shape, f.Name.Name+": …")
		field := f.Name.Name + ": " + dec.Fields[i].Type.String()
		if f.Default != nil {
			field += " = " + ast.Sprint(f.Default)
		}
		fields = append(fields, field)
	}
	l.c.errorf(d.Name, fmt.Sprintf("write `decision %s { %s }`, or run `sigil fmt --write`", dec.Name, strings.Join(fields, "  ")),
		"old decision syntax; write `decision %s { %s }`", dec.Name, strings.Join(shape, " "))
}

func (l *kindLoader) precedence(d *ast.PrecedenceDecl) {
	if d == nil {
		return
	}
	if d.Scope != nil {
		l.reasonPrecedence(d)
		return
	}
	if l.once(&l.precedenceAt, d, "precedence") {
		return
	}
	l.set("precedence", d)
	for _, n := range d.Names {
		l.set("precedence."+n.Name, n)
		l.kind.Precedence = append(l.kind.Precedence, n.Name)
	}
}

// reasonPrecedence loads `precedence d: a > b`, which ranks the reasons
// of decision d. The decision must exist, and it takes one ranking.
func (l *kindLoader) reasonPrecedence(d *ast.PrecedenceDecl) {
	dec := l.kind.Decision(d.Scope.Name)
	if dec == nil {
		l.c.errorf(d.Scope, "a scoped precedence ranks the reasons of one of the kind's decisions", "precedence: undeclared decision %q", d.Scope.Name)
		return
	}
	if dec.Ranked != nil {
		l.c.errorf(d, fmt.Sprintf("a decision's reasons are ranked once; merge the two `precedence %s:` lines", dec.Name), "precedence %s is declared twice", dec.Name)
		return
	}
	key := "precedence " + dec.Name
	l.set(key, d)
	dec.Ranked = []string{}
	for _, n := range d.Names {
		l.set(key+"."+n.Name, n)
		dec.Ranked = append(dec.Ranked, n.Name)
	}
}

// exclusive loads `exclusive a, b.x`.
func (l *kindLoader) exclusive(d *ast.ExclusiveDecl) {
	if d == nil {
		return
	}
	key := fmt.Sprintf("exclusive %d", len(l.kind.Exclusive)+1)
	l.set(key, d)
	set := make([]kind.Outcome, 0, len(d.Outcomes))
	for i, o := range d.Outcomes {
		l.set(fmt.Sprintf("%s.%d", key, i+1), o)
		out := kind.Outcome{Decision: o.Decision.Name}
		if o.Reason != nil {
			out.Reason = o.Reason.Name
		}
		set = append(set, out)
	}
	l.kind.Exclusive = append(l.kind.Exclusive, set)
}

func (l *kindLoader) collect(d *ast.CollectDecl) {
	if d == nil {
		return
	}
	if l.once(&l.collectAt, d, "collect") {
		return
	}
	l.set("collect", d)
	l.kind.Collect = kind.CollectOne
	if d.All {
		l.kind.Collect = kind.CollectAll
	}
}

// once reports and skips a second declaration of something a kind has one
// of.
func (l *kindLoader) once(at *ast.Node, d ast.Node, what string) bool {
	if at == nil {
		return false
	}
	if *at != nil {
		l.c.errorf(d, fmt.Sprintf("a kind declares `%s` once; remove one", what), "%s is declared twice", what)
		return true
	}
	*at = d
	return false
}

// defaultDecl loads `default deny(reason: no_rule_matched, field: value)`.
// The reason follows a constructor's rules, and each argument must be a
// constant of its payload field's type, given once.
func (l *kindLoader) defaultDecl(d *ast.DefaultDecl) {
	if d == nil {
		return
	}
	if l.once(&l.defaultAt, d, "default") {
		return
	}
	l.kind.Default = l.constructor(d, d.Call, "default")
}

// conflictDecl loads `conflict deny(reason: conflicting_rules)`, which
// follows the default's rules. Whether the kind may declare one at all
// depends on its collect mode, which kind.Validate checks once every
// declaration is loaded.
func (l *kindLoader) conflictDecl(d *ast.ConflictDecl) {
	if d == nil {
		return
	}
	if l.once(&l.conflictAt, d, "conflict") {
		return
	}
	l.kind.Conflict = l.constructor(d, d.Call, "conflict")
}

// constructor loads the constructor call of the declaration d, `default`
// or `conflict` as key says: its decision, a labeled reason that follows a
// constructor's rules, and a constant for each payload argument.
func (l *kindLoader) constructor(d ast.Node, call *ast.CallStmt, key string) *kind.Default {
	l.set(key, d)
	def := &kind.Default{Decision: call.Name.Name, Args: map[string]any{}}

	// The reason's key points at whatever is wrong with it, so the model's
	// finding about it lands on the loader's error and is dropped.
	decl := l.kind.Decision(def.Decision)
	l.set(key+".reason", reasonNode(call))
	if decl == nil {
		// The unknown decision is reported by kind.Validate; the reason
		// can't be checked without it.
		if r, ok := reasonNode(call).(*ast.Ident); ok {
			def.Reason = r.Name
		}
	} else if r := l.c.reasonArg(call, decl, nil); r != nil {
		def.Reason = r.Name
	}

	for _, arg := range call.Args {
		name := arg.Name.Name
		if name == reasonField {
			continue
		}
		l.set(key+".arg "+name, arg.Name)
		if _, dup := def.Args[name]; dup {
			l.c.errorf(arg.Name, "pass each field once", "field %q is given twice", name)
			continue
		}
		if decl == nil {
			continue
		}
		f := decl.Field(name)
		if f == nil {
			help := decl.Signature()
			if closest, ok := closestField(decl, name); ok {
				help = fmt.Sprintf("did you mean %q? %s", closest, help)
			}
			l.c.errorf(arg.Name, help, "decision %s has no payload field %q", decl.Name, name)
			continue
		}
		v, err := constant.Eval(arg.Value, f.Type)
		if err != nil {
			err.File = l.c.file
			l.c.errs = append(l.c.errs, err)
			// The field was given, so it isn't also reported as missing, and
			// the model's finding about its value lands on this error.
			l.loc[key+".arg "+name] = span{err.Pos, err.End}
		}
		def.Args[name] = v
	}
	return def
}

// reasonNode returns the part of call that gives its reason: the value of
// its first `reason:` argument, else its positional argument, else the
// call itself.
func reasonNode(call *ast.CallStmt) ast.Node { //nolint:returninterface // an argument's value, or the call itself
	for _, arg := range call.Args {
		if arg.Name.Name == reasonField {
			return arg.Value
		}
	}
	if call.Positional != nil {
		return call.Positional
	}
	return call
}

// resolve turns a type expression in the kind into a type.
func (l *kindLoader) resolve(t ast.Type) types.Type {
	return l.c.resolveType(t, l.kind, true)
}

// validate runs the model's rules and anchors each finding: at the span
// the key names, or at the header when the key isn't in the file. A
// finding at the position of a loader error is dropped, since the loader
// already said what's wrong there.
func (l *kindLoader) validate(doc *ast.KindDoc) {
	if doc == nil {
		return
	}
	reported := map[token.Pos]bool{}
	for _, e := range l.c.errs {
		reported[e.Pos] = true
	}
	locate := func(key string) (token.Pos, token.Pos, bool) {
		s, ok := l.loc[key]
		return s.start, s.end, ok
	}
	for _, e := range l.kind.Validate(locate) {
		e.File = l.c.file
		if !e.Pos.IsValid() {
			e.Pos, e.End = doc.Name.Pos(), doc.Version.End()
		}
		if reported[e.Pos] {
			continue
		}
		reported[e.Pos] = true
		l.c.errs = append(l.c.errs, e)
	}
}
