package project_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/check"
	"github.com/spechtlabs/sigil/internal/diag"
	"github.com/spechtlabs/sigil/internal/gokind"
)

const (
	accessKind = `kind Access version 1

input user: string

decision deny {
  reason: no_rule_matched
}

collect one
precedence deny

default deny(reason: no_rule_matched)
`
	rolesKind = `kind Roles version 1

input user: string

decision read {
  reason: member
}

collect all
`
)

// TestLoad loads a fixture tree in many ways and checks what the project
// holds: its groups, its policies, the files it read and the diagnostics
// loading and checking found.
func TestLoad(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"access.sigil":           accessKind,
		"roles.sigil":            rolesKind,
		"stale.sigil":            strings.Replace(accessKind, "version 1", "version 2", 1),
		"broken.sigil":           "kind Broken version 1\n\ninput x: nope\n",
		"bundle.sigil":           accessKind + "\n---\npolicy b.main: Access@1\n",
		"kindplus.sigil":         accessKind + "\n---\npolicy k.extra: Access@1\n",
		"access/main.sigil":      "policy access.main: Access@1\n",
		"access/sub/deep.sigil":  "policy access.deep: Access@1\n",
		"access/.hidden.sigil":   "policy access.hidden: Access@1\n",
		"access/notes.txt":       "not sigil",
		"roles/main.sigil":       "policy roles.main: Roles@1\n",
		"platform/base.sigil":    "policy platform.base: Access@1\n",
		"typo/main.sigil":        "policy typo.main: Acess@1\n",
		"typo/lib.sigil":         "module typo.lib: Acess@1\n",
		"dup/main.sigil":         "policy access.main: Roles@1\n",
		"parse/bad.sigil":        "policy parse.bad: Access@1\n\nlet = \n",
		"brokenuse/main.sigil":   "policy brokenuse.main: Broken@1\n",
		"useskind/main.sigil":    "policy useskind.main: Access@1\n\nuse Access\n",
		"trustedin/team.sigil":   "policy team.main: Access@1\n",
		"trustedin/plat/p.sigil": "policy plat.p: Access@1\n",
	}
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	in := func(names ...string) []string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = filepath.Join(dir, n)
			if n == "-" {
				out[i] = n
			}
		}
		return out
	}
	access, roles := linked(t, accessKind), linked(t, rolesKind)

	tests := []struct {
		name     string
		src      project.Sources
		linked   []project.Linked
		groups   []string // kind names
		policies []string
		files    int
		diags    []string // every diagnostic, by message, in order
		help     string   // part of the first diagnostic's help
		host     bool     // the first group's kind is the linked one
		err      string   // Load fails
		advice   string
	}{
		{
			name:     "a kind file among the paths",
			src:      project.Sources{Paths: in("access.sigil", "access")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    3,
		},
		{
			name:     "a self-contained file",
			src:      project.Sources{Paths: in("bundle.sigil")},
			groups:   []string{"Access"},
			policies: []string{"b.main"},
			files:    1,
		},
		{
			name:     "stdin",
			src:      project.Sources{Paths: in("-"), Stdin: strings.NewReader(accessKind + "---\npolicy s.p: Access@1\n")},
			groups:   []string{"Access"},
			policies: []string{"s.p"},
			files:    1,
		},
		{
			name:     "two kinds",
			src:      project.Sources{Paths: in("access.sigil", "roles.sigil", "access", "roles")},
			groups:   []string{"Access", "Roles"},
			policies: []string{"access.deep", "access.main", "roles.main"},
			files:    5,
		},
		{
			name:     "a --kind file",
			src:      project.Sources{Paths: in("access"), Kinds: in("access.sigil")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    2,
		},
		{
			name:     "a --kind file's other documents",
			src:      project.Sources{Paths: in("access"), Kinds: in("kindplus.sigil")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    2,
		},
		{
			name:     "a --kind file among the paths too",
			src:      project.Sources{Paths: in("kindplus.sigil", "access"), Kinds: in("kindplus.sigil")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main", "k.extra"},
			files:    3,
		},
		{
			name:     "a path named twice",
			src:      project.Sources{Paths: in("access.sigil", "access", "access/main.sigil")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    3,
		},
		{
			name:     "a linked kind",
			src:      project.Sources{Paths: in("access")},
			linked:   []project.Linked{roles, access},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    2,
			host:     true,
		},
		{
			name:     "a kind file matching the linked kind",
			src:      project.Sources{Paths: in("access.sigil", "access"), Kinds: in("access.sigil")},
			linked:   []project.Linked{access},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    3,
			host:     true,
		},
		{
			name:   "a --kind file that's a stale export",
			src:    project.Sources{Paths: in("access"), Kinds: in("stale.sigil")},
			linked: []project.Linked{access},
			err:    "stale.sigil doesn't match the kind Access linked into this binary",
			advice: "export Access --out " + filepath.ToSlash(filepath.Join(dir, "stale.sigil")),
		},
		{
			name:     "a stale export among the paths",
			src:      project.Sources{Paths: in("stale.sigil", "access")},
			linked:   []project.Linked{access},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    3,
			diags:    []string{"kind document Access doesn't match the kind Access linked into this binary"},
			help:     "export Access --out",
			host:     true,
		},
		{
			name:     "two kind documents that differ",
			src:      project.Sources{Paths: in("access", "stale.sigil"), Kinds: in("access.sigil")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    3,
			diags:    []string{"kind document Access differs from the one at " + filepath.ToSlash(filepath.Join(dir, "access.sigil")) + ":1:6"},
		},
		{
			name:     "an unknown kind",
			src:      project.Sources{Paths: in("access.sigil", "typo")},
			files:    3,
			policies: nil,
			diags:    []string{"module typo.lib is written against kind Acess, but no kind Acess was found", "policy typo.main is written against kind Acess, but no kind Acess was found"},
			help:     "did you mean `Access`? add its kind file to the paths or name it with --kind",
		},
		{
			name:  "an unknown kind, nothing close",
			src:   project.Sources{Paths: in("roles")},
			files: 1,
			diags: []string{"policy roles.main is written against kind Roles, but no kind Roles was found"},
			help:  "add its kind file",
		},
		{
			name:     "a name defined twice across kinds",
			src:      project.Sources{Paths: in("access.sigil", "roles.sigil", "access", "dup")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    5,
			diags:    []string{"policy access.main is defined twice"},
			help:     "first defined at " + filepath.ToSlash(filepath.Join(dir, "access", "main.sigil")) + ":1:1",
		},
		{
			name:     "a parse error, reported once",
			src:      project.Sources{Paths: in("access.sigil", "roles.sigil", "parse", "roles")},
			groups:   []string{"Access", "Roles"},
			policies: []string{"parse.bad", "roles.main"},
			files:    4,
			diags:    []string{"expected a name after `let`, found `=`"},
		},
		{
			name:  "a kind document that doesn't check",
			src:   project.Sources{Paths: in("broken.sigil", "brokenuse")},
			files: 2,
			diags: []string{"kind Broken declares no decisions", "unknown type `nope`"},
		},
		{
			name:     "use of a kind",
			src:      project.Sources{Paths: in("access.sigil", "useskind")},
			groups:   []string{"Access"},
			policies: []string{"useskind.main"},
			files:    2,
			diags:    []string{"`Access` is a kind, not a policy or module"},
		},
		{
			name:     "trusted",
			src:      project.Sources{Paths: in("access.sigil", "access"), Trusted: in("platform")},
			groups:   []string{"Access"},
			policies: []string{"access.deep", "access.main"},
			files:    3,
		},
		{
			name:     "trusted inside the paths",
			src:      project.Sources{Paths: in("access.sigil", "trustedin"), Trusted: in("trustedin/plat")},
			groups:   []string{"Access"},
			policies: []string{"team.main"},
			files:    2,
		},
		{
			name:     "a trusted path among the paths",
			src:      project.Sources{Paths: in("access.sigil", "platform"), Trusted: in("platform")},
			groups:   []string{"Access"},
			files:    1,
			policies: nil,
		},
		{name: "a missing path", src: project.Sources{Paths: in("nope")}, err: "can't be read"},
		{name: "a missing trusted path", src: project.Sources{Paths: in("access"), Trusted: in("nope")}, err: "can't be read"},
		{name: "a missing kind file", src: project.Sources{Paths: in("access"), Kinds: in("nope.sigil")}, err: "the kind file couldn't be read", advice: "--kind"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := project.Load(tt.src, tt.linked)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Load() error = %v, want %q", err, tt.err)
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.advice) {
					t.Errorf("advice = %q, want %q", advice, tt.advice)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			p.Check()
			var groups []string
			for _, g := range p.Groups() {
				groups = append(groups, g.Kind.Model.Name)
				if g.Kind.Binding == nil {
					t.Errorf("group %s has no binding", g.Kind.Model.Name)
				}
			}
			if !reflect.DeepEqual(groups, tt.groups) {
				t.Errorf("Groups() = %v, want %v", groups, tt.groups)
			}
			if got := p.Policies(); !reflect.DeepEqual(got, tt.policies) {
				t.Errorf("Policies() = %v, want %v", got, tt.policies)
			}
			if p.Files() != tt.files {
				t.Errorf("Files() = %d, want %d", p.Files(), tt.files)
			}
			var diags []string
			for _, e := range p.Errors() {
				diags = append(diags, e.Msg)
			}
			if !reflect.DeepEqual(diags, tt.diags) {
				t.Fatalf("Errors() = %q, want %q", diags, tt.diags)
			}
			if tt.help != "" && !strings.Contains(p.Errors()[0].Help, tt.help) {
				t.Errorf("help = %q, want %q", p.Errors()[0].Help, tt.help)
			}
			if tt.host != (len(p.Groups()) > 0 && p.Groups()[0].Kind.Host) {
				t.Errorf("host = %v, want %v", !tt.host, tt.host)
			}
			for _, g := range p.Groups() {
				for _, name := range g.Bundle.Policies() {
					if p.Group(name) != g {
						t.Errorf("Group(%s) isn't the group holding it", name)
					}
				}
			}
		})
	}
}

// TestTrusted checks that a trusted document resolves in its kind's
// group, as trusted, and that a regular document can't take its name.
func TestTrusted(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"access.sigil":        accessKind,
		"platform/base.sigil": "policy platform.base: Access@1\n",
		"team/main.sigil":     "policy team.main: Access@1\n",
		"team/base.sigil":     "policy platform.base: Access@1\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := project.Load(project.Sources{
		Paths:   []string{filepath.Join(dir, "team")},
		Trusted: []string{filepath.Join(dir, "platform")},
		Kinds:   []string{filepath.Join(dir, "access.sigil")},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := p.Group("platform.base")
	if g == nil {
		t.Fatal("Group(platform.base) = nil, want the Access group")
	}
	if d := g.Bundle.Document("platform.base"); d == nil || !d.Trusted {
		t.Errorf("Document(platform.base) = %v, want a trusted document", d)
	}
	errs := p.Errors()
	if len(errs) != 1 || !strings.Contains(errs[0].Help, "the name belongs to the trusted source") || errs[0].Doc != "platform.base" {
		t.Fatalf("Errors() = %v, want the trusted name taken, in the second platform.base", errs)
	}
	if got := p.Policies(); !reflect.DeepEqual(got, []string{"team.main"}) {
		t.Errorf("Policies() = %v, want the bundle's own", got)
	}
	if got := p.Names(); !reflect.DeepEqual(got, []string{"platform.base", "team.main"}) {
		t.Errorf("Names() = %v, want the trusted name too", got)
	}
}

// TestResolve checks that diagnostics are named by their document,
// sorted, and rendered with the source they point into.
func TestResolve(t *testing.T) {
	src := accessKind + "---\npolicy p.first: Access@1\n\nlet x = nope\n---\npolicy p.second: Access@1\n\nlet y = nada\n"
	p, err := project.Load(project.Sources{Paths: []string{"-"}, Stdin: strings.NewReader(src)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p.Check()
	p.Check()
	errs := p.Resolve(append(p.Errors(), &diag.Error{Msg: "nowhere"}))
	if len(errs) != 3 || errs[0].Doc != "" || errs[0].Msg != "nowhere" || errs[1].Doc != "p.first" || errs[2].Doc != "p.second" {
		t.Fatalf("Resolve() = %v, want one diagnostic in each policy", errs)
	}
	if p.SourceOf("<stdin>") == nil || p.SourceOf("nope.sigil") != nil {
		t.Error("SourceOf() doesn't return exactly the files read")
	}
	out := p.Render(p.Errors())
	if !strings.Contains(out, "<stdin>:") || !strings.Contains(out, "let x = nope") {
		t.Errorf("Render() = %q, want positions and source lines", out)
	}
}

// TestExpand checks the path rules every command shares.
func TestExpand(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.sigil", "b_test.yaml", "notes.txt", "sub/c.sigil", "sub/deep/d.sigil", ".hidden/e.sigil", "..data/f.sigil", "sub/.g.sigil"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "..data", "f.sigil"), filepath.Join(dir, "link.sigil")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "nope"), filepath.Join(dir, "sub", "deep", "dangling.sigil")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	tests := []struct {
		name  string
		paths []string
		match func(string) bool
		want  []string
		err   string
	}{
		{name: "a dangling link below a directory", paths: []string{"sub"}, match: project.IsSigil, err: "sub/deep/dangling.sigil is a symbolic link to nothing that can be read"},
		{name: "a named file, whatever its name", paths: []string{"notes.txt", "-", "a.sigil"}, match: project.IsSigil, want: []string{"notes.txt", "-", "a.sigil"}},
		{name: "named twice or two ways", paths: []string{"a.sigil", "./a.sigil", "sub/c.sigil", "sub/../sub/c.sigil"}, match: project.IsSigil, want: []string{"a.sigil", "sub/c.sigil"}},
		{name: "a missing path", paths: []string{"nope"}, match: project.IsSigil, err: "nope can't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := project.Expand(tt.paths, tt.match)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Expand() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Expand() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}

	if err := os.Remove(filepath.Join(dir, "sub", "deep", "dangling.sigil")); err != nil {
		t.Fatal(err)
	}
	got, err := project.Expand([]string{"."}, func(n string) bool { return project.IsSigil(n) || strings.HasSuffix(n, "_test.yaml") })
	want := []string{"a.sigil", "b_test.yaml", "link.sigil", "sub/c.sigil", "sub/deep/d.sigil"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("Expand(.) = %q, %v, want %q: dot entries skipped, links followed, files before subdirectories", got, err, want)
	}
}

func TestMatch(t *testing.T) {
	policies := []string{"deploy.production", "payments.production", "payments.staging"}
	tests := []struct {
		name     string
		patterns []string
		want     []string
		err      string
		advice   string
	}{
		{name: "a name", patterns: []string{"payments.staging"}, want: []string{"payments.staging"}},
		{name: "a pattern", patterns: []string{"payments.*"}, want: []string{"payments.production", "payments.staging"}},
		{name: "star crosses dots", patterns: []string{"*production"}, want: []string{"deploy.production", "payments.production"}},
		{name: "patterns in order, deduplicated", patterns: []string{"payments.staging", "*"}, want: []string{"payments.staging", "deploy.production", "payments.production"}},
		{name: "regexp characters are literal", patterns: []string{"payments.(staging)"}, err: `no policy matches "payments.(staging)"`},
		{name: "no match", patterns: []string{"deploy.*", "nope"}, err: `no policy matches "nope"`, advice: "the bundle defines: deploy.production, payments.production, payments.staging"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := project.Match(policies, tt.patterns)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Match() error = %v, want %q", err, tt.err)
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.advice) {
					t.Errorf("advice = %q, want %q", advice, tt.advice)
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Match() = %v, %v, want %v", got, err, tt.want)
			}
		})
	}

	if _, err := project.Match(nil, []string{"x"}); err == nil || !strings.Contains(strings.Join(err.Advice(), ""), "the bundle defines no policies") {
		t.Errorf("Match() on no policies = %v", err)
	}
}

func TestRoot(t *testing.T) {
	tests := []struct {
		name     string
		policies []string
		policy   string
		want     string
		err      string
	}{
		{name: "named", policies: []string{"a.x", "b.y"}, policy: "b.y", want: "b.y"},
		{name: "a pattern matching one", policies: []string{"a.x", "b.y"}, policy: "b.*", want: "b.y"},
		{name: "a pattern matching several", policies: []string{"a.x", "a.y"}, policy: "a.*", err: `"a.*" matches 2 policies`},
		{name: "named but missing", policies: []string{"a.x"}, policy: "b.y", err: `no policy matches "b.y"`},
		{name: "the only policy", policies: []string{"a.x"}, want: "a.x"},
		{name: "no policies", err: "the bundle holds no policies"},
		{name: "several without a name", policies: []string{"a.x", "b.y"}, err: "the bundle holds several policies"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := project.Root(tt.policies, tt.policy)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Root() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Errorf("Root() = %q, %v, want %q", got, err, tt.want)
			}
		})
	}
}

// linked loads a kind as if a host had linked it into the binary.
func linked(t *testing.T, src string) project.Linked {
	t.Helper()
	k, errs := check.LoadKind("linked.sigil", []byte(src))
	if errs != nil {
		t.Fatalf("LoadKind: %v", errs)
	}
	return project.Linked{Model: k, Binding: gokind.Synthesize(k)}
}

// TestIdentity checks that a file named once relatively and once
// absolutely, as a path argument and a path from sigil.yaml can be, is
// read once, and that a trusted file named the other way is still left
// out of the regular files.
func TestIdentity(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"access.sigil":        accessKind,
		"platform/base.sigil": "policy platform.base: Access@1\n",
		"team/main.sigil":     "policy team.main: Access@1\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(dir)
	files, err := project.Expand([]string{"team", filepath.Join(dir, "team", "main.sigil")}, project.IsSigil)
	if err != nil || !reflect.DeepEqual(files, []string{"team/main.sigil"}) {
		t.Fatalf("Expand() = %v, %v, want team/main.sigil once", files, err)
	}
	p, herr := project.Load(project.Sources{
		Paths:   []string{".", filepath.Join(dir, "access.sigil")},
		Trusted: []string{filepath.Join(dir, "platform")},
		Kinds:   []string{filepath.Join(dir, "access.sigil")},
	}, nil)
	if herr != nil {
		t.Fatal(herr)
	}
	if errs := p.Errors(); errs != nil || p.Files() != 2 {
		t.Errorf("Load() read %d files with %v, want access.sigil and team/main.sigil once each and no errors", p.Files(), errs)
	}
}
