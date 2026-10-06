package gogen

import (
	"bytes"
	"go/ast"
	"go/format"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/constant"
	"github.com/spechtlabs/sigil/internal/kind"
	sigiltoken "github.com/spechtlabs/sigil/internal/token"
	sigil "github.com/spechtlabs/sigil/internal/types"
)

// nonIdent splits the fuzzer's name string into words.
var nonIdent = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// sourceImporter type-checks package policy and its dependencies from
// source once per process, so every fuzz input after the first costs
// only its own file. The source importer looks a path up with go list
// before it consults its own cache, so packages are cached here by path.
var sourceImporter = sync.OnceValue(func() types.Importer {
	return &cachingImporter{from: importer.ForCompiler(token.NewFileSet(), "source", nil), pkgs: map[string]*types.Package{}}
})

// cachingImporter imports each path once.
type cachingImporter struct {
	from types.Importer
	pkgs map[string]*types.Package
	mu   sync.Mutex
}

// names hands out Sigil names built from the fuzzer's words, unique
// across the kind's one namespace, so the kind stays valid while its
// names collide in Go: `a_b` and `aB` are two Sigil names and one Go
// name.
type names struct {
	words []string
	used  map[string]bool
	next  int
}

// FuzzGenerate builds valid kinds from the fuzzer's bytes, with names
// from its words and defaults from its values, puts their declarations
// in the order policy.NewKind produces, and checks the property gen go
// promises: Generate succeeds unless a struct type, enum or the package
// is named so Go can't declare it, and the code it returns is gofmt's
// output and type-checks against package policy.
func FuzzGenerate(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5}, "Tier tier user_id userID level level_low new_kind Input", int64(42), "say `hi`", false)
	f.Add([]byte{5, 4, 3, 2, 1, 0}, "error chan time Funcs NewKind x x_data", int64(-9223372036854775808), "\"\n世界", true)
	f.Add([]byte{3, 3, 6, 6}, "_1 _ __ a__b A_B ab", int64(0), "", false)
	f.Add([]byte{}, "", int64(1), "x", true)
	f.Fuzz(func(t *testing.T, shape []byte, words string, n int64, s string, all bool) {
		k := fuzzKind(shape, words, n, s, all)
		if k.Validate(nil) != nil {
			return
		}
		code, errs := Generate(k, Options{})
		for _, e := range errs {
			if !strings.HasPrefix(e.Msg, "Go code can't declare") && !strings.HasPrefix(e.Msg, "package name") {
				t.Fatalf("Generate() rejected a kind in canonical order: %s\n%s", e.Msg, k.Source())
			}
		}
		if errs != nil {
			return
		}
		if formatted, err := format.Source(code); err != nil || !bytes.Equal(formatted, code) {
			t.Fatalf("generated code isn't gofmt's output (%v):\n%s", err, code)
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "kind.go", code, parser.ParseComments)
		if err != nil {
			t.Fatalf("generated code doesn't parse: %v\n%s", err, code)
		}
		conf := types.Config{Importer: sourceImporter()}
		if _, err := conf.Check(file.Name.Name, fset, []*ast.File{file}, nil); err != nil {
			t.Fatalf("generated code doesn't type-check: %v\n%s\nfrom\n%s", err, code, k.Source())
		}
	})
}

// Import implements [types.Importer].
func (c *cachingImporter) Import(path string) (*types.Package, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.pkgs[path]; ok {
		return p, nil
	}
	p, err := c.from.Import(path)
	if err == nil {
		c.pkgs[path] = p
	}
	return p, err
}

// fuzzKind builds a kind from the fuzzer's input: a struct type whose
// first field nests lists, maps and optionals as shape says, inputs,
// host functions and decisions named from words, payload defaults from
// n and s, and either `collect one` with a default and a conflict
// outcome, or `collect all` with a precedence. The struct types and
// enums come in the order policy.NewKind declares them.
func fuzzKind(shape []byte, words string, n int64, s string, all bool) *kind.Kind {
	nm := &names{words: nonIdent.Split(words, -1), used: map[string]bool{}}
	level := &sigil.Enum{Name: nm.take("Level"), Values: []string{nm.take("low"), nm.take("high")}}
	spare := &sigil.Enum{Name: nm.take("Spare"), Values: []string{nm.take("spare")}}
	inner := &sigil.Struct{Name: nm.take("Inner"), Fields: []*sigil.Field{{Name: nm.take("at"), Type: sigil.Timestamp}}}
	record := &sigil.Struct{Name: nm.take("Record")}
	typ := shapeType(shape, level, inner)
	record.Fields = []*sigil.Field{{Name: nm.take("values"), Type: typ}, {Name: nm.take("label"), Type: sigil.String}}
	if len(shape)%2 == 1 {
		record.Fields = append(record.Fields, &sigil.Field{Name: nm.take("level"), Type: &sigil.Optional{Elem: level}})
	}

	deny := &kind.Decision{Name: nm.take("deny"), Reasons: []string{nm.take("fallback"), nm.take("blocked"), nm.take("clash")}}
	deny.Ranked = []string{deny.Reasons[1], deny.Reasons[2], deny.Reasons[0]}
	allow := &kind.Decision{Name: nm.take("allow"), Reasons: []string{nm.take("ok")}, Fields: []*kind.Field{
		{Name: nm.take("count"), Type: sigil.Int, HasDefault: true, Default: n},
		{Name: nm.take("text"), Type: sigil.String, HasDefault: true, Default: s},
		{Name: nm.take("ttl"), Type: sigil.Duration, HasDefault: true, Default: time.Duration(n / 1000000 * 1000000)},
		{Name: nm.take("tags"), Type: &sigil.List{Elem: sigil.String}, HasDefault: true, Default: []any{s}},
		{Name: nm.take("level"), Type: level, HasDefault: true, Default: constant.EnumValue(level.Values[uint64(n)%2])},
		{Name: nm.take("ratio"), Type: sigil.Float, HasDefault: true, Default: float64(n%1000) / 8},
		{Name: nm.take("data"), Type: &sigil.Optional{Elem: record}},
	}}
	k := &kind.Kind{
		Name: nm.take("Generated"), Version: 2, Accepts: 1 + int(uint64(n)%2),
		Enums: []*sigil.Enum{spare, level},
		Types: []*sigil.Struct{inner, record},
		Inputs: []*kind.Input{
			{Name: nm.take("record"), Type: &sigil.Optional{Elem: record}},
			{Name: nm.take("count"), Type: sigil.Int},
		},
		Funcs: []*kind.Func{
			{Name: nm.take("lookup"), Params: []sigil.Type{sigil.String, typ}, Result: sigil.Bool},
			{Name: nm.take("now"), Result: sigil.Timestamp},
		},
		Decisions: []*kind.Decision{deny, allow},
		Collect:   kind.CollectOne,
		Exclusive: [][]kind.Outcome{{{Decision: deny.Name, Reason: deny.Reasons[1]}, {Decision: allow.Name}}},
		Default:   &kind.Default{Decision: deny.Name, Reason: deny.Reasons[0], Args: map[string]any{}},
		Conflict:  &kind.Default{Decision: deny.Name, Reason: deny.Reasons[2], Args: map[string]any{}},
	}
	k.Precedence = decisionNames(k)
	if all {
		k.Collect, k.Default, k.Conflict = kind.CollectAll, nil, nil
		k.Precedence = []string{allow.Name, deny.Name}
	}
	canonical(k)
	return k
}

// canonical puts k's struct types and enums in the order policy.NewKind
// declares them.
func canonical(k *kind.Kind) {
	r := walk(k)
	byName := map[string]*sigil.Struct{}
	for _, t := range k.Types {
		byName[t.Name] = t
	}
	k.Types = k.Types[:0]
	for _, name := range r.structs {
		k.Types = append(k.Types, byName[name])
	}
	slices.SortStableFunc(k.Enums, func(a, b *sigil.Enum) int {
		return rank(r.enums, a.Name) - rank(r.enums, b.Name)
	})
}

// rank is name's position in reached, or past its end for an enum
// nothing reaches.
func rank(reached []string, name string) int {
	if i := slices.Index(reached, name); i >= 0 {
		return i
	}
	return len(reached)
}

// shapeType nests base types in lists, maps and optionals, one level per
// byte of shape, starting from int.
func shapeType(shape []byte, level *sigil.Enum, inner *sigil.Struct) sigil.Type {
	var typ sigil.Type = sigil.Int
	for _, b := range shape[:min(len(shape), 6)] {
		switch b % 8 {
		case 0:
			typ = &sigil.List{Elem: typ}
		case 1:
			typ = &sigil.Map{Key: sigil.String, Value: typ}
		case 2:
			typ = &sigil.Map{Key: sigil.Timestamp, Value: typ}
		case 3:
			typ = &sigil.Map{Key: level, Value: typ}
		case 4:
			typ = &sigil.List{Elem: level} // restart: a bare enum can't be a map value
		case 5:
			typ = inner // restart from the inner struct type
		case 6:
			if _, ok := typ.(sigil.Basic); ok {
				typ = &sigil.Optional{Elem: typ}
			}
		case 7:
			typ = sigil.Duration
		}
	}
	return typ
}

// take returns the next of the fuzzer's words as a name, or fallback
// when they've run out, made unique in the kind's namespace. Fields and
// reasons only need to be unique in their scope; unique across the kind
// is simpler, and their Go names still collide.
func (nm *names) take(fallback string) string {
	return nm.unique(nm.word(fallback), nm.used)
}

// word returns the next word, turned into a Sigil identifier that isn't a
// keyword, or fallback.
func (nm *names) word(fallback string) string {
	for nm.next < len(nm.words) {
		w := nm.words[nm.next]
		nm.next++
		if w == "" {
			continue
		}
		if w[0] >= '0' && w[0] <= '9' {
			w = "n" + w
		}
		if sigiltoken.Lookup(w) != sigiltoken.Ident || isBuiltin(w) {
			w += "_"
		}
		return w
	}
	return fallback
}

// unique returns name, or name with a number when used has it.
func (nm *names) unique(name string, used map[string]bool) string {
	for i := 2; used[name]; i++ {
		name = strings.TrimRight(name, "0123456789") + strconv.Itoa(i)
	}
	used[name] = true
	return name
}

// isBuiltin reports whether name is a built-in Sigil type name, which no
// enum or struct type may take.
func isBuiltin(name string) bool {
	switch name {
	case "bool", "int", "float", "string", "duration", "timestamp", "list", "map", "decision":
		return true
	}
	return false
}
