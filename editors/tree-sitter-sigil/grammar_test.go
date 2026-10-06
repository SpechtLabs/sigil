// The tests in this file hold the tree-sitter grammar to the Go parser,
// which defines the language. They need the tree-sitter CLI and a C
// compiler, and skip without the CLI, so a plain `go test ./...` passes on
// a machine that has neither. `mise run tree-sitter-test` runs them with
// the CLI that .mise.toml pins.
package treesitter_test

import (
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/internal/ast"
	"github.com/spechtlabs/sigil/internal/parser"
)

// repoRoot is where the corpus test looks for Sigil sources.
const repoRoot = "../.."

// What the Go parser must make of a source.
const (
	anyVerdict verdict = iota // a source from the repository, skipped if it doesn't parse
	mustAccept                // a test case of the grammar's corpus
	mustReject                // a test case of the grammar's corpus marked `:error`
)

var (
	// sigilBlock matches a fenced sigil code block in Markdown, with or
	// without a title after the language.
	sigilBlock = regexp.MustCompile("(?ms)^[ \t]*```sigil[^\n]*\n(.*?)^[ \t]*```")

	// wrappers turn a code block from the documentation that isn't a whole
	// file, such as a few statements or an expression, into one, tried in
	// order until the Go parser accepts the result.
	wrappers = []func([]byte) []byte{
		func(b []byte) []byte { return b },
		prefix("policy fragment: Fragment\n"),
		prefix("kind Fragment version 1\n"),
		prefix("policy fragment: Fragment\nlet fragment =\n"),
		letPerLine,
	}

	// caseHeader matches the header of a test case in a tree-sitter corpus
	// file: the name and any attributes, between two lines of `=`.
	caseHeader = regexp.MustCompile(`(?m)^={3,}\n((?s:.*?))\n={3,}\n`)

	// caseDivider matches the line of `-` between a test case's input and
	// its expected tree. An input may hold `---` lines of its own, but the
	// tree never does, so the last one in a case is the divider.
	caseDivider = regexp.MustCompile(`(?m)^-{3,}$`)

	// failure is a line `tree-sitter parse --quiet` prints for a file whose
	// tree has an error: the path, a tab, timing, and the first bad node.
	failure = regexp.MustCompile(`^(.*?)\t.*\((ERROR|MISSING[^\[]*) \[(\d+), (\d+)\]`)
)

// TestCorpus parses every Sigil source in the repository that the Go
// parser accepts with the tree-sitter parser too; see corpus for what it
// reads. Each one must parse without an ERROR or MISSING node, and its tree
// must agree with the Go parser's on where every document, statement,
// declaration, composite type and composite expression starts and ends,
// which holds the grammar's precedence and statement boundaries to the
// parser's. A source the Go parser rejects, such as a parser error golden,
// is skipped, so what's left out follows the parser, not a list. A test case
// of the grammar's own corpus must parse, or, marked `:error`, must fail to.
func TestCorpus(t *testing.T) {
	cli := treeSitter(t)
	lib := build(t, cli)

	dir := t.TempDir()
	var inputs []input
	var list bytes.Buffer
	sources := corpus(t)
	for i, src := range sources {
		f, ok := accept(&src)
		if !ok || src.verdict == mustReject {
			t.Run(src.name, func(t *testing.T) {
				switch {
				case ok:
					t.Fatal("the grammar's corpus expects an error, but the Go parser accepts it")
				case src.verdict == mustAccept:
					t.Fatal("the Go parser rejects this test case of the grammar's corpus")
				case src.verdict == anyVerdict:
					t.Skip("the Go parser rejects it")
				}
			})
			continue
		}
		path := filepath.Join(dir, fmt.Sprintf("%04d.sigil", i))
		if err := os.WriteFile(path, src.text, 0o600); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input{source: src, path: path, want: goSpans(f)})
		list.WriteString(path + "\n")
	}
	t.Logf("%d of %d sources parse with the Go parser", len(inputs), len(sources))
	if len(inputs) < 100 {
		t.Fatal("too few sources parse with the Go parser; is the corpus walk broken?")
	}
	listFile := filepath.Join(dir, "paths.txt")
	if err := os.WriteFile(listFile, list.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	failed := treeErrors(t, cli, lib, listFile)
	trees := treeSpans(t, cli, lib, listFile)

	for _, in := range inputs {
		t.Run(in.name, func(t *testing.T) {
			if in.fragment {
				// Positions are in the text tree-sitter parsed, the wrapper included.
				defer func() {
					if t.Failed() {
						t.Logf("the text both parsers read:\n%s", in.text)
					}
				}()
			}
			if why, ok := failed[in.path]; ok {
				t.Fatalf("the Go parser accepts it, but the tree has an error: %s", why)
			}
			got := trees[in.path]
			for _, s := range missing(in.want, got) {
				t.Errorf("the Go parser has %s, the tree doesn't", s.describe(in.text))
			}
			for _, s := range missing(got, in.want) {
				t.Errorf("the tree has %s, the Go parser doesn't", s.describe(in.text))
			}
		})
	}
}

// TestQueries compiles every query in queries/ against the grammar, so a
// node or field a grammar change renames or drops fails here rather than in
// an editor. The highlight test cases in test/highlight check what the
// highlights capture; `tree-sitter test` runs them.
func TestQueries(t *testing.T) {
	cli := treeSitter(t)
	lib := build(t, cli)
	queries, err := filepath.Glob(filepath.Join("queries", "*.scm"))
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) == 0 {
		t.Fatal("no queries in queries/")
	}
	for _, q := range queries {
		t.Run(filepath.Base(q), func(t *testing.T) {
			run(t, cli, "query", "--quiet", "--lib-path", lib, "--lang-name", "sigil", q, filepath.Join("test", "highlight", "policy.sigil"))
		})
	}
}

// TestGeneratedSourcesAreCurrent regenerates the parser from grammar.js
// and fails when the committed src/ differs from it.
func TestGeneratedSourcesAreCurrent(t *testing.T) {
	cli := treeSitter(t)
	out := t.TempDir()
	run(t, cli, "generate", "--abi", "15", "--js-runtime", "native", "--output", out, "grammar.js")

	err := filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(out, path)
		if err != nil {
			return err
		}
		want, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		got, err := os.ReadFile(filepath.Join("src", rel))
		switch {
		case err != nil:
			t.Errorf("src/%s: %v; run `mise run tree-sitter-generate`", rel, err)
		case !bytes.Equal(got, want):
			t.Errorf("src/%s is out of date; run `mise run tree-sitter-generate`", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// source is a Sigil source from the repository: a file, a code block in a
// Markdown file, or a test case of the grammar's own corpus, which name
// says. A fragment is a code block, which may hold less than a file.
type source struct {
	name     string
	text     []byte
	verdict  verdict
	fragment bool // a code block, which may need one of the wrappers
}

// verdict is what the Go parser must make of a source: see the constants.
type verdict uint8

// input is a source the Go parser accepts, written to path for the CLI,
// with the spans of its Go syntax tree.
type input struct {
	source
	path string
	want []span
}

// span is a node both trees have, by tree-sitter's name for it, and the
// byte offsets it covers.
type span struct {
	kind       string
	start, end int
}

// describe formats s with line:column positions in src.
func (s span) describe(src []byte) string {
	return fmt.Sprintf("a %s at %s-%s", s.kind, position(src, s.start), position(src, s.end))
}

// corpus collects the Sigil sources in the repository: every .sigil file,
// every .golden file of the formatter's tests, which hold formatted Sigil,
// every sigil block in a Markdown file, and the input of every test case
// in test/corpus.
func corpus(t *testing.T) []source {
	t.Helper()
	var out []source
	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".claude", "node_modules", "dist", "target":
				return filepath.SkipDir
			}
			return nil
		}
		name, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		switch {
		case strings.HasSuffix(path, ".sigil"):
		case strings.HasSuffix(path, ".golden") && strings.HasPrefix(name, filepath.Join("internal", "format")):
		case strings.HasSuffix(path, ".txt") && strings.HasPrefix(name, filepath.Join("editors", "tree-sitter-sigil", "test", "corpus")):
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			out = append(out, corpusCases(name, src)...)
			return nil
		case strings.HasSuffix(path, ".md"):
			src, rerr := os.ReadFile(path)
			if rerr != nil {
				return rerr
			}
			for i, m := range sigilBlock.FindAllSubmatch(src, -1) {
				out = append(out, source{name: fmt.Sprintf("%s#%03d", name, i), text: m[1], fragment: true})
			}
			return nil
		default:
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out = append(out, source{name: name, text: src})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// accept parses src with the Go parser and returns the tree, or false when
// the parser rejects it. A fragment the parser rejects on its own is tried
// with each of the wrappers, and src keeps the text that parsed.
func accept(src *source) (*ast.File, bool) {
	for i, w := range wrappers {
		if i > 0 && !src.fragment {
			break
		}
		text := w(src.text)
		if f, errs := parser.ParseFile(src.name, text); errs == nil {
			src.text = text
			return f, true
		}
	}
	return nil, false
}

// prefix returns a wrapper that puts text before a code block.
func prefix(text string) func([]byte) []byte {
	return func(b []byte) []byte {
		return append([]byte(text), b...)
	}
}

// letPerLine is the wrapper for a code block that lists expressions one
// per line: it makes each line that isn't blank or a comment a `let`.
func letPerLine(b []byte) []byte {
	out := []byte("policy fragment: Fragment\n")
	for line := range bytes.Lines(b) {
		if trimmed := bytes.TrimSpace(line); len(trimmed) > 0 && !bytes.HasPrefix(trimmed, []byte("//")) {
			out = append(out, "let fragment = "...)
		}
		out = append(out, line...)
	}
	return out
}

// corpusCases returns the input of every test case in a tree-sitter corpus
// file, named after the file and the case. The Go parser must reject the
// input of a case marked `:error` and accept every other.
func corpusCases(file string, src []byte) []source {
	var out []source
	headers := caseHeader.FindAllSubmatchIndex(src, -1)
	for i, h := range headers {
		end := len(src)
		if i+1 < len(headers) {
			end = headers[i+1][0]
		}
		body := src[h[1]:end]
		dividers := caseDivider.FindAllIndex(body, -1)
		if len(dividers) == 0 {
			continue
		}
		lines := strings.Split(string(src[h[2]:h[3]]), "\n")
		v := mustAccept
		if slices.Contains(lines[1:], ":error") {
			v = mustReject
		}
		name := fmt.Sprintf("%s: %s", file, lines[0])
		out = append(out, source{name: name, text: body[:dividers[len(dividers)-1][0]], verdict: v})
	}
	return out
}

// goSpans returns the spans of the documents, statements, declarations,
// composite types and composite expressions in f.
func goSpans(f *ast.File) []span {
	var c collector
	for _, d := range f.Docs {
		c.doc(d)
	}
	return c.spans
}

// collector gathers the spans goSpans returns.
type collector struct {
	spans []span
}

func (c *collector) add(kind string, n ast.Node) {
	c.spans = append(c.spans, span{kind: kind, start: n.Pos().Offset, end: n.End().Offset})
}

func (c *collector) doc(d ast.Doc) {
	switch d := d.(type) {
	case *ast.PolicyDoc:
		c.add("policy_document", d)
		for _, u := range d.Uses {
			c.stmt(u)
		}
		for _, s := range d.Stmts {
			c.stmt(s)
		}
	case *ast.ModuleDoc:
		c.add("module_document", d)
		for _, u := range d.Uses {
			c.stmt(u)
		}
		for _, l := range d.Lets {
			c.stmt(l)
		}
	case *ast.KindDoc:
		c.add("kind_document", d)
		for _, decl := range d.Decls {
			c.decl(decl)
		}
	}
}

func (c *collector) stmt(s ast.Stmt) {
	switch s := s.(type) {
	case *ast.UseStmt:
		c.add("use_statement", s)
	case *ast.ParamStmt:
		c.add("param_statement", s)
		c.typ(s.Type)
		c.expr(s.Default, s.Min, s.Max)
	case *ast.LetStmt:
		c.add("let_statement", s)
		c.expr(s.Value)
	case *ast.WhenStmt:
		c.add("when_statement", s)
		c.expr(s.Cond)
		for _, b := range s.Body {
			c.stmt(b)
		}
	case *ast.AssertStmt:
		c.add("assert_statement", s)
		c.expr(s.Cond)
	case *ast.CallStmt:
		c.call(s)
	}
}

func (c *collector) decl(d ast.Decl) {
	switch d := d.(type) {
	case *ast.EnumDecl:
		c.add("enum_declaration", d)
	case *ast.TypeDecl:
		c.add("type_declaration", d)
		c.fields(d.Fields)
	case *ast.InputDecl:
		c.add("input_declaration", d)
		c.typ(d.Type)
	case *ast.FnDecl:
		c.add("function_declaration", d)
		for _, p := range d.Params {
			c.typ(p)
		}
		c.typ(d.Result)
	case *ast.DecisionDecl:
		c.add("decision_declaration", d)
		c.fields(d.Fields)
	case *ast.PrecedenceDecl:
		c.add("precedence_declaration", d)
	case *ast.ExclusiveDecl:
		c.add("exclusive_declaration", d)
	case *ast.CollectDecl:
		c.add("collect_declaration", d)
	case *ast.DefaultDecl:
		c.add("default_declaration", d)
		c.call(d.Call)
	case *ast.ConflictDecl:
		c.add("conflict_declaration", d)
		c.call(d.Call)
	}
}

func (c *collector) call(s *ast.CallStmt) {
	c.add("call", s)
	c.expr(s.Positional)
	for _, a := range s.Args {
		c.expr(a.Value)
	}
}

func (c *collector) fields(fields []*ast.Field) {
	for _, f := range fields {
		c.typ(f.Type)
		c.expr(f.Default)
	}
}

func (c *collector) typ(t ast.Type) {
	switch t := t.(type) {
	case *ast.OptionalType:
		c.add("optional_type", t)
		c.typ(t.Elem)
	case *ast.ListType:
		c.add("list_type", t)
		c.typ(t.Elem)
	case *ast.MapType:
		c.add("map_type", t)
		c.typ(t.Key)
		c.typ(t.Value)
	}
}

func (c *collector) expr(xs ...ast.Expr) {
	for _, x := range xs {
		ast.Inspect(x, func(x ast.Expr) bool {
			if kind := exprKind(x); kind != "" {
				c.add(kind, x)
			}
			return true
		})
	}
}

// exprKind returns tree-sitter's name for a composite expression, or ""
// for a name or a literal, whose spans can't disagree.
func exprKind(x ast.Expr) string {
	switch x.(type) {
	case *ast.BinaryExpr:
		return "binary_expression"
	case *ast.UnaryExpr:
		return "unary_expression"
	case *ast.QuantExpr:
		return "quantifier_expression"
	case *ast.FilterExpr:
		return "filter_expression"
	case *ast.SelectorExpr:
		return "selector_expression"
	case *ast.IndexExpr:
		return "index_expression"
	case *ast.CallExpr:
		return "call_expression"
	case *ast.ParenExpr:
		return "parenthesized_expression"
	case *ast.ListLit:
		return "list_literal"
	case *ast.MapLit:
		return "map_literal"
	}
	return ""
}

// treeErrors parses every file listed in listFile and returns, by path, a
// description of the first ERROR or MISSING node in each tree that has
// one.
func treeErrors(t *testing.T, cli, lib, listFile string) map[string]string {
	t.Helper()
	// --quiet prints a line for each tree with an error, and the CLI exits
	// 1 when there is one.
	cmd := exec.Command(cli, "parse", "--quiet", "--lib-path", lib, "--lang-name", "sigil", "--paths", listFile)
	out, err := cmd.Output()
	if err != nil && cmd.ProcessState.ExitCode() != 1 {
		t.Fatalf("tree-sitter parse: %v\n%s", err, stderr(err))
	}

	failed := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		m := failure.FindStringSubmatch(sc.Text())
		if m == nil {
			t.Fatalf("unexpected tree-sitter output: %q", sc.Text())
		}
		row, _ := strconv.Atoi(m[3])
		col, _ := strconv.Atoi(m[4])
		failed[m[1]] = fmt.Sprintf("%s at %d:%d", strings.TrimSpace(m[2]), row+1, col+1)
	}
	if (err == nil) != (len(failed) == 0) {
		t.Fatalf("tree-sitter parse exited with %v but reported %d trees with errors:\n%s", err, len(failed), out)
	}
	return failed
}

// treeSpans parses every file listed in listFile and returns, by path,
// the spans of the nodes goSpans also collects.
func treeSpans(t *testing.T, cli, lib, listFile string) map[string][]span {
	t.Helper()
	cmd := exec.Command(cli, "parse", "--xml", "--lib-path", lib, "--lang-name", "sigil", "--paths", listFile)
	out, err := cmd.Output()
	if err != nil && cmd.ProcessState.ExitCode() != 1 {
		t.Fatalf("tree-sitter parse: %v\n%s", err, stderr(err))
	}

	kinds := map[string]bool{}
	for _, k := range []string{
		"policy_document", "module_document", "kind_document",
		"use_statement", "param_statement", "let_statement", "when_statement", "assert_statement", "call",
		"enum_declaration", "type_declaration", "input_declaration", "function_declaration",
		"decision_declaration", "precedence_declaration", "exclusive_declaration",
		"collect_declaration", "default_declaration", "conflict_declaration",
		"optional_type", "list_type", "map_type",
		"binary_expression", "unary_expression", "quantifier_expression", "filter_expression",
		"selector_expression", "index_expression", "call_expression", "parenthesized_expression",
		"list_literal", "map_literal",
	} {
		kinds[k] = true
	}

	spans := map[string][]span{}
	var path string
	var lines []int // the byte offset of each line of the file at path
	dec := xml.NewDecoder(bytes.NewReader(out))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("reading tree-sitter's XML: %v", err)
		}
		el, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if el.Name.Local == "source" {
			path = attr(el, "name")
			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			lines = lineStarts(src)
			continue
		}
		if !kinds[el.Name.Local] {
			continue
		}
		offset := func(row, col string) int {
			r, _ := strconv.Atoi(attr(el, row))
			c, _ := strconv.Atoi(attr(el, col))
			return lines[r] + c
		}
		spans[path] = append(spans[path], span{kind: el.Name.Local, start: offset("srow", "scol"), end: offset("erow", "ecol")})
	}
	return spans
}

// missing returns the spans in want that got doesn't have, counting
// duplicates: `((a))` has two parenthesized expressions.
func missing(want, got []span) []span {
	left := map[span]int{}
	for _, s := range got {
		left[s]++
	}
	var out []span
	for _, s := range want {
		if left[s] > 0 {
			left[s]--
			continue
		}
		out = append(out, s)
	}
	slices.SortFunc(out, func(a, b span) int { return a.start - b.start })
	return out
}

// attr returns the value of an XML attribute, or "".
func attr(el xml.StartElement, name string) string {
	for _, a := range el.Attr {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// lineStarts returns the byte offset where each line of src starts, with
// one more for a line after a final newline.
func lineStarts(src []byte) []int {
	starts := []int{0}
	for i, c := range src {
		if c == '\n' {
			starts = append(starts, i+1)
		}
	}
	return starts
}

// position formats a byte offset in src as a 1-based line:column, the
// column counted in bytes.
func position(src []byte, offset int) string {
	line := bytes.Count(src[:offset], []byte("\n"))
	col := offset - (bytes.LastIndexByte(src[:offset], '\n') + 1)
	return fmt.Sprintf("%d:%d", line+1, col+1)
}

// treeSitter returns the tree-sitter CLI's path, or skips the test.
func treeSitter(t *testing.T) string {
	t.Helper()
	cli, err := exec.LookPath("tree-sitter")
	if err != nil {
		t.Skip("tree-sitter isn't on PATH; `mise run tree-sitter-test` runs this with the pinned CLI")
	}
	return cli
}

// build compiles the parser in src/ into a library in a temporary
// directory and returns its path.
func build(t *testing.T, cli string) string {
	t.Helper()
	lib := filepath.Join(t.TempDir(), "sigil.so")
	run(t, cli, "build", "--output", lib, ".")
	return lib
}

// run runs the CLI in the grammar's directory and fails the test if it
// fails.
func run(t *testing.T, cli string, args ...string) {
	t.Helper()
	cmd := exec.Command(cli, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tree-sitter %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// stderr returns what a failed command wrote to its standard error.
func stderr(err error) []byte {
	if ee, ok := errors.AsType[*exec.ExitError](err); ok {
		return ee.Stderr
	}
	return nil
}
