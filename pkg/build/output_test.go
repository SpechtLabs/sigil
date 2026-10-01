package build_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/spechtlabs/sigil/pkg/build"
)

// unreadableFS is a file system nothing can be read from.
type unreadableFS struct{}

// sharedDirFS hands out one slice for every listing of dir, as a file
// system that caches its listings may.
type sharedDirFS struct {
	fstest.MapFS
	dir     string
	entries []fs.DirEntry
}

func TestFS(t *testing.T) {
	c := common()
	fsys, err := build.FS(c, guardrails(c))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Deploy.Load(fsys, "deploy.guardrails")
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Eval(t.Context(), Input{Release: Release{Soak: time.Hour}})
	if err != nil {
		t.Fatal(err)
	}
	if !Deny.Reason("not_eligible").Is(res) {
		t.Errorf("got %v, want deny(reason: not_eligible)", res.Outcome)
	}
}

// TestRenderAll covers what every output function reports before it
// writes anything: documents that don't render, and documents that
// collide.
func TestRenderAll(t *testing.T) {
	broken := build.Module("broken", Access, func(m *build.ModuleDoc[Request], in *Request) {
		build.Let(m, "x", build.Lit(time.Time{}))
	})
	a := build.Module("a.b", Access, nil)
	samePath := build.Module("a_b", Access, nil, build.WithPath("a/b.sigil"))
	sameName := build.Module("a.b", Access, nil, build.WithPath("other.sigil"))
	otherCase := build.Module("x.y", Access, nil, build.WithPath("A/b.sigil"))
	var nilDoc *build.ModuleDoc[Request]
	tests := []struct {
		name string
		docs []build.Doc
		err  string
	}{
		{name: "broken", docs: []build.Doc{broken}, err: "timestamp has no literal"},
		{name: "same path", docs: []build.Doc{a, samePath}, err: "a.b and a_b both render to a/b.sigil"},
		{name: "same name", docs: []build.Doc{a, sameName}, err: "two documents are named a.b"},
		{name: "nil", docs: []build.Doc{a, nilDoc}, err: "document 2 is nil"},
		{name: "paths that differ in case", docs: []build.Doc{a, otherCase}, err: "a/b.sigil and A/b.sigil render to paths that differ only in case"},
	}
	outputs := map[string]func(docs []build.Doc) error{
		"FS": func(docs []build.Doc) error {
			_, err := build.FS(docs...)
			return err
		},
		"Write": func(docs []build.Doc) error { return build.Write(t.TempDir(), docs...) },
		"Diff":  func(docs []build.Doc) error { return build.Diff(fstest.MapFS{}, docs...) },
		"Check": func(docs []build.Doc) error { return build.Check(Access, nil, docs...) },
	}
	for _, tt := range tests {
		for name, output := range outputs {
			t.Run(tt.name+"/"+name, func(t *testing.T) {
				err := output(tt.docs)
				wantError(t, err, 0, tt.err)
			})
		}
	}
}

func TestWrite(t *testing.T) {
	dir := t.TempDir()
	c := common()
	if err := build.Write(dir, c, guardrails(c)); err != nil {
		t.Fatal(err)
	}
	if err := build.Diff(os.DirFS(dir), c, guardrails(c)); err != nil {
		t.Errorf("Diff after Write: %v", err)
	}

	// A file where a directory has to go.
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, "deploy"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := build.Write(blocked, c); err == nil || !strings.Contains(err.Error(), "creating the directory for deploy/common.sigil") {
		t.Errorf("got %v, want an error creating the directory", err)
	}

	// A directory where the file has to go.
	taken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(taken, "deploy", "common.sigil"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := build.Write(taken, c); err == nil || !strings.Contains(err.Error(), "writing deploy/common.sigil") {
		t.Errorf("got %v, want an error writing the file", err)
	}
}

func TestDiff(t *testing.T) {
	c := common()
	g := guardrails(c)
	src, err := c.Source()
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(src), "owns_service", "owns", 1)
	tests := []struct {
		name    string
		fsys    fs.FS
		stale   []string
		missing []string
		msg     []string
	}{
		{
			name: "current",
			fsys: fstest.MapFS{"deploy/common.sigil": {Data: src}, "deploy/guardrails.sigil": {Data: mustSource(t, g)}},
		},
		{
			name:    "stale and missing",
			fsys:    fstest.MapFS{"deploy/common.sigil": {Data: []byte(stale)}},
			stale:   []string{"deploy/common.sigil"},
			missing: []string{"deploy/guardrails.sigil"},
			msg: []string{
				"build: 2 rendered file(s) out of date; regenerate them from the Go code",
				"deploy/common.sigil is stale:\n--- deploy/common.sigil (on disk)\n+++ deploy/common.sigil (rendered)",
				"-pub let owns = ",
				"+pub let owns_service = ",
				"deploy/guardrails.sigil is missing",
			},
		},
		{
			name: "line endings",
			fsys: fstest.MapFS{
				"deploy/common.sigil":     {Data: []byte(strings.ReplaceAll(string(src), "\n", "\r\n"))},
				"deploy/guardrails.sigil": {Data: mustSource(t, g)},
			},
			stale: []string{"deploy/common.sigil"},
			msg: []string{
				"deploy/common.sigil is stale:\nonly the line endings differ: CRLF on disk, LF rendered",
				"add `*.sigil text eol=lf` to .gitattributes",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := build.Diff(tt.fsys, c, g)
			if tt.stale == nil && tt.missing == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			var drift *build.DriftError
			if !errors.As(err, &drift) {
				t.Fatalf("got %v, want a *build.DriftError", err)
			}
			if strings.Join(drift.Stale, ",") != strings.Join(tt.stale, ",") || strings.Join(drift.Missing, ",") != strings.Join(tt.missing, ",") {
				t.Errorf("stale %v, missing %v; want %v, %v", drift.Stale, drift.Missing, tt.stale, tt.missing)
			}
			for _, m := range tt.msg {
				if !strings.Contains(err.Error(), m) {
					t.Errorf("message lacks %q:\n%v", m, err)
				}
			}
		})
	}

	// A file that can't be read.
	unreadable := fstest.MapFS{"deploy/common.sigil": {Mode: fs.ModeDir}}
	if err := build.Diff(unreadable, c); err == nil || !strings.Contains(err.Error(), "build: reading deploy/common.sigil") {
		t.Errorf("got %v, want a read error", err)
	}
}

func TestCheck(t *testing.T) {
	var whenLine, pubLine, refLine int
	broken := build.Policy("access.broken", Access, func(p *build.PolicyDoc[Request], in *Request) {
		p.When(build.Field(&in.Score).Eq(build.Raw[float64]("1")), func(*build.Block) {})
		whenLine = line() - 1
		pubLine = line(build.Pub(p, "admin", build.Lit(true)))
		refLine = line(build.Let(p, "x", build.Ref[bool](build.Extern("access.missing"), "y")))
	})

	err := build.Check(Access, nil, broken)
	var ce *build.CheckError
	if !errors.As(err, &ce) {
		t.Fatalf("got %v, want a *build.CheckError", err)
	}
	wantSites := map[string]int{
		"`==` needs operands of the same type, found float and int": whenLine,
		"`admin` is already the name of an enum value":              pubLine,
		"unknown document `access.missing`":                         refLine,
		"unknown name `y`":                                          refLine,
	}
	if len(ce.Diagnostics) != len(wantSites) {
		t.Fatalf("got %d diagnostics, want %d:\n%v", len(ce.Diagnostics), len(wantSites), err)
	}
	for _, d := range ce.Diagnostics {
		l, ok := wantSites[d.Message]
		switch {
		case !ok:
			t.Errorf("unexpected diagnostic %q", d.Message)
		case d.Site == nil || d.Site.Line != l:
			t.Errorf("%q: site %v, want line %d", d.Message, d.Site, l)
		case d.Position.File != "access/broken.sigil" || d.Position.Line == 0:
			t.Errorf("%q: position %v", d.Message, d.Position)
		}
	}
	for _, want := range []string{
		"access/broken.sigil:7:6: error: `==` needs operands of the same type, found float and int",
		"  = go: output_test.go:" + strconv.Itoa(whenLine) + ": (*PolicyDoc).When",
		"  = go: output_test.go:" + strconv.Itoa(pubLine) + ": build.Pub",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%v", want, err)
		}
	}

	t.Run("handwritten document", func(t *testing.T) {
		base := fstest.MapFS{
			"access/hand.sigil":   {Data: []byte("module access.hand: Access@2\n\npub let x = nope\n")},
			"access/broken.sigil": {Data: []byte("this file is replaced")},
			".hidden.sigil":       {Data: []byte("skipped")},
		}
		ok := build.Policy("access.broken", Access, func(p *build.PolicyDoc[Request], in *Request) {
			build.Let(p, "y", build.Ref[bool](build.Extern("access.hand"), "x"))
		})
		err := build.Check(Access, base, ok)
		var ce *build.CheckError
		if !errors.As(err, &ce) || len(ce.Diagnostics) != 1 || ce.Diagnostics[0].Site != nil {
			t.Fatalf("got %v, want one diagnostic without a site", err)
		}
		if strings.Contains(err.Error(), "= go:") {
			t.Errorf("a handwritten document's diagnostic names a Go site:\n%v", err)
		}
	})

	t.Run("unreadable base", func(t *testing.T) {
		if err := build.Check(Access, unreadableFS{}, build.Module("a", Access, nil)); err == nil || !strings.Contains(err.Error(), "build: reading the base documents") {
			t.Errorf("got %v, want a read error", err)
		}
	})

	t.Run("base that shares its listing", func(t *testing.T) {
		mapFS := fstest.MapFS{
			"access/broken.sigil": {Data: []byte("replaced by the rendered document")},
			"access/other.sigil":  {Data: []byte("module access.other: Access@2\n")},
		}
		listing, err := mapFS.ReadDir("access")
		if err != nil {
			t.Fatal(err)
		}
		base := sharedDirFS{MapFS: mapFS, dir: "access", entries: listing}
		ok := build.Policy("access.broken", Access, nil)
		if err := build.Check(Access, base, ok); err != nil {
			t.Fatal(err)
		}
		if listing[0].Name() != "broken.sigil" || listing[1].Name() != "other.sigil" {
			t.Errorf("Check changed the base's listing: %v", listing)
		}
	})

	t.Run("another kind", func(t *testing.T) {
		err := build.Check(Access, nil, common())
		wantError(t, err, 0, "deploy.common is built for kind DeployApproval, not Access")
	})

	t.Run("nil kind", func(t *testing.T) {
		wantError(t, build.Check[Request](nil, nil), 0, "the kind is nil")
	})
}

func (s sharedDirFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name == s.dir {
		return s.entries, nil
	}
	return s.MapFS.ReadDir(name)
}

func (unreadableFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrPermission}
}

// mustSource renders d.
func mustSource(t *testing.T, d build.Doc) []byte {
	t.Helper()
	src, err := d.Source()
	if err != nil {
		t.Fatal(err)
	}
	return src
}
