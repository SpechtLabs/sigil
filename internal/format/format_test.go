package format

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/gokind"
	"github.com/spechtlabs/sigil/internal/kind"
	"github.com/spechtlabs/sigil/internal/parser"
)

// repoRoot is where the corpus tests look for Sigil sources.
const repoRoot = "../.."

// spans matches the `[line:col-line:col]` ranges ast.Dump prints.
var spans = regexp.MustCompile(`\s*\[[0-9?:-]+\]`)

// sigilBlock matches a fenced sigil code block in the documentation.
var sigilBlock = regexp.MustCompile("(?s)```sigil\n(.*?)```")

func TestSource(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
		err  bool // the source doesn't parse, so nothing is formatted
	}{
		{name: "empty file", src: "", want: ""},
		{name: "a file that doesn't parse isn't formatted", src: "policy a: K@1\nlet = 1\n", err: true},
		{name: "blank lines only", src: "\n\n\n", want: ""},
		{name: "comments only", src: "// a\n\n\n// b", want: "// a\n\n// b\n"},
		{
			name: "spacing and indentation",
			src:  "policy  a.b :K@1\nwhen x==1{deny( reason:r )}",
			want: "policy a.b: K@1\n\nwhen x == 1 { deny(reason: r) }\n",
		},
		{
			name: "blank lines collapse to one and leave block edges",
			src:  "policy a: K@1\n\n\n\nwhen x {\n\n  deny(reason: r)\n\n}\n",
			want: "policy a: K@1\n\nwhen x {\n  deny(reason: r)\n}\n",
		},
		{
			name: "the conflict outcome goes right under the default",
			src:  "kind K version 1\ndecision d { reason: x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\n\n\nconflict d(reason: y)\n",
			want: "kind K version 1\n\ndecision d {\n  reason: x | y\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x)\nconflict d(reason: y)\n",
		},
		{
			name: "a conflict outcome away from the default stands apart",
			src:  "kind K version 1\nconflict d(reason: y)\ndecision d { reason: x | y }\ncollect one\nprecedence d\ndefault d(reason: x)\n",
			want: "kind K version 1\n\nconflict d(reason: y)\n\ndecision d {\n  reason: x | y\n}\n\ncollect one\nprecedence d\n\ndefault d(reason: x)\n",
		},
		{
			name: "separators: one between documents, none around them",
			src:  "---\npolicy a: K@1\n---\n---\nmodule b: K@1\n---\n",
			want: "policy a: K@1\n\n---\n\nmodule b: K@1\n",
		},
		{
			name: "missing separator added",
			src:  "policy a: K@1\nmodule b: K@1\n",
			want: "policy a: K@1\n\n---\n\nmodule b: K@1\n",
		},
		{
			name: "quantifier body with and gets parentheses",
			src:  "policy a: K@1\nlet x = any r in xs: r like \"a*\" and y\n",
			want: "policy a: K@1\n\nlet x = any r in xs: (r like \"a*\" and y)\n",
		},
		{
			name: "quantifier body without and or or stays bare",
			src:  "policy a: K@1\nlet x = all r in xs: r != \"admin\"\n",
			want: "policy a: K@1\n\nlet x = all r in xs: r != \"admin\"\n",
		},
		{
			name: "filter body with and gets parentheses",
			src:  "policy a: K@1\nlet x = filter   r in [\"a\",\"b\"]: r != \"a\" and r != y\n",
			want: "policy a: K@1\n\nlet x = filter r in [\"a\", \"b\"]: (r != \"a\" and r != y)\n",
		},
		{
			name: "filter body without and or or stays bare",
			src:  "policy a: K@1\nlet x = z in (filter r in xs: r != y)\n",
			want: "policy a: K@1\n\nlet x = z in (filter r in xs: r != y)\n",
		},
		{
			name: "break after an operator moves before it",
			src:  "policy a: K@1\nlet x = a and\n    b or\n c\n",
			want: "policy a: K@1\n\nlet x = a\n  and b\n  or c\n",
		},
		{
			name: "breaks inside a comparison are joined",
			src:  "policy a: K@1\nlet x = a\n ==\n b\n",
			want: "policy a: K@1\n\nlet x = a == b\n",
		},
		{
			name: "a list whose first item starts a line is one item per line",
			src:  "policy a: K@1\nlet x = [\n\"a\", \"b\"]\n",
			want: "policy a: K@1\n\nlet x = [\n  \"a\",\n  \"b\",\n]\n",
		},
		{
			name: "a list broken after its first item is joined",
			src:  "policy a: K@1\nlet x = [\"a\",\n\"b\",\n]\n",
			want: "policy a: K@1\n\nlet x = [\"a\", \"b\"]\n",
		},
		{
			name: "raw and interpreted strings keep their spelling",
			src:  "policy a: K@1\nlet x = y matches `^a\\d$` and z == \"\\u00e9\"\n",
			want: "policy a: K@1\n\nlet x = y matches `^a\\d$` and z == \"\\u00e9\"\n",
		},
		{
			name: "minus signs never touch",
			src:  "policy a: K@1\nlet x = - - -1\n",
			want: "policy a: K@1\n\nlet x = - - -1\n",
		},
		{
			name: "trailing comments align on consecutive lines",
			src:  "policy a: K@1\nlet a = 1 // one\nlet bbb = 22 // two\n\nlet c = 3   // three\n",
			want: "policy a: K@1\n\nlet a = 1    // one\nlet bbb = 22 // two\n\nlet c = 3 // three\n",
		},
		{
			name: "a comment after a call's opening paren trails it",
			src:  "policy a: K@1\nwhen x {\n  q( // why\n    a: 1,\n  )\n}\n",
			want: "policy a: K@1\n\nwhen x {\n  q( // why\n    a: 1,\n  )\n}\n",
		},
		{
			name: "a comment after the paren of a call on the rule's line trails it",
			src:  "policy a: K@1\nwhen x { deny(// why\nreason: a) }\n",
			want: "policy a: K@1\n\nwhen x {\n  deny( // why\n    reason: a,\n  )\n}\n",
		},
		{
			name: "a one-line rule with two statements is expanded",
			src:  "policy a: K@1\nwhen x { deny(reason: a) deny(reason: b) }\n",
			want: "policy a: K@1\n\nwhen x {\n  deny(reason: a)\n  deny(reason: b)\n}\n",
		},
		{
			name: "param bounds print min before max",
			src:  "policy a: K@1\nparam s: duration = 2h, max: 4h, min: 1h\n",
			want: "policy a: K@1\n\nparam s: duration = 2h, min: 1h, max: 4h\n",
		},
		{
			name: "closing type argument before a default",
			src:  "policy a: K@1\nparam m: map<string, int>= {}\n",
			want: "policy a: K@1\n\nparam m: map<string, int> = {}\n",
		},
		{
			name: "enum on one line",
			src:  "kind K version 1\nenum  Tier :critical|standard\n",
			want: "kind K version 1\n\nenum Tier: critical | standard\n",
		},
		{
			name: "enum breaks where the source broke, before or after the pipe",
			src:  "kind K version 1\nenum Tier: a |\nb\n| c | d\n",
			want: "kind K version 1\n\nenum Tier: a\n  | b\n  | c | d\n",
		},
		{
			name: "consecutive enums group together",
			src:  "kind K version 1\nenum A: a\nenum B: b\ninput x: A\n",
			want: "kind K version 1\n\nenum A: a\nenum B: b\n\ninput x: A\n",
		},
		{
			name: "decision on one line gets a line per field, reason first",
			src:  "kind K version 1\ndecision review { approvers: list<string> reason: owner }\n",
			want: "kind K version 1\n\ndecision review {\n  reason: owner\n  approvers: list<string>\n}\n",
		},
		{
			name: "field named like an operator after a default",
			src:  "kind K version 1\ndecision d {\nreason: r\nnote: string = \"\"\nin: int\n}\n",
			want: "kind K version 1\n\ndecision d {\n  reason: r\n  note: string = \"\"\n  in: int\n}\n",
		},
		{
			name: "legacy decision without fields",
			src:  "kind K version 1\ndecision deny { a\nb }\n",
			want: "kind K version 1\n\ndecision deny {\n  reason: a | b\n}\n",
		},
		{
			name: "legacy decision with fields",
			src:  "kind K version 1\ndecision approve(bake: duration = 1h, note: string,) {\n  lgtm\n}\n",
			want: "kind K version 1\n\ndecision approve {\n  reason: lgtm\n  bake: duration = 1h\n  note: string\n}\n",
		},
		{
			name: "legacy decision keeps a comment between reasons with the reason before it",
			src:  "kind K version 1\ndecision deny {\n  a // first\n  b\n}\n",
			want: "kind K version 1\n\ndecision deny {\n  reason: a // first\n    | b\n}\n",
		},
		{
			name: "legacy fields keep their comments when the reason moves ahead",
			src:  "kind K version 1\ndecision approve(\n  // how long\n  bake: duration, // at least\n) { // then\n  lgtm\n}\n",
			want: "kind K version 1\n\ndecision approve {\n  // then\n  reason: lgtm\n  // how long\n  bake: duration // at least\n}\n",
		},
		{
			name: "a comment after the closing brace stays there",
			src:  "kind K version 1\ndecision deny { reason: a } // done\n",
			want: "kind K version 1\n\ndecision deny {\n  reason: a\n} // done\n",
		},
		{
			name: "positional reason becomes a named argument",
			src:  "policy a: K@1\nwhen x { approve(lgtm, bake: 1h) }\n",
			want: "policy a: K@1\n\nwhen x { approve(reason: lgtm, bake: 1h) }\n",
		},
		{
			name: "positional reason in a broken argument list",
			src:  "policy a: K@1\nwhen x {\n  review(\n    owner,\n    approvers: [\"a\"])\n}\n",
			want: "policy a: K@1\n\nwhen x {\n  review(\n    reason: owner,\n    approvers: [\"a\"],\n  )\n}\n",
		},
		{
			name: "positional reason of the default",
			src:  "kind K version 1\ndefault deny(no_rule_matched)\n",
			want: "kind K version 1\n\ndefault deny(reason: no_rule_matched)\n",
		},
		{
			name: "file without a trailing newline gets one",
			src:  "module a: K@1\npub let x = 1",
			want: "module a: K@1\n\npub let x = 1\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, errs := Source("test.sigil", []byte(tt.src))
			if tt.err {
				if errs == nil || got != nil {
					t.Fatalf("expected parse errors and no output, got %v and\n%s", errs, got)
				}
				return
			}
			if errs != nil {
				t.Fatalf("unexpected errors: %v", errs)
			}
			if string(got) != tt.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tt.want)
			}
			again, errs := Source("test.sigil", got)
			if errs != nil || string(again) != string(got) {
				t.Errorf("not idempotent:\n%s\nformats to:\n%s", got, again)
			}
		})
	}
}

// TestCorpus formats every Sigil source in the repository and every
// sigil block in the documentation that parses on its own, and checks
// that formatting is idempotent and keeps the tree: the formatted source
// parses to the same AST, apart from the parentheses fmt adds around
// quantifier and filter bodies and the migration to the current decision
// and constructor syntax.
func TestCorpus(t *testing.T) {
	for name, src := range corpus(t) {
		t.Run(name, func(t *testing.T) {
			if _, errs := parser.ParseFile(name, src); errs != nil {
				t.Skip("doesn't parse")
			}
			once, errs := Source(name, src)
			if errs != nil {
				t.Fatalf("parses but doesn't format: %v", errs)
			}
			twice, errs := Source(name, once)
			if errs != nil {
				t.Fatalf("formatted source doesn't parse: %v\n%s", errs, once)
			}
			if string(twice) != string(once) {
				t.Errorf("not idempotent:\n--- once ---\n%s\n--- twice ---\n%s", once, twice)
			}
			if before, after := tree(t, name, src), tree(t, name, once); before != after {
				t.Errorf("formatting changed the tree:\n--- before ---\n%s\n--- after ---\n%s", before, after)
			}
		})
	}
}

// TestKindSourceIsFormatted checks that what Schema() exports passes
// `sigil fmt --check`: the canonical source of every kind in the corpus,
// and of a kind built from Go types with every feature, formats to
// itself.
func TestKindSourceIsFormatted(t *testing.T) {
	kinds := map[string]*kind.Kind{"gokind": builtKind(t)}
	for name, src := range corpus(t) {
		f, errs := parser.ParseFile(name, src)
		if errs != nil {
			continue
		}
		for _, d := range f.Docs {
			if kd, ok := d.(*ast.KindDoc); ok {
				c := check.New(name)
				if k := c.Kind(kd); k != nil && c.Errors() == nil {
					kinds[name+"#"+kd.Name.Name] = k
				}
			}
		}
	}
	if len(kinds) < 3 {
		t.Fatalf("found only %d kinds in the corpus", len(kinds))
	}
	for name, k := range kinds {
		t.Run(name, func(t *testing.T) {
			src := k.Source()
			got, errs := Source(name, []byte(src))
			if errs != nil {
				t.Fatalf("exported kind doesn't parse: %v\n%s", errs, src)
			}
			if string(got) != src {
				t.Errorf("exported kind isn't formatted:\n--- Source() ---\n%s\n--- formatted ---\n%s", src, got)
			}
		})
	}
}

// corpus collects the Sigil sources in the repository: every .sigil and
// .golden file under testdata directories, and every sigil block in the
// documentation.
func corpus(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", ".vuepress", "dist":
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".sigil"):
		case strings.HasSuffix(path, ".golden") && strings.HasPrefix(path, filepath.Join(repoRoot, "internal", "format")):
		case strings.HasSuffix(path, ".md") && strings.HasPrefix(path, filepath.Join(repoRoot, "docs")):
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for i, m := range sigilBlock.FindAllSubmatch(src, -1) {
				out[fmt.Sprintf("%s#%03d", path, i)] = m[1]
			}
			return nil
		default:
			return nil
		}
		src, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		out[path] = src
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) < 20 {
		t.Fatalf("corpus holds only %d sources", len(out))
	}
	return out
}

// tree dumps the AST of src without positions, with the parentheses fmt
// adds around quantifier and filter bodies removed, and the old decision
// and constructor syntax migrated as fmt does it.
func tree(t *testing.T, name string, src []byte) string {
	t.Helper()
	f, errs := parser.ParseFile(name, src)
	if errs != nil {
		t.Fatalf("doesn't parse: %v\n%s", errs, src)
	}
	for _, d := range f.Docs {
		migrate(d)
		for _, x := range exprsOf(d) {
			ast.Inspect(x, func(x ast.Expr) bool {
				switch q := x.(type) {
				case *ast.QuantExpr:
					q.Body = bare(q.Body)
				case *ast.FilterExpr:
					q.Body = bare(q.Body)
				}
				return true
			})
		}
	}
	return spans.ReplaceAllString(ast.Dump(f), "")
}

// bare returns a quantifier's or filter's body without the parentheses
// fmt adds around a top-level `and`, `or` or `xor`.
func bare(body ast.Expr) ast.Expr {
	if p, ok := body.(*ast.ParenExpr); ok {
		if b, ok := p.X.(*ast.BinaryExpr); ok && breaks(b.Op) {
			return b
		}
	}
	return body
}

// migrate rewrites the old syntax in d the way fmt prints it: a legacy
// decision becomes a current one, and a positional reason a leading
// `reason:` argument. The order of a decision's fields doesn't show in
// the dump, which prints the reason first.
func migrate(d ast.Doc) {
	call := func(c *ast.CallStmt) {
		if c.Positional != nil {
			reason := &ast.Ident{Name: "reason"}
			c.Args = append([]*ast.NamedArg{{Name: reason, Value: c.Positional}}, c.Args...)
			c.Positional = nil
		}
	}
	var stmts func([]ast.Stmt)
	stmts = func(ss []ast.Stmt) {
		for _, s := range ss {
			switch s := s.(type) {
			case *ast.WhenStmt:
				stmts(s.Body)
			case *ast.CallStmt:
				call(s)
			}
		}
	}
	switch d := d.(type) {
	case *ast.PolicyDoc:
		stmts(d.Stmts)
	case *ast.KindDoc:
		for _, decl := range d.Decls {
			switch decl := decl.(type) {
			case *ast.DecisionDecl:
				decl.Legacy = false
			case *ast.DefaultDecl:
				call(decl.Call)
			case *ast.ConflictDecl:
				call(decl.Call)
			}
		}
	}
}

// exprsOf returns the top-level expressions of a document.
func exprsOf(d ast.Doc) []ast.Expr {
	var out []ast.Expr
	var stmts func([]ast.Stmt)
	call := func(c *ast.CallStmt) {
		if c.Positional != nil {
			out = append(out, c.Positional)
		}
		for _, a := range c.Args {
			out = append(out, a.Value)
		}
	}
	stmts = func(ss []ast.Stmt) {
		for _, s := range ss {
			switch s := s.(type) {
			case *ast.ParamStmt:
				for _, x := range []ast.Expr{s.Default, s.Min, s.Max} {
					if x != nil {
						out = append(out, x)
					}
				}
			case *ast.LetStmt:
				out = append(out, s.Value)
			case *ast.WhenStmt:
				out = append(out, s.Cond)
				stmts(s.Body)
			case *ast.AssertStmt:
				out = append(out, s.Cond)
			case *ast.CallStmt:
				call(s)
			}
		}
	}
	switch d := d.(type) {
	case *ast.PolicyDoc:
		stmts(d.Stmts)
	case *ast.ModuleDoc:
		for _, l := range d.Lets {
			out = append(out, l.Value)
		}
	case *ast.KindDoc:
		for _, decl := range d.Decls {
			switch decl := decl.(type) {
			case *ast.DecisionDecl:
				for _, f := range decl.Fields {
					if f.Default != nil {
						out = append(out, f.Default)
					}
				}
			case *ast.DefaultDecl:
				call(decl.Call)
			case *ast.ConflictDecl:
				call(decl.Call)
			}
		}
	}
	return out
}

// Types for builtKind, covering every shape NewKind accepts.
type (
	buildRelease struct {
		Soak    time.Duration `policy:"soak"`
		Hotfix  bool          `policy:"hotfix"`
		Ticket  *string       `policy:"ticket"`
		BuiltAt time.Time     `policy:"built_at"`
	}
	buildInput struct {
		Release buildRelease              `policy:"release"`
		Weights map[string]float64        `policy:"weights"`
		Owners  map[int64][]string        `policy:"owners"`
		Prior   *buildRelease             `policy:"prior"`
		Count   int                       `policy:"count"`
		Nested  map[string]map[string]int `policy:"nested"`
	}
	buildNone     struct{}
	buildApproved struct{}
	buildReview   struct {
		Approvers []string      `policy:"approvers"`
		Note      string        `policy:"note,default=\"none\""`
		Bake      time.Duration `policy:"bake,default=1h30m"`
		Tiers     []string      `policy:"tiers,default=[\"a\", \"b\"]"`
	}
)

// builtKind builds a kind from Go types the way policy.NewKind does.
func builtKind(t *testing.T) *kind.Kind {
	t.Helper()
	k, _, errs := gokind.Build(gokind.Options{
		Name:    "Built",
		Input:   reflect.TypeFor[buildInput](),
		Version: 3,
		Accepts: new(2),
		Ranked:  true,
		Decisions: []gokind.Decision{
			{Name: "deny", Payload: reflect.TypeFor[buildNone](), Reasons: []string{"no_rule_matched", "stale"}},
			{Name: "review", Payload: reflect.TypeFor[buildReview](), Reasons: []string{"owner", "sre"}},
			{Name: "approve", Payload: reflect.TypeFor[buildApproved](), Reasons: []string{"lgtm", "release_manager"}},
		},
		Rankings:  []gokind.Ranking{{Decision: "review", Reasons: []string{"sre", "owner"}}},
		Exclusive: [][]kind.Outcome{{{Decision: "approve", Reason: "lgtm"}, {Decision: "approve", Reason: "release_manager"}}},
		Funcs:     []gokind.Func{{Name: "split", Fn: strings.Split}, {Name: "parse", Fn: time.ParseDuration}},
		Default:   &gokind.Default{Decision: "deny", Reason: "no_rule_matched"},
		Conflict:  &gokind.Default{Decision: "deny", Reason: "stale"},
	})
	if errs != nil {
		t.Fatalf("building the kind: %v", errs)
	}
	return k
}
