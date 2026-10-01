// Package payload is what `sigil compile` builds into a binary: a bundle
// of policy files, with the version of the sigil that compiled it and
// when.
//
// Every binary that links this package carries a reserved area of
// [AreaSize] bytes in its data, initialized at link time. The area starts
// with [Marker], which occurs nowhere else in the binary, so package stamp
// can find it in the file and write an encoded payload right after it
// without moving anything. Nothing written, the rest of the area is zero,
// and [Embedded] returns nil.
//
// What follows the marker is what [Encode] returns:
//
//	offset  size  field
//	0       4     format version, uint32 little-endian, from 1; 0 is no payload
//	4       4     length of the body, uint32 little-endian
//	8       32    SHA-256 of the body
//	40      n     the body: the [Payload] as JSON, compressed with compress/flate
//
// The zero bytes after the body are the rest of the area. [Decode] reads
// this back and tells a damaged area or a newer format apart from no
// payload at all.
package payload

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"time"

	"github.com/sierrasoftworks/humane-errors-go"
)

// AreaSize is the size of the reserved area, the marker included.
const AreaSize = 1 << 20

// markerSize is the length of the marker at the start of the area.
const markerSize = 8

// area is the reserved area: the marker, then the encoded payload, zero
// when nothing is compiled in. It has to be initialized with non-zero
// bytes, which places it in the binary's data instead of BSS, so that
// stamp can find it in the file. The marker is spelled out byte by byte
// here and nowhere else, so the binary holds it exactly once.
var area = [AreaSize]byte{'S', 'I', 'G', 'I', 'L', 'B', 'D', 'L'}

// File is one file of a compiled bundle.
type File struct {
	Name   string `json:"name"`   // as diagnostics name it
	Source string `json:"source"` // the file's contents
}

// Requirement is one policy that compile enforced, as `sigil check`
// does: it must be invoked by the roots, read from the trusted paths.
type Requirement struct {
	Policy  string   `json:"policy"`
	Trusted []string `json:"trusted,omitempty"` // as given, for display; the files are in Bundle.Trusted
	Roots   []string `json:"roots,omitempty"`   // name patterns of the policies it applies to
}

// Bundle is what a compiled binary evaluates.
type Bundle struct {
	Root    string        `json:"root,omitempty"`    // the default policy for eval and explain; empty when the bundle holds several and none was named
	Kinds   []File        `json:"kinds,omitempty"`   // kind files loaded as with --kind
	Paths   []File        `json:"paths"`             // the documents
	Trusted []File        `json:"trusted,omitempty"` // read as trusted, as policy.From does
	Require []Requirement `json:"require,omitempty"` // what compile enforced, recorded for `version`
}

// Payload is everything a compiled binary carries.
type Payload struct {
	Built  time.Time `json:"built,omitzero"` // when it was compiled; zero leaves it out
	Sigil  string    `json:"sigil"`          // the version of the sigil that compiled it
	Bundle Bundle    `json:"bundle"`
}

// digester hashes the canonical encoding of a bundle.
type digester struct {
	h   hash.Hash
	buf [binary.MaxVarintLen64]byte
}

// Marker returns the bytes the reserved area starts with, "SIGILBDL",
// which package stamp looks for in a binary. It's read from the area
// itself, which keeps the linker from dropping the area, and it's a
// copy, so the caller can't change the area through it.
func Marker() []byte {
	return bytes.Clone(area[:markerSize])
}

// Embedded returns the payload compiled into this binary: [Decode] of the
// area after the marker. It's nil, without an error, when nothing was
// compiled in.
func Embedded() (*Payload, humane.Error) {
	return Decode(area[markerSize:])
}

// Digest identifies the bundle's content: "sha256:" and the hex SHA-256
// of a canonical encoding of Root, Kinds, Paths, Trusted and Require, in
// that order. The same files compile to the same digest, whatever sigil
// version compiled them or when, and changing any byte of a file's name
// or contents, the root, or a requirement changes it.
//
// The encoding is a sequence of strings and lists. A string is its length
// in bytes as an unsigned varint (encoding/binary's), then its bytes. A
// list is its number of items as an unsigned varint, then each item, so a
// nil and an empty list encode alike. A file is its name, then its
// source. A requirement is its policy, then its trusted list of strings,
// then its roots list of strings. The bundle is its root, then the lists
// of kind files, of paths, of trusted files and of requirements. Every
// boundary is spelled out, so no two bundles encode alike.
func (b *Bundle) Digest() string {
	d := &digester{h: sha256.New()}
	d.string(b.Root)
	d.files(b.Kinds)
	d.files(b.Paths)
	d.files(b.Trusted)
	d.count(len(b.Require))
	for _, r := range b.Require {
		d.string(r.Policy)
		d.strings(r.Trusted)
		d.strings(r.Roots)
	}
	return "sha256:" + hex.EncodeToString(d.h.Sum(nil))
}

// files writes a list of files.
func (d *digester) files(fs []File) {
	d.count(len(fs))
	for _, f := range fs {
		d.string(f.Name)
		d.string(f.Source)
	}
}

// strings writes a list of strings.
func (d *digester) strings(ss []string) {
	d.count(len(ss))
	for _, s := range ss {
		d.string(s)
	}
}

// string writes one string, its length first.
func (d *digester) string(s string) {
	d.count(len(s))
	_, _ = d.h.Write([]byte(s)) // a hash.Hash never fails to write
}

// count writes a length or a number of items, as an unsigned varint.
func (d *digester) count(n int) {
	_, _ = d.h.Write(binary.AppendUvarint(d.buf[:0], uint64(n))) //nolint:gosec // a length is never negative
}
