package project_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/check"
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

func TestLoadKind(t *testing.T) {
	dir := t.TempDir()
	write := func(name, src string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	same := write("access.sigil", accessKind)
	stale := write("stale.sigil", strings.Replace(accessKind, "version 1", "version 2", 1))
	other := write("roles.sigil", rolesKind)
	broken := write("broken.sigil", "kind Broken version 1\n\ninput x: nope\n")

	access, roles := linked(t, accessKind), linked(t, rolesKind)

	tests := []struct {
		name   string
		file   string
		linked []project.Linked
		kind   string // the loaded kind's name
		host   bool
		err    string
		advice string
	}{
		{name: "no file, nothing linked", err: "no kind file given", advice: "--kind"},
		{name: "no file, one kind linked", linked: []project.Linked{access}, kind: "Access", host: true},
		{name: "no file, two kinds linked", linked: []project.Linked{access, roles}, err: "this binary links several kinds", advice: "linked: Access, Roles"},
		{name: "file matches the linked kind", file: same, linked: []project.Linked{roles, access}, kind: "Access", host: true},
		{name: "file is a stale export", file: stale, linked: []project.Linked{access}, err: "doesn't match the kind Access linked into this binary", advice: "`export Access --out " + stale + "`"},
		{name: "file for an unlinked kind", file: other, linked: []project.Linked{access}, kind: "Roles"},
		{name: "file without linked kinds", file: same, kind: "Access"},
		{name: "missing file", file: filepath.Join(dir, "nope.sigil"), err: "the kind file couldn't be read"},
		{name: "invalid kind file", file: broken, err: "nope", advice: "regenerate it from the host's Schema()"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			k, err := project.LoadKind(tt.file, tt.linked)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("LoadKind() error = %v, want %q", err, tt.err)
				}
				if advice := strings.Join(err.Advice(), "; "); !strings.Contains(advice, tt.advice) {
					t.Errorf("advice = %q, want %q", advice, tt.advice)
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadKind() error = %v", err)
			}
			if k.Model.Name != tt.kind || k.Host != tt.host || k.Binding == nil {
				t.Errorf("LoadKind() = %s, host %v, binding %v; want %s, host %v", k.Model.Name, k.Host, k.Binding != nil, tt.kind, tt.host)
			}
			if tt.host {
				for _, l := range tt.linked {
					if l.Model.Name == tt.kind && (k.Model != l.Model || k.Binding != l.Binding) {
						t.Error("LoadKind() didn't use the linked kind's model and binding")
					}
				}
			}
		})
	}
}

func TestBundle(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"platform/base.sigil": "policy platform.base: Access@1\n",
		"team/main.sigil":     "policy team.main: Access@1\n",
		"team/sub/deep.sigil": "policy team.deep: Access@1\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	k, err := project.LoadKind("", []project.Linked{linked(t, accessKind)})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		src  project.Sources
		want []string
		doc  string // a trusted document the bundle must resolve
		err  string
	}{
		{name: "one directory", src: project.Sources{Paths: []string{filepath.Join(dir, "team")}}, want: []string{"team.main"}},
		{name: "recursive", src: project.Sources{Paths: []string{filepath.Join(dir, "team")}, Recursive: true}, want: []string{"team.main", "team.deep"}},
		{name: "trusted", src: project.Sources{Paths: []string{filepath.Join(dir, "team")}, Trusted: []string{filepath.Join(dir, "platform")}}, want: []string{"team.main"}, doc: "platform.base"},
		{name: "stdin", src: project.Sources{Paths: []string{"-"}, Stdin: strings.NewReader("policy s.p: Access@1\n")}, want: []string{"s.p"}},
		{name: "missing path", src: project.Sources{Paths: []string{filepath.Join(dir, "nope")}}, err: "can't be read"},
		{name: "missing trusted path", src: project.Sources{Paths: []string{"-"}, Trusted: []string{filepath.Join(dir, "nope")}}, err: "can't be read"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, err := k.Bundle(tt.src)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("Bundle() error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Bundle() error = %v", err)
			}
			if got := b.Policies(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Policies() = %v, want %v", got, tt.want)
			}
			if tt.doc != "" {
				if d := b.Document(tt.doc); d == nil || !d.Trusted {
					t.Errorf("Document(%s) = %v, want a trusted document", tt.doc, d)
				}
			}
		})
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
