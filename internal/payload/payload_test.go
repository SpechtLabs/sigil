package payload_test

import (
	"slices"
	"testing"

	"github.com/spechtlabs/sigil/internal/payload"
)

// baseDigest is the digest of base(). It pins the canonical encoding: a
// change to it changes every compiled binary's digest, so it has to be
// deliberate.
const baseDigest = "sha256:38352239aa50158ca2d3abc17c9e31957f6f5c596a16bd5dd68b96245610495b"

// TestMarker checks that the marker is what stamp looks for, and that
// the slice Marker returns is a copy.
func TestMarker(t *testing.T) {
	m := payload.Marker()
	if string(m) != marker() {
		t.Fatalf("Marker() = %q, want %q", m, marker())
	}
	m[0] = 'X'
	if string(payload.Marker()) != marker() {
		t.Error("changing what Marker returned changed the area")
	}
}

// TestEmbedded checks that a binary nothing was compiled into, such as
// this test, has no payload.
func TestEmbedded(t *testing.T) {
	p, err := payload.Embedded()
	if p != nil || err != nil {
		t.Errorf("Embedded() = %v, %v, want nil, nil", p, err)
	}
}

// TestDigest checks that the digest depends on every part of the bundle's
// content, each boundary included, and on nothing else.
func TestDigest(t *testing.T) {
	if got := base().Digest(); got != baseDigest {
		t.Fatalf("Digest() = %s, want %s: the canonical encoding changed", got, baseDigest)
	}
	empty := &payload.Bundle{Kinds: []payload.File{}, Paths: []payload.File{}, Require: []payload.Requirement{{Policy: "a.b", Roots: []string{}}}}
	if empty.Digest() != (&payload.Bundle{Paths: nil, Require: []payload.Requirement{{Policy: "a.b"}}}).Digest() {
		t.Error("an empty list and a nil one digest differently, though they encode alike")
	}
	tests := []struct {
		name   string
		change func(b *payload.Bundle)
		same   bool
	}{
		{name: "nothing", change: func(*payload.Bundle) {}, same: true},
		{name: "a copy of the files", change: func(b *payload.Bundle) { b.Paths = slices.Clone(b.Paths) }, same: true},
		{name: "the root", change: func(b *payload.Bundle) { b.Root = "team.other" }},
		{name: "no root", change: func(b *payload.Bundle) { b.Root = "" }},
		{name: "a byte of a path's source", change: func(b *payload.Bundle) { b.Paths[0].Source += " " }},
		{name: "a byte of a kind file's source", change: func(b *payload.Bundle) { b.Kinds[0].Source = b.Kinds[0].Source[1:] }},
		{name: "a byte of a trusted file's source", change: func(b *payload.Bundle) { b.Trusted[0].Source += "\n" }},
		{name: "a file's name", change: func(b *payload.Bundle) { b.Paths[0].Name = "team/Main.sigil" }},
		{name: "a byte moved from name to source", change: func(b *payload.Bundle) {
			b.Paths[0] = payload.File{Name: b.Paths[0].Name[:len(b.Paths[0].Name)-1], Source: "l" + b.Paths[0].Source}
		}},
		{name: "the order of the paths", change: func(b *payload.Bundle) { b.Paths[0], b.Paths[1] = b.Paths[1], b.Paths[0] }},
		{name: "a path read as trusted", change: func(b *payload.Bundle) {
			b.Trusted, b.Paths = append(b.Trusted, b.Paths[1]), b.Paths[:1]
		}},
		{name: "a kind file read as a path", change: func(b *payload.Bundle) { b.Paths, b.Kinds = append(b.Kinds, b.Paths...), nil }},
		{name: "a requirement's policy", change: func(b *payload.Bundle) { b.Require[0].Policy = "platform.other" }},
		{name: "a requirement's trusted path", change: func(b *payload.Bundle) { b.Require[0].Trusted = []string{"platform/"} }},
		{name: "a requirement's root moved to trusted", change: func(b *payload.Bundle) {
			b.Require[0].Trusted, b.Require[0].Roots = append(b.Require[0].Trusted, b.Require[0].Roots...), nil
		}},
		{name: "another requirement", change: func(b *payload.Bundle) { b.Require = append(b.Require, payload.Requirement{Policy: "x.y"}) }},
		{name: "no requirements", change: func(b *payload.Bundle) { b.Require = nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := base()
			tt.change(b)
			if got := b.Digest(); (got == baseDigest) != tt.same {
				t.Errorf("Digest() = %s, the same as before: %v, want %v", got, got == baseDigest, tt.same)
			}
		})
	}
}

// base is a bundle with something in every field.
func base() *payload.Bundle {
	return &payload.Bundle{
		Root:    "team.main",
		Kinds:   []payload.File{{Name: "access.sigil", Source: "kind Access version 1\n"}},
		Paths:   []payload.File{{Name: "team/main.sigil", Source: "policy team.main: Access@1\n"}, {Name: "team/lib.sigil", Source: "module team.lib: Access@1\n"}},
		Trusted: []payload.File{{Name: "platform/base.sigil", Source: "policy platform.base: Access@1\n"}},
		Require: []payload.Requirement{{Policy: "platform.base", Trusted: []string{"platform"}, Roots: []string{"team.*"}}},
	}
}

// marker is the marker, built at runtime, so this test binary doesn't hold
// it a second time.
func marker() string {
	m := []byte("LDBLIGIS")
	slices.Reverse(m)
	return string(m)
}
