package policy

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/eval"
	"github.com/spechtlabs/sigil/internal/parser"
	"github.com/spechtlabs/sigil/internal/token"
	"github.com/spechtlabs/sigil/internal/types"
)

// bundle is a set of documents being compiled against one kind.
type bundle[In any] struct {
	kind    *Kind[In]
	sources map[string][]byte
	docs    map[string]*document
	order   []string // document names in the order they were read
	errs    diag.ErrorList
}

// document is one policy or module in a bundle, with the file it came
// from and what the checker learned about it.
type document struct {
	info *check.Info
	node ast.Doc
	file string
	name string
}

func newBundle[In any](k *Kind[In]) *bundle[In] {
	return &bundle[In]{kind: k, sources: map[string][]byte{}, docs: map[string]*document{}}
}

// add parses src as file, indexes its documents and checks each one.
func (b *bundle[In]) add(file string, src []byte) {
	b.sources[file] = src
	f, errs := parser.ParseFile(file, src)
	b.errs = append(b.errs, errs...)
	for _, doc := range f.Docs {
		b.index(file, doc)
	}
}

// index records doc by its header name, or reports the name as defined
// twice. A kind document with the host kind's name must match the host's
// contract; one for another kind is ignored.
func (b *bundle[In]) index(file string, doc ast.Doc) {
	c := check.New(file)
	var name *ast.PolicyName
	switch d := doc.(type) {
	case *ast.PolicyDoc:
		name = d.Name
		c.Policy(d, b.kind.kind)
	case *ast.ModuleDoc:
		name = d.Name
		c.Module(d, b.kind.kind)
	case *ast.KindDoc:
		if d.Name.Name == b.kind.kind.Name {
			b.matchKind(c, d)
		}
		b.errs = append(b.errs, c.Errors()...)
		return
	default:
		return
	}
	b.errs = append(b.errs, c.Errors()...)
	if prev, dup := b.docs[name.String()]; dup {
		b.errs = append(b.errs, &diag.Error{
			File: file, Pos: name.Pos(), End: name.End(),
			Msg:  fmt.Sprintf("%s %s is defined twice", describeDoc(doc), name),
			Help: fmt.Sprintf("first defined at %s; documents resolve by name, so each name has one definition", position(prev.file, "", prev.node.Pos())),
		})
		return
	}
	b.docs[name.String()] = &document{name: name.String(), file: file, node: doc, info: c.Info()}
	b.order = append(b.order, name.String())
}

// matchKind checks a kind document against the host's contract.
func (b *bundle[In]) matchKind(c *check.Checker, d *ast.KindDoc) {
	loaded := c.Kind(d)
	if loaded == nil || loaded.Source() == b.kind.kind.Source() {
		return
	}
	c.KindMismatch(d, b.kind.kind.Name)
}

// compile binds the root's params and compiles it, once every document
// has checked.
func (b *bundle[In]) compile(name string, o *loadOptions) (*Policy[In], error) {
	root := b.docs[name]
	switch {
	case root == nil:
		b.errs = append(b.errs, b.noRoot(name))
	case !isPolicy(root.node):
		b.errs = append(b.errs, &diag.Error{
			File: root.file, Pos: root.node.Pos(), End: root.node.Pos(),
			Msg:  fmt.Sprintf("%s is a module, not a policy", name),
			Help: "a module holds only lets and has no rules to evaluate; name a policy",
		})
	}
	if ce := b.err(); ce != nil {
		return nil, ce
	}
	doc := root.node.(*ast.PolicyDoc)
	params := b.params(doc, root.info, o.params)
	if ce := b.err(); ce != nil {
		return nil, ce
	}
	prog, cerr := eval.CompilePolicy(doc, root.file, b.sources[root.file], root.info, b.kind.kind, b.kind.binding, params)
	if cerr != nil {
		b.errs = append(b.errs, cerr)
		return nil, b.err()
	}
	return &Policy[In]{kind: b.kind, prog: prog, name: name}, nil
}

// noRoot describes a root that isn't in the bundle, listing what is.
func (b *bundle[In]) noRoot(name string) *diag.Error {
	var policies []string
	for _, n := range b.order {
		if isPolicy(b.docs[n].node) {
			policies = append(policies, n)
		}
	}
	help := "the bundle defines no policies"
	if len(policies) > 0 {
		help = "the bundle defines: " + strings.Join(policies, ", ")
	}
	return &diag.Error{Msg: fmt.Sprintf("bundle has no policy %s", name), Help: help}
}

// params turns the host's bindings into evaluator values, checking each
// against its param's type and rejecting names the policy doesn't
// declare.
func (b *bundle[In]) params(doc *ast.PolicyDoc, info *check.Info, given Params) map[string]eval.Value {
	declared := map[string]*ast.ParamStmt{}
	names := make([]string, 0, len(given))
	for _, s := range doc.Stmts {
		if p, ok := s.(*ast.ParamStmt); ok {
			declared[p.Name.Name] = p
			names = append(names, p.Name.Name)
		}
	}
	out := map[string]eval.Value{}
	for name, v := range given {
		p, ok := declared[name]
		if !ok {
			help := fmt.Sprintf("%s declares no params", doc.Name)
			if len(names) > 0 {
				help = fmt.Sprintf("%s declares: %s", doc.Name, strings.Join(names, ", "))
			}
			b.errs = append(b.errs, &diag.Error{File: "", Msg: fmt.Sprintf("policy %s has no param %q", doc.Name, name), Help: help})
			continue
		}
		want := info.Params[p]
		file := b.docs[doc.Name.String()].file
		if got, ok := b.conforms(v, want); !ok {
			b.errs = append(b.errs, &diag.Error{
				File: file, Pos: p.Pos(), End: p.End(),
				Msg:  fmt.Sprintf("param %s: expected %s, found %s", name, want, got),
				Help: "Params values are Go values of the shape NewKind accepts for the param's type",
			})
			continue
		}
		if err := b.inBounds(p, want, v); err != nil {
			err.File = file
			b.errs = append(b.errs, err)
			continue
		}
		out[name] = reflect.ValueOf(v)
	}
	return out
}

// inBounds checks a bound value against the param's `min` and `max`.
func (b *bundle[In]) inBounds(p *ast.ParamStmt, t types.Type, v any) *diag.Error {
	if p.Min == nil && p.Max == nil {
		return nil
	}
	got := ordered(v)
	if lo, err := constant.Eval(p.Min, t); p.Min != nil && err == nil && constant.Compare(got, lo) < 0 {
		return &diag.Error{Pos: p.Pos(), End: p.End(),
			Msg:  fmt.Sprintf("param %s: %s is below the minimum %s", p.Name.Name, constant.Format(got), constant.Format(lo)),
			Help: "the policy bounds the param; bind a value it accepts"}
	}
	if hi, err := constant.Eval(p.Max, t); p.Max != nil && err == nil && constant.Compare(got, hi) > 0 {
		return &diag.Error{Pos: p.Pos(), End: p.End(),
			Msg:  fmt.Sprintf("param %s: %s is above the maximum %s", p.Name.Name, constant.Format(got), constant.Format(hi)),
			Help: "the policy bounds the param; bind a value it accepts"}
	}
	return nil
}

// ordered returns a Go value of an ordered type in the constant
// representation constant.Compare takes.
func ordered(v any) any { //nolint:emptyinterface // constants are typed by their Sigil type; see constant.Conforms
	switch x := v.(type) {
	case time.Duration, int64, float64:
		return x
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int64:
		return rv.Int()
	case reflect.Float64:
		return rv.Float()
	}
	return v
}

// conforms reports whether v, a Go value bound to a param, is a t: either
// a constant in the evaluator's representation or a Go value whose type
// maps to t. On failure it describes what was found.
func (b *bundle[In]) conforms(v any, t types.Type) (string, bool) {
	if v == nil {
		return "nil", false
	}
	if constant.Conforms(v, t) {
		return "", true
	}
	got, ok := b.kind.binding.TypeOf(reflect.TypeOf(v))
	if !ok {
		return fmt.Sprintf("Go type %T", v), false
	}
	if !types.Identical(got, t) {
		return got.String(), false
	}
	return "", true
}

// err returns the diagnostics so far as a *CompileError, or nil.
func (b *bundle[In]) err() *CompileError {
	if len(b.errs) == 0 {
		return nil
	}
	b.errs.Sort()
	e := &CompileError{}
	var rendered []string
	for _, d := range b.errs {
		e.Diagnostics = append(e.Diagnostics, Diagnostic{
			Message:  d.Msg,
			Help:     d.Help,
			Position: position(d.File, b.docAt(d.File, d.Pos), d.Pos),
			End:      position(d.File, "", d.End),
		})
		rendered = append(rendered, strings.TrimRight(diag.Render(d, b.sources[d.File]), "\n"))
	}
	e.rendered = strings.Join(rendered, "\n")
	return e
}

// docAt returns the name of the document at p in file, or "" when p
// isn't a position or no document covers it.
func (b *bundle[In]) docAt(file string, p token.Pos) string {
	if !p.IsValid() {
		return ""
	}
	offset := p.Offset
	for _, name := range b.order {
		d := b.docs[name]
		if d.file == file && d.node.Pos().Offset <= offset && offset < d.node.End().Offset {
			return name
		}
	}
	return ""
}

// describeDoc names a document's kind for a message.
func describeDoc(d ast.Doc) string {
	if _, ok := d.(*ast.ModuleDoc); ok {
		return "module"
	}
	return "policy"
}

func isPolicy(d ast.Doc) bool {
	_, ok := d.(*ast.PolicyDoc)
	return ok
}
