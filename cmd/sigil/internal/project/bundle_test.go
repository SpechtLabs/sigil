package project_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/project"
	"github.com/spechtlabs/sigil/internal/payload"
	"github.com/spechtlabs/sigil/internal/workspace"
)

// TestFromBundle checks that a bundle's files become files to load, each
// with its name for an ID, and back.
func TestFromBundle(t *testing.T) {
	kind := payload.File{Name: "access.sigil", Source: accessKind}
	main := payload.File{Name: "team/main.sigil", Source: "policy team.main: Access@1\n"}
	base := payload.File{Name: "platform/base.sigil", Source: "policy platform.base: Access@1\n"}
	file := func(f payload.File) workspace.File {
		return workspace.File{Name: f.Name, ID: f.Name, Source: []byte(f.Source)}
	}
	tests := []struct {
		name   string
		bundle payload.Bundle
		want   project.Files
	}{
		{
			name:   "empty",
			bundle: payload.Bundle{},
			want:   project.Files{Kinds: []workspace.File{}, Paths: []workspace.File{}, Trusted: []workspace.File{}},
		},
		{
			name:   "every list",
			bundle: payload.Bundle{Root: "team.main", Kinds: []payload.File{kind}, Paths: []payload.File{main, kind}, Trusted: []payload.File{base}},
			want:   project.Files{Kinds: []workspace.File{file(kind)}, Paths: []workspace.File{file(main), file(kind)}, Trusted: []workspace.File{file(base)}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := project.FromBundle(&tt.bundle)
			if !reflect.DeepEqual(*got, tt.want) {
				t.Fatalf("FromBundle() = %+v, want %+v", *got, tt.want)
			}
			back := got.Bundle()
			want := payload.Bundle{Kinds: tt.bundle.Kinds, Paths: tt.bundle.Paths, Trusted: tt.bundle.Trusted}
			if want.Digest() != back.Digest() || back.Root != "" || back.Require != nil {
				t.Errorf("Bundle() = %+v, want the files of %+v, without root and requirements", back, want)
			}
		})
	}
}

// TestRead checks what Read reads, and that Bundle keeps each file's name
// and contents, in Read's order.
func TestRead(t *testing.T) {
	dir := tree(t, map[string]string{
		"access.sigil":        accessKind,
		"platform/base.sigil": "policy platform.base: Access@1\n",
		"team/main.sigil":     "policy team.main: Access@1\n",
		"team/lib.sigil":      "module team.lib: Access@1\n",
	})
	t.Chdir(dir)
	f, err := project.Read(project.Sources{
		Paths:   []string{"team", "platform", "access.sigil"},
		Trusted: []string{"platform"},
		Kinds:   []string{"access.sigil", "./access.sigil", filepath.Join(dir, "access.sigil")},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := payload.Bundle{
		Kinds:   []payload.File{{Name: "access.sigil", Source: accessKind}},
		Paths:   []payload.File{{Name: "team/lib.sigil", Source: "module team.lib: Access@1\n"}, {Name: "team/main.sigil", Source: "policy team.main: Access@1\n"}, {Name: "access.sigil", Source: accessKind}},
		Trusted: []payload.File{{Name: "platform/base.sigil", Source: "policy platform.base: Access@1\n"}},
	}
	if got := f.Bundle(); !reflect.DeepEqual(*got, want) {
		t.Errorf("Read().Bundle() = %+v, want %+v: the kind file once, the trusted file out of the paths", *got, want)
	}
	if f.Kinds[0].ID != f.Paths[2].ID {
		t.Error("the kind file among the paths isn't the same file")
	}
}

// TestLoadFiles checks that a project loaded from a compiled bundle is the
// one loaded from disk: the files Read reads, through Bundle, Encode,
// Decode and FromBundle, load to the same groups, policies, files and
// diagnostics, and fail the same way.
func TestLoadFiles(t *testing.T) {
	dir := tree(t, map[string]string{
		"access.sigil":           accessKind,
		"roles.sigil":            rolesKind,
		"stale.sigil":            strings.Replace(accessKind, "version 1", "version 2", 1),
		"kindplus.sigil":         accessKind + "\n---\npolicy k.extra: Access@1\n",
		"access/main.sigil":      "policy access.main: Access@1\n\nlet x = nope\n",
		"access/sub/deep.sigil":  "policy access.deep: Access@1\n",
		"roles/main.sigil":       "policy roles.main: Roles@1\n",
		"platform/base.sigil":    "policy platform.base: Access@1\n",
		"dup/main.sigil":         "policy access.main: Roles@1\n",
		"parse/bad.sigil":        "policy parse.bad: Access@1\n\nlet = \n",
		"typo/main.sigil":        "policy typo.main: Acess@1\n",
		"trustedin/team.sigil":   "policy team.main: Access@1\n",
		"trustedin/plat/p.sigil": "policy plat.p: Access@1\n",
		"trustedin/plat/q.sigil": "policy team.main: Access@1\n",
	})
	t.Chdir(dir)
	access := linked(t, accessKind)
	tests := []struct {
		name   string
		src    project.Sources
		stdin  string
		linked []project.Linked
		err    string // LoadFiles fails
	}{
		{name: "two kinds", src: project.Sources{Paths: []string{"access.sigil", "roles.sigil", "access", "roles"}}},
		{name: "a --kind file among the paths too", src: project.Sources{Paths: []string{"kindplus.sigil", "access"}, Kinds: []string{"kindplus.sigil"}}},
		{name: "a --kind file named twice", src: project.Sources{Paths: []string{"access"}, Kinds: []string{"access.sigil", filepath.Join(dir, "access.sigil")}}},
		{name: "a linked kind", src: project.Sources{Paths: []string{"access"}}, linked: []project.Linked{access}},
		{name: "a stale export among the paths", src: project.Sources{Paths: []string{"stale.sigil", "access"}}, linked: []project.Linked{access}},
		{name: "two kind documents that differ", src: project.Sources{Paths: []string{"access", "stale.sigil"}, Kinds: []string{"access.sigil"}}},
		{name: "diagnostics", src: project.Sources{Paths: []string{"access.sigil", "roles.sigil", "access", "dup", "parse", "typo"}}},
		{name: "trusted", src: project.Sources{Paths: []string{"access.sigil", "trustedin"}, Trusted: []string{"trustedin/plat"}}},
		{name: "stdin", src: project.Sources{Paths: []string{"-", "access"}}, stdin: accessKind + "---\npolicy s.p: Access@1\n"},
		{name: "a --kind file that's a stale export", src: project.Sources{Paths: []string{"access"}, Kinds: []string{"stale.sigil"}}, linked: []project.Linked{access}, err: "stale.sigil doesn't match the kind Access linked into this binary"},
		{name: "a --kind file without a kind", src: project.Sources{Paths: []string{"access"}, Kinds: []string{"platform/base.sigil"}}, err: "platform/base.sigil holds no kind document"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.src.Stdin = strings.NewReader(tt.stdin)
			disk, derr := project.Load(tt.src, tt.linked)
			tt.src.Stdin = strings.NewReader(tt.stdin)
			files, err := project.Read(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			data, eerr := payload.Encode(&payload.Payload{Bundle: *files.Bundle()})
			if eerr != nil {
				t.Fatal(eerr)
			}
			p, eerr := payload.Decode(data)
			if eerr != nil {
				t.Fatal(eerr)
			}
			compiled, cerr := project.LoadFiles(project.FromBundle(&p.Bundle), tt.linked)
			if tt.err != "" {
				if derr == nil || cerr == nil || !strings.Contains(cerr.Error(), tt.err) || cerr.Display() != derr.Display() {
					t.Fatalf("LoadFiles() error = %v, Load() error = %v, want both %q", cerr, derr, tt.err)
				}
				return
			}
			if derr != nil || cerr != nil {
				t.Fatalf("Load() error = %v, LoadFiles() error = %v", derr, cerr)
			}
			if got, want := summary(compiled), summary(disk); got != want {
				t.Errorf("LoadFiles(FromBundle()) =\n%s\nwant, as Load:\n%s", got, want)
			}
		})
	}
}

// summary describes a project as the commands see it: its groups, its
// policies and names, its files, and every diagnostic after Check, with
// the source it points into.
func summary(p *project.Project) string {
	p.Check()
	var b strings.Builder
	for _, g := range p.Groups() {
		b.WriteString("group " + g.Kind.Model.Name + "\n")
	}
	b.WriteString("policies " + strings.Join(p.Policies(), ", ") + "\n")
	b.WriteString("names " + strings.Join(p.Names(), ", ") + "\n")
	b.WriteString("files " + strconv.Itoa(p.Files()) + "\n")
	b.WriteString(p.Render(p.Errors()))
	return b.String()
}

// tree writes files into a new temporary directory and returns it.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, src := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}
