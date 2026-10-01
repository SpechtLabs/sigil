package build

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/spechtlabs/sigil/internal/contract"
	"github.com/spechtlabs/sigil/pkg/policy"
)

// Doc is a module or policy built in Go: a [*ModuleDoc] or a
// [*PolicyDoc]. Its source is rendered on demand, in `sigil fmt`'s
// canonical style.
type Doc interface {
	Importable
	// Path is where the rendered file goes, relative to a policy
	// directory: the name with its dots as slashes, deploy.freeze in
	// deploy/freeze.sigil, unless [WithPath] sets it.
	Path() string
	// Source renders the document. The error is [Errors], every mistake
	// in the Go code that built it, each with its call site.
	Source() ([]byte, error)
	document() *document
}

// Importable is a document another one can read pub lets from with
// [Ref]: a [Doc], or an [*ExternDoc] for one written by hand.
type Importable interface {
	// Name returns the name in the document's header, like deploy.freeze.
	Name() string
	built() *document // nil for a document written by hand
}

// Invocable is a policy another one can invoke with [Block.Invoke]: a
// [*PolicyDoc], or an [*ExternDoc] for one written by hand.
type Invocable interface {
	Importable
	invocable()
}

// Scope is where a [Let] goes: a [*ModuleDoc] or [*PolicyDoc] for a
// top-level let, or a [*Block] for a let scoped to a `when` body.
type Scope interface {
	scope() *body
}

// TopScope is the top level of a document, where a [Pub] let goes: a
// [*ModuleDoc] or a [*PolicyDoc].
type TopScope interface {
	Scope
	topLevel() *body
}

// ParamScope is where a [Param] goes: a [*PolicyDoc]. Modules have no
// params.
type ParamScope interface {
	TopScope
	params() *body
}

// DocOption configures [Module] and [Policy]: [WithPath] and
// [WithHeader].
type DocOption interface {
	apply(d *document)
}

// ModuleDoc is a module built in Go: imports and lets, no rules. Build
// one with [Module].
type ModuleDoc[In any] struct {
	d *document
}

// PolicyDoc is a policy built in Go: params, lets and rules. Build one
// with [Policy]. Its rule statements are those of a [Block].
type PolicyDoc[In any] struct {
	d *document
}

// Block is the body of a `when`, which [Block.When] and [PolicyDoc.When]
// hand to the function that builds it.
type Block struct {
	b *body
}

// ExternDoc is a module or policy written by hand, named so a document
// built in Go can import its pub lets with [Ref] or invoke it with
// [Block.Invoke]. Build one with [Extern].
type ExternDoc struct {
	err  *Error
	name string
}

// Argument is a named argument of a decision or an invocation, built with
// [Arg].
type Argument struct {
	x    node
	name string
	s    Site
}

// ParamOption is a param's default or bound: [Default], [Min] or [Max].
type ParamOption[T any] struct {
	v    T
	s    Site
	kind int
}

// The kinds of ParamOption.
const (
	optDefault = iota
	optMin
	optMax
)

// document is what a module and a policy built in Go have in common,
// whatever their input type.
type document struct {
	contract *contract.Kind
	shadow   *root
	top      *body
	names    map[string]Site // the lets and params, for names declared twice
	name     string
	path     string
	header   string
	errs     Errors
	site     Site
	module   bool
}

// body is a list of statements: a document's top level, or a `when`
// body.
type body struct {
	doc    *document
	parent *body // the body around a `when` body; nil at the top level
	stmts  []stmt
}

// pathOption is WithPath.
type pathOption struct {
	path string
	s    Site
}

// headerOption is WithHeader.
type headerOption string

// Module builds the module called name, against kind k: body declares
// its lets, reading the input through in. The header pins k's current
// version.
//
//	freeze := build.Module("deploy.freeze", deploy.Kind, func(m *build.ModuleDoc[deploy.Input], in *deploy.Input) {
//		build.Pub(m, "is_frozen", build.Or(
//			build.Field(&in.Freeze.Unknown),
//			build.In(build.Field(&in.Environment), build.Field(&in.Freeze.Environments)),
//		))
//	})
//
// Mistakes in the body are collected, never panicked on, and reported by
// [ModuleDoc.Source] with their Go call sites.
func Module[In any](name string, k *policy.Kind[In], body func(m *ModuleDoc[In], in *In), opts ...DocOption) *ModuleDoc[In] {
	d, in := newDocument(callSite("build.Module"), name, k, true, opts)
	m := &ModuleDoc[In]{d: d}
	if body != nil {
		body(m, in)
	}
	return m
}

// Policy builds the policy called name, against kind k: body declares its
// params, lets and rules, reading the input through in. The header pins
// k's current version.
func Policy[In any](name string, k *policy.Kind[In], body func(p *PolicyDoc[In], in *In), opts ...DocOption) *PolicyDoc[In] {
	d, in := newDocument(callSite("build.Policy"), name, k, false, opts)
	p := &PolicyDoc[In]{d: d}
	if body != nil {
		body(p, in)
	}
	return p
}

// WithPath sets the path of the rendered file, relative to a policy
// directory, such as "platform/deploy/freeze.sigil". It must be a valid
// [io/fs] path ending in `.sigil`.
func WithPath(path string) DocOption {
	return pathOption{path: path, s: callSite("build.WithPath")}
}

// WithHeader sets the comment the rendered file starts with, one `//`
// line per line of text. The default says the file is generated, and from
// which Go file, so a reviewer knows where to change it:
//
//	// Code generated by pkg/build from freeze.go. DO NOT EDIT.
//
// An empty text leaves the comment out.
func WithHeader(text string) DocOption {
	return headerOption(text)
}

// Extern names a module or policy written by hand, for [Ref] and
// [Block.Invoke].
func Extern(name string) *ExternDoc {
	s := callSite("build.Extern")
	e := &ExternDoc{name: name}
	if msg := nameError(name); msg != "" {
		e.err = s.errorf("%s", msg)
	}
	return e
}

// Let declares `let name = x` in s and returns the let, to read wherever
// it's in scope. In a [*Block], the let is scoped to that `when` body and
// the bodies nested in it.
func Let[T any](s Scope, name string, x Expr[T]) Expr[T] {
	st := callSite("build.Let")
	var b *body
	if s != nil {
		b = s.scope()
	}
	return Expr[T]{declareLet(st, b, name, x.n, false)}
}

// Pub declares `pub let name = x` at the top level of a document, which
// other documents can import with [Ref], and returns the let.
func Pub[T any](s TopScope, name string, x Expr[T]) Expr[T] {
	st := callSite("build.Pub")
	var b *body
	if s != nil {
		b = s.topLevel()
	}
	return Expr[T]{declareLet(st, b, name, x.n, true)}
}

// Param declares `param name: type` in a policy and returns the param.
// The Sigil type is the one T maps to under the kind; an optional can't
// be a param. Without [Default], invoking the policy must bind it.
//
//	build.Param(p, "min_soak", build.Default(24*time.Hour), build.Min(time.Hour), build.Max(48*time.Hour))
//
// renders `param min_soak: duration = 24h, min: 1h, max: 48h`.
func Param[T any](p ParamScope, name string, opts ...ParamOption[T]) Expr[T] {
	s := callSite("build.Param")
	var b *body
	if p != nil {
		b = p.params()
	}
	if b == nil {
		return Expr[T]{&errNode{s.errorf("the policy is nil")}}
	}
	t := reflect.TypeFor[T]()
	ps := &paramStmt{name: name, t: t, s: s, doc: b.doc}
	for _, o := range opts {
		lit := &litNode{v: reflect.ValueOf(&o.v).Elem(), t: t, s: o.s}
		slot, what := &ps.def, "a default"
		switch o.kind {
		case optMin:
			slot, what = &ps.min, "a minimum"
		case optMax:
			slot, what = &ps.max, "a maximum"
		}
		if *slot != nil {
			b.doc.errs = append(b.doc.errs, o.s.errorf("param %s has %s already", name, what))
			continue
		}
		*slot = lit
	}
	b.doc.declare(s, name)
	b.add(ps)
	return Expr[T]{&paramRef{param: ps, s: s}}
}

// Default is a param's default, which an invocation may leave out.
func Default[T any](v T) ParamOption[T] {
	return ParamOption[T]{v: v, s: callSite("build.Default"), kind: optDefault}
}

// Min is a param's inclusive lower bound, for an int, float or duration.
func Min[T any](v T) ParamOption[T] {
	return ParamOption[T]{v: v, s: callSite("build.Min"), kind: optMin}
}

// Max is a param's inclusive upper bound, for an int, float or duration.
func Max[T any](v T) ParamOption[T] {
	return ParamOption[T]{v: v, s: callSite("build.Max"), kind: optMax}
}

// Arg is the argument `name: x` of a decision or an invocation. A
// decision's reason isn't one; it comes from the [policy.Outcome].
func Arg[T any](name string, x Expr[T]) Argument {
	return Argument{name: name, x: x.n, s: callSite("build.Arg")}
}

// Ref reads the pub let called name of another document, and generates
// the `use` that imports it: `use deploy.freeze.{is_frozen}`, or, when the
// document also invokes d, the whole import `use deploy.guardrails` and
// the qualified name `guardrails.is_hotfix`. For a document built in Go,
// a name it doesn't export is an error when the document renders.
func Ref[T any](d Importable, name string) Expr[T] {
	s := callSite("build.Ref")
	if d == nil {
		return Expr[T]{&errNode{s.errorf("the document is nil")}}
	}
	return Expr[T]{&refNode{target: d, name: name, s: s}}
}

// Name returns the module's name.
func (m *ModuleDoc[In]) Name() string { return m.d.name }

// Path returns where the rendered file goes; see [Doc].
func (m *ModuleDoc[In]) Path() string { return m.d.path }

// Source renders the module; see [Doc].
func (m *ModuleDoc[In]) Source() ([]byte, error) { return m.d.source() }

// Comment puts a comment line before the next statement, or at the end
// of the module when none follows. A text of several lines is several
// comment lines.
func (m *ModuleDoc[In]) Comment(text string) {
	m.d.top.comment(text)
}

// Name returns the policy's name.
func (p *PolicyDoc[In]) Name() string { return p.d.name }

// Path returns where the rendered file goes; see [Doc].
func (p *PolicyDoc[In]) Path() string { return p.d.path }

// Source renders the policy; see [Doc].
func (p *PolicyDoc[In]) Source() ([]byte, error) { return p.d.source() }

// Comment puts a comment line before the next statement; see
// [ModuleDoc.Comment].
func (p *PolicyDoc[In]) Comment(text string) {
	p.d.top.comment(text)
}

// When adds the rule `when cond { … }` at the top level; see
// [Block.When].
func (p *PolicyDoc[In]) When(cond Expr[bool], body func(b *Block)) {
	p.d.top.when(callSite("When"), cond.n, body)
}

// Assert adds `assert("reason", cond)` at the top level; see
// [Block.Assert].
func (p *PolicyDoc[In]) Assert(reason string, cond Expr[bool]) {
	p.d.top.assert(callSite("Assert"), reason, cond.n)
}

// Decide constructs a decision at the top level; see [Block.Decide].
func (p *PolicyDoc[In]) Decide(o policy.Outcome, args ...Argument) {
	p.d.top.decide(callSite("Decide"), o, args)
}

// Invoke invokes a policy at the top level, so it applies
// unconditionally; see [Block.Invoke].
func (p *PolicyDoc[In]) Invoke(target Invocable, args ...Argument) {
	p.d.top.invoke(callSite("Invoke"), target, args)
}

// When adds the nested rule `when cond { … }`: body builds its body,
// which applies when cond and every enclosing condition hold.
func (b *Block) When(cond Expr[bool], body func(b *Block)) {
	b.b.when(callSite("When"), cond.n, body)
}

// Assert adds `assert("reason", cond)`: cond must hold whenever the
// assert is reached, or the evaluation fails.
func (b *Block) Assert(reason string, cond Expr[bool]) {
	b.b.assert(callSite("Assert"), reason, cond.n)
}

// Decide constructs a decision: the decision and reason of o, which a
// [policy.Decision.Reason] handle names, and the payload fields:
//
//	b.Decide(deploy.Deny.Reason("change_freeze"))
//	b.Decide(deploy.Review.Reason("service_owner"), build.Arg("approvers", approvers))
//
// render `deny(reason: change_freeze)` and `review(reason: service_owner,
// approvers: approvers)`.
func (b *Block) Decide(o policy.Outcome, args ...Argument) {
	b.b.decide(callSite("Decide"), o, args)
}

// Invoke invokes a policy with its params bound by name, as
// `guardrails(min_soak: 4h)`, and generates the `use` that imports it.
// Arguments are constants and the invoking policy's own params.
func (b *Block) Invoke(target Invocable, args ...Argument) {
	b.b.invoke(callSite("Invoke"), target, args)
}

// Comment puts a comment line before the next statement of the body; see
// [ModuleDoc.Comment].
func (b *Block) Comment(text string) {
	b.b.comment(text)
}

// Name returns the document's name, as given to [Extern].
func (e *ExternDoc) Name() string { return e.name }

func (m *ModuleDoc[In]) built() *document    { return m.d }
func (m *ModuleDoc[In]) document() *document { return m.d }
func (m *ModuleDoc[In]) scope() *body        { return m.topLevel() }

func (m *ModuleDoc[In]) topLevel() *body {
	if m == nil {
		return nil
	}
	return m.d.top
}

func (p *PolicyDoc[In]) built() *document    { return p.d }
func (p *PolicyDoc[In]) document() *document { return p.d }
func (p *PolicyDoc[In]) invocable()          {}
func (p *PolicyDoc[In]) scope() *body        { return p.topLevel() }
func (p *PolicyDoc[In]) params() *body       { return p.topLevel() }

func (p *PolicyDoc[In]) topLevel() *body {
	if p == nil {
		return nil
	}
	return p.d.top
}

func (b *Block) scope() *body {
	if b == nil {
		return nil
	}
	return b.b
}

func (e *ExternDoc) built() *document { return nil }
func (e *ExternDoc) invocable()       {}

func (o pathOption) apply(d *document) {
	if !fs.ValidPath(o.path) || filepath.Ext(o.path) != ".sigil" {
		d.errs = append(d.errs, o.s.errorf("%q isn't a path for a policy file: write a slash-separated relative path ending in .sigil", o.path))
		return
	}
	d.path = o.path
}

func (o headerOption) apply(d *document) { d.header = string(o) }

// newDocument starts a document built by the call at s, and returns it
// with the shadow input its body reads.
func newDocument[In any](s Site, name string, k *policy.Kind[In], module bool, opts []DocOption) (*document, *In) {
	d := &document{
		name:   name,
		path:   strings.ReplaceAll(name, ".", "/") + ".sigil",
		header: "Code generated by pkg/build from " + filepath.Base(s.File) + ". DO NOT EDIT.",
		names:  map[string]Site{},
		site:   s,
		module: module,
	}
	d.top = &body{doc: d}
	if msg := nameError(name); msg != "" {
		d.errs = append(d.errs, s.errorf("%s", msg))
	}
	if k == nil {
		d.errs = append(d.errs, s.errorf("the kind is nil"))
	} else {
		d.contract = k.Contract()
	}
	for _, o := range opts {
		if o != nil {
			o.apply(d)
		}
	}
	in := newShadow[In]()
	d.shadow = &root{v: reflect.ValueOf(in).Elem()}
	return d, in
}

// declare records name as a let or param of the document, reporting an
// invalid name or one declared before.
func (d *document) declare(s Site, name string) {
	if msg := identError(name); msg != "" {
		d.errs = append(d.errs, s.errorf("%s", msg))
		return
	}
	if prev, ok := d.names[name]; ok {
		d.errs = append(d.errs, s.errorf("%s is already declared at %s; every name in a document means one thing", name, prev.at()))
		return
	}
	d.names[name] = s
}

// pub reports whether the document's top level declares a pub let called
// name, and lists those it declares.
func (d *document) pub(name string) (bool, []string) {
	var names []string
	found := false
	for _, s := range d.top.stmts {
		if l, ok := s.(*letStmt); ok && l.pub {
			names = append(names, l.name)
			found = found || l.name == name
		}
	}
	return found, names
}

// declareLet adds a let to b and returns the node that reads it.
func declareLet(st Site, b *body, name string, x node, pub bool) node {
	if b == nil {
		return &errNode{st.errorf("the scope is nil; pass the document or block the let belongs to")}
	}
	l := &letStmt{name: name, x: x, s: st, scope: b, pub: pub}
	b.doc.declare(st, name)
	b.add(l)
	return &letRef{let: l}
}

// nameError says why name can't be a document's name, or returns "" when
// it can.
func nameError(name string) string {
	if name == "" {
		return "the document name is empty"
	}
	for part := range strings.SplitSeq(name, ".") {
		if msg := identError(part); msg != "" {
			return "document name " + name + ": " + msg
		}
	}
	return ""
}
