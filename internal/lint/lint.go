// Package lint finds the patterns `sigil check` warns about: code that
// compiles and does what it says, but probably not what its author meant.
// Each lint has a name, a default level, and a place in the lint reference
// https://sigil.specht-labs.de/reference/lints/, which describes when it fires.
//
// [Run] lints only the documents that checked cleanly, because the lints
// read what the checker recorded about each one in its check.Info. The
// lints are:
//
//   - unused-import: a `use` binds a name nothing reads or invokes.
//   - shadowed-kind-name: a document keeps a name the kind has since given
//     to an input, host function or decision.
//   - unused-let: a private let is never read.
//   - gated-assert: a policy holding asserts, directly or through its own
//     invocations, is invoked under `when` and the host doesn't require it.
//   - gated-deny: the same for a policy holding constructors of the
//     decision a `collect one` kind ranks highest.
//   - duplicate-invocation: a policy is invoked twice with the same
//     arguments, in any order.
//   - qualified-imports: a selective import is used. Off by default.
//   - path-matches-name: a document's name doesn't match its file's path.
//     Off by default.
//
// A repository sets each lint's [Level] in sigil.yaml, which the CLI reads
// into [Options.Levels].
package lint

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/bundle"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/kind"
)

// Levels a lint can be set to.
const (
	Off   Level = iota // not reported
	Warn               // reported as a warning, which doesn't fail the check
	Error              // reported as an error, which fails the check
)

// Lint names, as findings, `sigil check` output and sigil.yaml spell them.
const (
	UnusedImport        = "unused-import"
	ShadowedKindName    = "shadowed-kind-name"
	UnusedLet           = "unused-let"
	GatedAssert         = "gated-assert"
	GatedDeny           = "gated-deny"
	DuplicateInvocation = "duplicate-invocation"
	QualifiedImports    = "qualified-imports"
	PathMatchesName     = "path-matches-name"
)

// All lists every lint with its default level, in the order the CLI
// reference documents them. Callers must not modify it.
var All = []Lint{
	{Name: UnusedImport, Default: Warn},
	{Name: ShadowedKindName, Default: Warn},
	{Name: UnusedLet, Default: Warn},
	{Name: GatedAssert, Default: Warn},
	{Name: GatedDeny, Default: Warn},
	{Name: DuplicateInvocation, Default: Warn},
	{Name: QualifiedImports, Default: Off},
	{Name: PathMatchesName, Default: Off},
}

// Level is how a lint is reported: not at all, as a warning, or as an
// error that fails `sigil check`.
type Level int

// Lint is one lint and its default level.
type Lint struct {
	Name    string
	Default Level
}

// Finding is one lint report. The embedded diagnostic carries the
// position, message and help; its Code is the lint's name and its
// Severity follows Level.
type Finding struct {
	*diag.Error
	Lint  string // the lint's name
	Level Level  // Warn or Error, never Off
}

// Options configures [Run].
type Options struct {
	Kind *kind.Kind // the kind the bundle was checked against; required
	// Levels overrides the default level of the lints it names. An unknown
	// name has no effect here; loading sigil.yaml rejects one.
	Levels map[string]Level
	// Required names the policies the host requires, which may be gated
	// without gated-assert or gated-deny firing: a host rejects a gated
	// required policy anyway.
	Required []string
}

// Names lists every lint's name, in the order of [All].
func Names() []string {
	names := make([]string, len(All))
	for i, l := range All {
		names[i] = l.Name
	}
	return names
}

// ParseLevel reads a level as a configuration spells it: `off`, `warn` or
// `error`. It reports false for anything else, such as `Warn`.
func ParseLevel(s string) (Level, bool) {
	switch s {
	case "off":
		return Off, true
	case "warn":
		return Warn, true
	case "error":
		return Error, true
	}
	return Off, false
}

// Run lints the bundle's own documents that checked cleanly, and returns
// the findings of every lint that isn't off, sorted by file and position.
// It doesn't modify the bundle.
func Run(b *bundle.Bundle, o Options) []Finding {
	l := &linter{b: b, o: o, contains: map[string]*contents{}}
	for _, d := range b.Documents() {
		if !d.Clean() {
			continue
		}
		l.doc(d)
	}
	sort.SliceStable(l.out, func(i, j int) bool {
		a, c := l.out[i], l.out[j]
		if a.File != c.File {
			return a.File < c.File
		}
		return a.Pos.Offset < c.Pos.Offset
	})
	return l.out
}

// String implements [fmt.Stringer]. It returns the level as a
// configuration spells it, and "off" for an unknown level.
func (lv Level) String() string {
	switch lv {
	case Warn:
		return "warn"
	case Error:
		return "error"
	}
	return "off"
}

// linter is one Run.
type linter struct {
	b        *bundle.Bundle
	contains map[string]*contents // by policy name, computed on first use
	o        Options
	out      []Finding
}

// contents is what a policy holds, directly or through the policies it
// invokes.
type contents struct {
	asserts bool
	denies  bool
}

// level returns the configured level of a lint.
func (l *linter) level(name string) Level {
	if lv, ok := l.o.Levels[name]; ok {
		return lv
	}
	for _, lint := range All {
		if lint.Name == name {
			return lint.Default
		}
	}
	return Off
}

// reportf records a finding if its lint isn't off.
func (l *linter) reportf(lint string, d *bundle.Document, at ast.Node, help, format string, args ...any) {
	lv := l.level(lint)
	if lv == Off {
		return
	}
	sev := diag.SeverityWarning
	if lv == Error {
		sev = diag.SeverityError
	}
	l.out = append(l.out, Finding{
		Error: &diag.Error{File: d.File, Doc: d.Name, Pos: at.Pos(), End: at.End(), Msg: fmt.Sprintf(format, args...), Help: help, Code: lint, Severity: sev},
		Lint:  lint,
		Level: lv,
	})
}

// doc runs every lint over one document.
func (l *linter) doc(d *bundle.Document) {
	var uses []*ast.UseStmt
	var stmts []ast.Stmt
	var header ast.Node
	switch n := d.Node.(type) {
	case *ast.PolicyDoc:
		uses, stmts, header = n.Uses, n.Stmts, n.Name
	case *ast.ModuleDoc:
		uses, header = n.Uses, n.Name
		for _, let := range n.Lets {
			stmts = append(stmts, let)
		}
	default:
		return
	}
	l.unusedImports(d, uses)
	l.qualifiedImports(d, uses)
	l.unusedLets(d, stmts)
	l.shadows(d)
	l.invocations(d, stmts)
	l.pathMatchesName(d, header)
}

// unusedImports reports imports nothing reads or invokes. A whole import
// is read through its qualifier, `common.owns`, and a selective one
// through the name it binds, so the two are told apart when a document
// imports one document both ways.
func (l *linter) unusedImports(d *bundle.Document, uses []*ast.UseStmt) {
	qualified := map[string]bool{} // docs read through a qualifier
	bound := map[string]bool{}     // local names of selectively imported lets that are read
	for x, rd := range d.Info.Reads {
		switch x := x.(type) {
		case *ast.SelectorExpr:
			qualified[rd.Doc] = true
		case *ast.Ident:
			bound[x.Name] = true
		}
	}
	invoked := map[string]bool{}
	for _, target := range d.Info.Invocations {
		invoked[target] = true
	}
	for _, u := range uses {
		path := u.Path.String()
		if u.Items == nil && !qualified[path] && !invoked[path] {
			l.reportf(UnusedImport, d, u, "remove the import, or use what it binds",
				"%s is imported but never used", path)
		}
		for _, item := range u.Items {
			name, at := item.Name.Name, ast.Node(item.Name)
			if item.Alias != nil {
				name, at = item.Alias.Name, item.Alias
			}
			if bound[name] {
				continue
			}
			l.reportf(UnusedImport, d, at, "remove it from the import list",
				"%s is imported from %s but never used", name, path)
		}
	}
}

func (l *linter) qualifiedImports(d *bundle.Document, uses []*ast.UseStmt) {
	for _, u := range uses {
		if u.Items == nil {
			continue
		}
		last := u.Path.Parts[len(u.Path.Parts)-1].Name
		l.reportf(QualifiedImports, d, u, fmt.Sprintf("import the whole document, `use %s`, and qualify each name, `%s.%s`", u.Path, last, u.Items[0].Name.Name),
			"selective import of %s", u.Path)
	}
}

// unusedLets reports private lets nothing in the document reads. Names
// in a document are unique and nothing shadows, so a let is read when
// its name appears as an identifier anywhere in an expression.
func (l *linter) unusedLets(d *bundle.Document, stmts []ast.Stmt) {
	var lets []*ast.LetStmt
	seen := map[string]bool{}
	walkStmts(stmts, func(s ast.Stmt) {
		if let, ok := s.(*ast.LetStmt); ok {
			lets = append(lets, let)
		}
		for _, x := range exprsOf(s) {
			names(x, seen)
		}
	})
	for _, let := range lets {
		if !let.Pub && !seen[let.Name.Name] {
			l.reportf(UnusedLet, d, let.Name, "remove the let, or read it; a private let can't be imported",
				"let %s is never read", let.Name.Name)
		}
	}
}

func (l *linter) shadows(d *bundle.Document) {
	for _, id := range d.Info.Shadows {
		what := "name"
		switch {
		case l.o.Kind.Input(id.Name) != nil:
			what = "input"
		case l.o.Kind.Func(id.Name) != nil:
			what = "host function"
		case l.o.Kind.Decision(id.Name) != nil:
			what = "decision"
		}
		l.reportf(ShadowedKindName, d, id,
			fmt.Sprintf("the kind's %s is out of reach here; rename %s and raise the document's pin to %s@%d", what, id.Name, l.o.Kind.Name, l.o.Kind.Version),
			"%s shadows the kind's %s %s, added after the version this document pins", id.Name, what, id.Name)
	}
}

// invocations reports gated invocations of policies with asserts or
// denies, and invocations that repeat an earlier one exactly.
func (l *linter) invocations(d *bundle.Document, stmts []ast.Stmt) {
	l.invocationsIn(d, stmts, false, map[string]*ast.CallStmt{})
}

// invocationsIn checks the invocations among stmts; gated is set inside
// a `when`, and seen holds the document's invocations so far by callee
// and arguments.
func (l *linter) invocationsIn(d *bundle.Document, stmts []ast.Stmt, gated bool, seen map[string]*ast.CallStmt) {
	for _, s := range stmts {
		switch s := s.(type) {
		case *ast.WhenStmt:
			l.invocationsIn(d, s.Body, true, seen)
		case *ast.CallStmt:
			target, ok := d.Info.Invocations[s]
			if !ok {
				continue
			}
			key := target + "(" + args(s) + ")"
			if first, dup := seen[key]; dup {
				l.reportf(DuplicateInvocation, d, s,
					fmt.Sprintf("the first is on line %d; every rule it holds already fires once, so remove one, or change its arguments", first.Pos().Line),
					"%s is invoked twice with the same arguments", target)
			} else {
				seen[key] = s
			}
			if gated && !slices.Contains(l.o.Required, target) {
				l.gated(d, s, target)
			}
		}
	}
}

// gated reports a gated invocation of a policy that holds asserts or
// denies.
func (l *linter) gated(d *bundle.Document, s *ast.CallStmt, target string) {
	c := l.contents(target)
	if c.asserts {
		l.reportf(GatedAssert, d, s, "its asserts only run while the condition holds; invoke it at the top level, or have the host require it",
			"%s holds asserts and is invoked under `when`", target)
	}
	if c.denies {
		deny := l.o.Kind.Precedence[0]
		l.reportf(GatedDeny, d, s, fmt.Sprintf("its %s rules only fire while the condition holds; invoke it at the top level, or have the host require it", deny),
			"%s holds %s rules and is invoked under `when`", target, deny)
	}
}

// contents computes what a policy holds, through its invocations too.
// A deny is a constructor of the decision a `collect one` kind ranks
// highest; a kind without a ranking has none.
func (l *linter) contents(name string) *contents {
	if c, ok := l.contains[name]; ok {
		return c
	}
	c := &contents{}
	l.contains[name] = c // an invocation cycle is a compile error; this just stops the walk
	d := l.b.Document(name)
	if d == nil || d.Info == nil {
		return c
	}
	doc, ok := d.Node.(*ast.PolicyDoc)
	if !ok {
		return c
	}
	walkStmts(doc.Stmts, func(s ast.Stmt) { l.addContents(c, d, s) })
	return c
}

// addContents records what one statement of the document d adds to c.
func (l *linter) addContents(c *contents, d *bundle.Document, s ast.Stmt) {
	switch s := s.(type) {
	case *ast.AssertStmt:
		c.asserts = true
	case *ast.CallStmt:
		if dec, ok := d.Info.Constructors[s]; ok && dec.Name == l.deny() {
			c.denies = true
		}
		if target, ok := d.Info.Invocations[s]; ok {
			inner := l.contents(target)
			c.asserts = c.asserts || inner.asserts
			c.denies = c.denies || inner.denies
		}
	}
}

// deny returns the decision gated-deny guards: the highest-ranked of a
// `collect one` kind, or "" for a kind without one.
func (l *linter) deny() string {
	if l.o.Kind.Collect == kind.CollectOne && len(l.o.Kind.Precedence) > 0 {
		return l.o.Kind.Precedence[0]
	}
	return ""
}

func (l *linter) pathMatchesName(d *bundle.Document, header ast.Node) {
	if d.File == "" || strings.HasPrefix(d.File, "<") {
		return // stdin has no path
	}
	want := strings.ReplaceAll(d.Name, ".", "/") + ".sigil"
	if diag.PathMatches(strings.TrimPrefix(d.File, "./"), d.Name) {
		return
	}
	l.reportf(PathMatchesName, d, header, fmt.Sprintf("move it to %s, or rename it to match its file", want),
		"%s is in %s, not in a file named after it", d.Name, d.File)
}

// walkStmts calls fn for every statement, descending into `when` bodies.
func walkStmts(stmts []ast.Stmt, fn func(ast.Stmt)) {
	for _, s := range stmts {
		fn(s)
		if w, ok := s.(*ast.WhenStmt); ok {
			walkStmts(w.Body, fn)
		}
	}
}

// exprsOf returns a statement's own expressions, not those of the
// statements in its body.
func exprsOf(s ast.Stmt) []ast.Expr {
	switch s := s.(type) {
	case *ast.LetStmt:
		return []ast.Expr{s.Value}
	case *ast.ParamStmt:
		return []ast.Expr{s.Default, s.Min, s.Max}
	case *ast.WhenStmt:
		return []ast.Expr{s.Cond}
	case *ast.AssertStmt:
		return []ast.Expr{s.Cond}
	case *ast.CallStmt:
		out := []ast.Expr{s.Positional}
		for _, a := range s.Args {
			out = append(out, a.Value)
		}
		return out
	}
	return nil
}

// names records every identifier x reads, leaving out field names after
// a dot, which name fields rather than bindings.
func names(x ast.Expr, seen map[string]bool) {
	ast.Inspect(x, func(e ast.Expr) bool {
		switch e := e.(type) {
		case *ast.Ident:
			seen[e.Name] = true
		case *ast.SelectorExpr:
			names(e.X, seen)
			return false
		}
		return true
	})
}

// args renders an invocation's arguments in a canonical order, so two
// calls that differ only in argument order compare equal.
func args(s *ast.CallStmt) string {
	parts := make([]string, len(s.Args))
	for i, a := range s.Args {
		parts[i] = a.Name.Name + ": " + ast.Sprint(a.Value)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
