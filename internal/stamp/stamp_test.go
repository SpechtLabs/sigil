package stamp_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"

	"github.com/spechtlabs/sigil/internal/stamp"
)

// patchCase is a binary to patch and what Patch must do with it.
type patchCase struct {
	name   string
	build  func() ([]byte, machoLayout)
	mutate func(b []byte, l machoLayout) // breaks the binary before the patch
	marker []byte                        // marker() when nil
	data   []byte                        // newPayload when nil
	size   int                           // synthAreaSize when zero
	want   error                         // matched with errors.Is, or As for *TooLargeError
	msg    string                        // the whole error message, when set
}

var newPayload = []byte("the new payload, shorter than the old one")

func TestPatch(t *testing.T) {
	sha256CD := []cdSpec{{hashType: 2}}
	tooLarge := make([]byte, synthAreaSize-len(marker())+1)
	tests := []patchCase{
		// Formats Patch only writes to.
		{name: "ELF", build: fixed(buildELF)},
		{name: "PE32+ without a signature", build: pe(0x20b, 16, 0)},
		{name: "PE32 without a signature", build: pe(0x10b, 16, 0)},
		{name: "PE without a security directory", build: pe(0x20b, 4, 0)},
		{name: "PE whose optional header ends before the directories", build: pe(0x20b, 16, 0), mutate: func(b []byte, _ machoLayout) {
			binary.LittleEndian.PutUint16(b[0x80+4+16:], 100)
		}},
		{name: "Mach-O without a signature", build: macho(machoSpec{noSig: true})},

		// Mach-O signatures Patch re-hashes.
		{name: "SHA-256", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone})},
		{name: "SHA-1 and SHA-256", build: macho(machoSpec{cds: []cdSpec{{hashType: 1}, {hashType: 2}}, cms: synthCMSNone})},
		{name: "truncated SHA-256", build: macho(machoSpec{cds: []cdSpec{{hashType: 3}}, cms: synthCMSNone})},
		{name: "SHA-384", build: macho(machoSpec{cds: []cdSpec{{hashType: 4}}, cms: synthCMSNone})},
		{name: "16 KiB pages", build: macho(machoSpec{cds: []cdSpec{{hashType: 2, pageShift: 14}}, cms: synthCMSNone})},
		{name: "CodeDirectory version 0x20001", build: macho(machoSpec{cds: []cdSpec{{hashType: 2, version: 0x20001}}, cms: synthCMSNone})},
		{name: "CodeDirectory version 0x20100", build: macho(machoSpec{cds: []cdSpec{{hashType: 2, version: 0x20100}}, cms: synthCMSNone})},
		{name: "CodeDirectory version 0x20200", build: macho(machoSpec{cds: []cdSpec{{hashType: 2, version: 0x20200}}, cms: synthCMSNone})},
		{name: "codeLimit64", build: macho(machoSpec{cds: []cdSpec{{hashType: 2, version: 0x20300, limit64: true}}, cms: synthCMSNone})},
		{name: "empty CMS wrapper", build: macho(machoSpec{cds: sha256CD, cms: 0})},
		{name: "two index entries for one CodeDirectory", build: macho(machoSpec{cds: []cdSpec{{hashType: 2}, {hashType: 1}}, cms: synthCMSNone}), mutate: func(b []byte, l machoLayout) {
			put32(b, l.sigOff+16+8, uint32(l.cds[0]-l.sigOff))
		}},

		// The area.
		{name: "data that fills the area", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), data: bytes.Repeat([]byte{'x'}, synthAreaSize-len(marker()))},
		{name: "no data", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), data: []byte{}},
		{
			name: "no marker", build: fixed(buildELF), want: stamp.ErrNoArea,
			mutate: func(b []byte, _ machoLayout) { clear(b[synthAreaOff : synthAreaOff+len(marker())]) },
			msg:    "the binary has no reserved area: it wasn't built with one, or it was stripped",
		},
		{name: "a second marker after the area", build: fixed(buildELF), want: stamp.ErrManyAreas, mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff+synthAreaSize+10:], marker())
		}},
		{name: "a second marker inside the area", build: fixed(buildELF), want: stamp.ErrManyAreas, mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff+100:], marker())
		}},
		{name: "a stray marker less than an area before the area", build: fixed(buildELF), want: stamp.ErrManyAreas, mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff-1000:], marker())
		}},
		{
			name: "data holding the marker", build: fixed(buildELF), data: append([]byte("x"), marker()...), want: stamp.ErrMarkerInData,
			msg: "the payload contains the reserved area's marker",
		},
		{name: "data that overlaps the marker", build: fixed(buildELF), marker: []byte("ABAB"), data: []byte("AB"), want: stamp.ErrMarkerInData, mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff:], "ABAB")
		}},
		{name: "data that meets the zeros after it", build: fixed(buildELF), marker: []byte("AB\x00"), data: []byte("xAB"), want: stamp.ErrMarkerInData, mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff:], "AB\x00")
		}},
		{name: "data that meets the bytes after the area", build: fixed(buildELF), marker: []byte("AB"), data: append(bytes.Repeat([]byte{'x'}, synthAreaSize-3), 'A'), want: stamp.ErrMarkerInData, mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff:], "AB")
			b[synthAreaOff+synthAreaSize] = 'B'
		}},
		{name: "data that ends where the area does", build: fixed(buildELF), marker: []byte("AB"), data: append(bytes.Repeat([]byte{'x'}, synthAreaSize-3), 'A'), mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff:], "AB")
		}},
		{name: "an area past the end of the file", build: fixed(buildELF), size: synthLimit, want: stamp.ErrMalformed},
		{
			name: "data that doesn't fit", build: fixed(buildELF), data: tooLarge,
			want: &stamp.TooLargeError{Size: len(tooLarge), Max: synthAreaSize - len(marker())},
			msg:  "the payload is 5989 bytes, 1 more than the reserved area holds (5988 bytes)",
		},
		{name: "an empty marker", build: fixed(buildELF), marker: []byte{}, msg: "stamp: a 6000-byte area can't start with a 0-byte marker"},
		{name: "an area smaller than its marker", build: fixed(buildELF), size: 4, msg: "stamp: a 4-byte area can't start with a 12-byte marker"},
		{name: "an area overlapping the load commands", build: macho(machoSpec{noSig: true}), want: stamp.ErrMalformed, mutate: func(b []byte, _ machoLayout) {
			binary.LittleEndian.PutUint32(b[20:], synthAreaOff+100)
		}},
		{name: "an area past the signed code", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone, areaOff: synthLimit - 100}), size: 200, data: []byte{}, want: stamp.ErrMalformed},

		// Formats Patch refuses.
		{
			name: "fat", build: bytesOf("\xca\xfe\xba\xbe\x00\x00\x00\x02"), want: stamp.ErrUnsupported,
			msg: "unsupported binary: a universal Mach-O holds several architectures; patch a thin one, as `lipo -thin` extracts it",
		},
		{name: "fat, read the other way", build: bytesOf("\xbe\xba\xfe\xca\x00\x00\x00\x02"), want: stamp.ErrUnsupported},
		{name: "fat64", build: bytesOf("\xca\xfe\xba\xbf\x00\x00\x00\x02"), want: stamp.ErrUnsupported},
		{name: "fat64, read the other way", build: bytesOf("\xbf\xba\xfe\xca\x00\x00\x00\x02"), want: stamp.ErrUnsupported},
		{
			name: "32-bit Mach-O", build: bytesOf("\xce\xfa\xed\xfe\x00\x00\x00\x00"), want: stamp.ErrUnsupported,
			msg: "unsupported binary: unknown format (magic 0xfeedface); patch an ELF, a 64-bit little-endian Mach-O or a PE",
		},
		{name: "a script", build: bytesOf("#!/bin/sh\necho hi\n"), want: stamp.ErrUnsupported},
		{name: "too short", build: bytesOf("ab"), want: stamp.ErrUnsupported},
		{name: "empty", build: bytesOf(""), want: stamp.ErrUnsupported},

		// Signatures Patch can't redo.
		{
			name: "Authenticode", build: pe(0x20b, 16, 0x1000), want: stamp.ErrSigned,
			msg: "the binary is signed with an identity, and only an ad-hoc signature can be updated after a change",
		},
		{name: "Authenticode in a PE32", build: pe(0x10b, 16, 0x1000), want: stamp.ErrSigned},
		{name: "CMS signature", build: macho(machoSpec{cds: sha256CD, cms: 100}), want: stamp.ErrSigned},
		{
			name: "unknown hash type", build: macho(machoSpec{cds: []cdSpec{{hashType: 9}}, cms: synthCMSNone}), want: stamp.ErrUnsupported,
			msg: "unsupported binary: a CodeDirectory with hash type 9",
		},
		{name: "scatter vectors", build: macho(machoSpec{cds: []cdSpec{{hashType: 2, version: 0x20100}}, cms: synthCMSNone}), want: stamp.ErrUnsupported, mutate: cd0(44, 1)},
		{name: "no paging", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrUnsupported, mutate: cd0byte(39, 0)},
		{name: "huge pages", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrUnsupported, mutate: cd0byte(39, 31)},

		// Damaged PEs.
		{name: "a cut-short DOS header", build: bytesOf("MZ\x90\x00"), want: stamp.ErrMalformed},
		{name: "a PE header past the end", build: pe(0x20b, 16, 0), want: stamp.ErrMalformed, mutate: func(b []byte, _ machoLayout) {
			binary.LittleEndian.PutUint32(b[0x3c:], 0xffffff00)
		}},
		{name: "no PE signature", build: pe(0x20b, 16, 0), want: stamp.ErrMalformed, mutate: func(b []byte, _ machoLayout) {
			copy(b[0x80:], "NE")
		}},
		{name: "an empty optional header", build: pe(0x20b, 16, 0), want: stamp.ErrMalformed, mutate: func(b []byte, _ machoLayout) {
			binary.LittleEndian.PutUint16(b[0x80+4+16:], 0)
		}},
		{name: "an optional header past the end", build: pe(0x20b, 16, 0), want: stamp.ErrMalformed, mutate: func(b []byte, _ machoLayout) {
			binary.LittleEndian.PutUint16(b[0x80+4+16:], 0xffff)
		}},
		{
			name: "an unknown optional header", build: pe(0x20b, 16, 0), want: stamp.ErrMalformed,
			mutate: func(b []byte, _ machoLayout) { binary.LittleEndian.PutUint16(b[0x80+4+20:], 0x107) },
			msg:    "the binary is damaged: the PE optional header has the unknown magic 0x107",
		},
		{name: "directories past the optional header", build: pe(0x20b, 16, 0), want: stamp.ErrMalformed, mutate: func(b []byte, _ machoLayout) {
			binary.LittleEndian.PutUint16(b[0x80+4+16:], 112+4*8)
		}},

		// Damaged Mach-O headers.
		{name: "a cut-short Mach-O header", build: bytesOf("\xcf\xfa\xed\xfe\x0c\x00\x00\x01"), want: stamp.ErrMalformed},
		{name: "load commands past the end", build: macho(machoSpec{noSig: true}), want: stamp.ErrMalformed, mutate: le32(20, 0xffffff00)},
		{name: "a load command past the load commands", build: macho(machoSpec{noSig: true}), want: stamp.ErrMalformed, mutate: le32(16, 2)},
		{name: "a load command of 4 bytes", build: macho(machoSpec{noSig: true}), want: stamp.ErrMalformed, mutate: le32(36, 4)},
		{name: "a load command past its end", build: macho(machoSpec{noSig: true}), want: stamp.ErrMalformed, mutate: le32(36, 1000)},
		{name: "a short LC_CODE_SIGNATURE", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: le32(52, 8)},
		{name: "a signature past the end", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: func(b []byte, l machoLayout) {
			binary.LittleEndian.PutUint32(b[60:], uint32(l.sigLen+1))
		}},
		{name: "a signature in the load commands", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: le32(56, 32)},

		// Damaged code signatures.
		{name: "no SuperBlob", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: sig(0, 0)},
		{name: "a SuperBlob of 4 bytes", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: sig(4, 4)},
		{name: "a SuperBlob past the signature", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: func(b []byte, l machoLayout) {
			put32(b, l.sigOff+4, uint32(l.sigLen+1))
		}},
		{name: "an index past the SuperBlob", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: sig(8, 1000)},
		{name: "a blob past the SuperBlob", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: func(b []byte, l machoLayout) {
			put32(b, l.sigOff+16, uint32(l.sigLen))
		}},
		{name: "a blob of 4 bytes", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: cd0(4, 4)},
		{name: "a blob past its SuperBlob", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: func(b []byte, l machoLayout) {
			put32(b, l.cds[0]+4, uint32(l.sigLen))
		}},
		{name: "overlapping CodeDirectories", build: macho(machoSpec{cds: []cdSpec{{hashType: 2}, {hashType: 1}}, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: func(b []byte, l machoLayout) {
			put32(b, l.cds[0]+88, 0xfade0c02)
			put32(b, l.cds[0]+92, 100)
			put32(b, l.sigOff+16+8, uint32(l.cds[0]+88-l.sigOff))
		}},
		{name: "a cut-short CodeDirectory", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: cd0(4, 40)},
		{name: "a cut-short CodeDirectory of version 0x20300", build: macho(machoSpec{cds: []cdSpec{{hashType: 2, version: 0x20300}}, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: cd0(4, 60)},
		{name: "a hash size that doesn't fit the hash type", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: cd0byte(36, 20)},
		{name: "a code limit in the signature", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: func(b []byte, l machoLayout) {
			put32(b, l.cds[0]+32, uint32(l.sigOff+1))
		}},
		{
			name: "too few page hashes", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: cd0(28, 5),
			msg: "the binary is damaged: a CodeDirectory has 5 page hashes for 20580 bytes",
		},
		{name: "page hashes in the header", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: cd0(16, 40)},
		{name: "page hashes past the CodeDirectory", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMalformed, mutate: cd0(16, 0xffff0000)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, l := tt.build()
			if tt.mutate != nil {
				tt.mutate(b, l)
			}
			before := bytes.Clone(b)
			m := tt.marker
			if m == nil {
				m = marker()
			}
			data := tt.data
			if data == nil {
				data = newPayload
			}
			size := tt.size
			if size == 0 {
				size = synthAreaSize
			}

			err := stamp.Patch(b, m, size, data)
			if tt.msg != "" && (err == nil || err.Error() != tt.msg) {
				t.Errorf("Patch() = %v, want the message %q", err, tt.msg)
			}
			if tt.want == nil && tt.msg == "" {
				checkPatched(t, before, b, l, m, data, err)
				return
			}
			if tt.want != nil && !matches(err, tt.want) {
				t.Errorf("Patch() = %v, want %v", err, tt.want)
			}
			if !bytes.Equal(b, before) {
				t.Error("Patch() changed the binary although it failed")
			}
		})
	}
}

func TestPatchTwice(t *testing.T) {
	b, _ := buildMachO(machoSpec{cds: []cdSpec{{hashType: 1}, {hashType: 2, pageShift: 14}}, cms: 0})
	for _, data := range [][]byte{bytes.Repeat([]byte{'a'}, 5000), marker()[:8], []byte("short")} {
		if err := stamp.Patch(b, marker(), synthAreaSize, data); err != nil {
			t.Fatalf("Patch(%.10q) = %v", data, err)
		}
		if err := stamp.Verify(b); err != nil {
			t.Fatalf("Verify() after Patch(%.10q) = %v", data, err)
		}
	}
	want := append(append(marker(), "short"...), make([]byte, synthAreaSize-len(marker())-5)...)
	if !bytes.Equal(b[synthAreaOff:synthAreaOff+synthAreaSize], want) {
		t.Error("the area doesn't hold the last payload followed by zeros")
	}
}

func TestVerify(t *testing.T) {
	sha256CD := []cdSpec{{hashType: 2}}
	tests := []struct {
		name   string
		build  func() ([]byte, machoLayout)
		mutate func(b []byte, l machoLayout)
		want   error
		msg    string
	}{
		{name: "SHA-256", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone})},
		{name: "CMS signature", build: macho(machoSpec{cds: sha256CD, cms: 100})},
		{name: "Mach-O without a signature", build: macho(machoSpec{noSig: true})},
		{name: "ELF", build: fixed(buildELF)},
		{name: "PE with Authenticode", build: pe(0x20b, 16, 0x1000)},
		{name: "fat", build: bytesOf("\xca\xfe\xba\xbe\x00\x00\x00\x02"), want: stamp.ErrUnsupported},
		{name: "damaged signature", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), mutate: sig(0, 0), want: stamp.ErrMalformed},
		{
			name: "a changed page", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMismatch,
			mutate: func(b []byte, _ machoLayout) { b[4*synthPage+5] ^= 1 },
			msg:    "the code signature doesn't match the binary: page 4 at 0x4000 doesn't match its SHA-256 hash",
		},
		{name: "a changed short last page", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMismatch, mutate: func(b []byte, _ machoLayout) {
			b[synthLimit-1] ^= 1
		}},
		{
			name: "a wrong hash in the alternate CodeDirectory", build: macho(machoSpec{cds: []cdSpec{{hashType: 2}, {hashType: 1}}, cms: synthCMSNone}), want: stamp.ErrMismatch,
			mutate: func(b []byte, l machoLayout) { b[l.cds[1]+int(binary.BigEndian.Uint32(b[l.cds[1]+16:]))+20] ^= 1 },
			msg:    "the code signature doesn't match the binary: page 1 at 0x1000 doesn't match its SHA-1 hash",
		},
		{name: "a patch without re-hashing", build: macho(machoSpec{cds: sha256CD, cms: synthCMSNone}), want: stamp.ErrMismatch, mutate: func(b []byte, _ machoLayout) {
			copy(b[synthAreaOff+len(marker()):], newPayload)
		}},
		{name: "a changed identifier", build: macho(machoSpec{cds: sha256CD, cms: 0}), mutate: func(b []byte, l machoLayout) {
			b[l.cds[0]+88] ^= 1
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b, l := tt.build()
			if tt.mutate != nil {
				tt.mutate(b, l)
			}
			err := stamp.Verify(b)
			if !matches(err, tt.want) {
				t.Errorf("Verify() = %v, want %v", err, tt.want)
			}
			if tt.msg != "" && (err == nil || err.Error() != tt.msg) {
				t.Errorf("Verify() = %v, want the message %q", err, tt.msg)
			}
		})
	}
}

// checkPatched checks a successful Patch: the area holds the marker, the
// data and zeros, every other byte before the signature is as it was, and
// the signature matches.
func checkPatched(t *testing.T, before, after []byte, l machoLayout, m, data []byte, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("Patch() = %v", err)
	}
	if err := stamp.Verify(before); err != nil {
		t.Fatalf("Verify() before the patch = %v", err)
	}
	if err := stamp.Verify(after); err != nil {
		t.Errorf("Verify() after the patch = %v", err)
	}
	off := bytes.Index(before, m)
	want := append(append(bytes.Clone(m), data...), make([]byte, synthAreaSize-len(m)-len(data))...)
	if got := after[off : off+synthAreaSize]; !bytes.Equal(got, want) {
		t.Errorf("the area holds %.40q…, want %.40q…", got, want)
	}
	end := len(after)
	if l.sigOff > 0 {
		end = l.sigOff
	}
	if !bytes.Equal(after[:off], before[:off]) || !bytes.Equal(after[off+synthAreaSize:end], before[off+synthAreaSize:end]) {
		t.Error("Patch() changed bytes outside the area and the signature")
	}
}

// matches reports whether err is want: errors.Is, or for a *TooLargeError,
// errors.As with the same fields.
func matches(err, want error) bool {
	if tl, ok := errors.AsType[*stamp.TooLargeError](want); ok {
		got, ok := errors.AsType[*stamp.TooLargeError](err)
		return ok && *got == *tl
	}
	return errors.Is(err, want)
}

func fixed(build func() []byte) func() ([]byte, machoLayout) {
	return func() ([]byte, machoLayout) { return build(), machoLayout{} }
}

func bytesOf(s string) func() ([]byte, machoLayout) {
	return func() ([]byte, machoLayout) { return []byte(s), machoLayout{} }
}

func pe(magic uint16, count, security uint32) func() ([]byte, machoLayout) {
	return fixed(func() []byte { return buildPE(magic, count, security) })
}

func macho(s machoSpec) func() ([]byte, machoLayout) {
	return func() ([]byte, machoLayout) { return buildMachO(s) }
}

// le32 sets the little-endian uint32 at off in the Mach-O header.
func le32(off int, v uint32) func([]byte, machoLayout) {
	return func(b []byte, _ machoLayout) { binary.LittleEndian.PutUint32(b[off:], v) }
}

// sig sets the big-endian uint32 at off in the SuperBlob.
func sig(off int, v uint32) func([]byte, machoLayout) {
	return func(b []byte, l machoLayout) { put32(b, l.sigOff+off, v) }
}

// cd0 sets the big-endian uint32 at off in the first CodeDirectory.
func cd0(off int, v uint32) func([]byte, machoLayout) {
	return func(b []byte, l machoLayout) { put32(b, l.cds[0]+off, v) }
}

// cd0byte sets the byte at off in the first CodeDirectory.
func cd0byte(off int, v byte) func([]byte, machoLayout) {
	return func(b []byte, l machoLayout) { b[l.cds[0]+off] = v }
}

func put32(b []byte, off int, v uint32) {
	binary.BigEndian.PutUint32(b[off:], v)
}
