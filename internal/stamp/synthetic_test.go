package stamp_test

import (
	"bytes"
	"cmp"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"slices"
)

// The layout of the synthetic binaries: five full pages of code and a short
// one, with a reserved area that spans the first three pages and holds an
// old payload.
const (
	synthPage     = 1 << 12
	synthLimit    = 5*synthPage + 100
	synthAreaOff  = 3000
	synthAreaSize = 6000
	synthCmdsEnd  = 32 + 16 + 16
	synthCMSNone  = -1
)

// machoSpec describes a synthetic thin 64-bit Mach-O.
type machoSpec struct {
	cds     []cdSpec
	areaOff int // where the marker goes; synthAreaOff when zero
	cms     int // bytes in the CMS blob wrapper, or synthCMSNone for none
	noSig   bool
}

// cdSpec describes one CodeDirectory of a synthetic Mach-O.
type cdSpec struct {
	version   uint32 // 0x20400 when zero
	hashType  uint8
	pageShift uint8 // 12 when zero
	limit64   bool  // record the code limit in codeLimit64 only
}

// machoLayout says where a synthetic Mach-O's parts are, for the tests
// that break them.
type machoLayout struct {
	cds    []int // offset of each CodeDirectory
	sigOff int
	sigLen int
}

// marker returns the test marker. It's spelled backwards here, so the test
// binary doesn't hold it.
func marker() []byte {
	m := []byte("REKRAM-PMATS")
	slices.Reverse(m)
	return m
}

// oldPayload is what the synthetic area holds before a test patches it.
func oldPayload() []byte {
	return bytes.Repeat([]byte{0xaa}, synthAreaSize-len(marker()))
}

// code returns n bytes of code: a pattern that never contains the marker,
// with the area at areaOff holding the marker and the old payload.
func code(n, areaOff int) []byte {
	b := make([]byte, n)
	x := uint32(1)
	for i := range b {
		x = x*1664525 + 1013904223
		b[i] = byte(x>>24) | 0x80
	}
	if areaOff >= 0 {
		copy(b[copy(b[areaOff:], marker())+areaOff:], oldPayload()[:min(len(oldPayload()), n-areaOff-len(marker()))])
	}
	return b
}

// buildELF returns a synthetic ELF: the magic, then code with the area.
func buildELF() []byte {
	b := code(synthLimit, synthAreaOff)
	copy(b, "\x7fELF")
	return b
}

// buildPE returns a synthetic PE with an optional header of the given magic
// (0x10b or 0x20b), count data directories, and security directory size.
func buildPE(magic uint16, count, security uint32) []byte {
	b := code(synthLimit, synthAreaOff)
	le := binary.LittleEndian
	copy(b, "MZ")
	const pe = 0x80
	le.PutUint32(b[0x3c:], pe)
	copy(b[pe:], "PE\x00\x00")
	opt := pe + 4 + 20
	dirs := 112
	if magic == 0x10b {
		dirs = 96
	}
	le.PutUint16(b[pe+4+16:], uint16(dirs+int(count)*8))
	le.PutUint16(b[opt:], magic)
	le.PutUint32(b[opt+dirs-4:], count)
	for i := range int(count) {
		le.PutUint32(b[opt+dirs+i*8:], 0)
		le.PutUint32(b[opt+dirs+i*8+4:], 0)
	}
	if count > 4 {
		le.PutUint32(b[opt+dirs+4*8+4:], security)
	}
	return b
}

// buildMachO returns a synthetic Mach-O as s describes it, with a valid
// code signature over its code, and where its parts are.
func buildMachO(s machoSpec) ([]byte, machoLayout) {
	areaOff := s.areaOff
	if areaOff == 0 {
		areaOff = synthAreaOff
	}
	b := code(synthLimit, areaOff)
	le := binary.LittleEndian
	le.PutUint32(b[0:], 0xfeedfacf)
	le.PutUint32(b[4:], 0x0100000c) // arm64
	le.PutUint32(b[8:], 0)
	le.PutUint32(b[12:], 2) // MH_EXECUTE
	le.PutUint32(b[24:], 0)
	le.PutUint32(b[28:], 0)
	// LC_SOURCE_VERSION, a command that stands for the ones Patch skips.
	le.PutUint32(b[32:], 0x2a)
	le.PutUint32(b[36:], 16)
	clear(b[40:48])
	if s.noSig {
		le.PutUint32(b[16:], 1)
		le.PutUint32(b[20:], 16)
		return b, machoLayout{}
	}
	le.PutUint32(b[16:], 2)
	le.PutUint32(b[20:], 32)

	blobs := make([][]byte, 0, len(s.cds)+1)
	for _, cd := range s.cds {
		blobs = append(blobs, codeDirectory(cd))
	}
	if s.cms != synthCMSNone {
		w := make([]byte, 8+s.cms)
		binary.BigEndian.PutUint32(w, 0xfade0b01)
		binary.BigEndian.PutUint32(w[4:], uint32(len(w)))
		blobs = append(blobs, w)
	}
	sig, offs := superBlob(blobs)
	l := machoLayout{sigOff: len(b), sigLen: len(sig)}
	for _, off := range offs[:len(s.cds)] {
		l.cds = append(l.cds, l.sigOff+off)
	}
	le.PutUint32(b[48:], 0x1d)
	le.PutUint32(b[52:], 16)
	le.PutUint32(b[56:], uint32(l.sigOff))
	le.PutUint32(b[60:], uint32(l.sigLen))
	// The signature covers the load commands, so it's computed last.
	b = append(b, sig...)
	for i, cd := range s.cds {
		resign(b, l.cds[i], cd)
	}
	return b, l
}

// codeDirectory returns a CodeDirectory as cd describes it, for code of
// synthLimit bytes, with zero page hashes that resign fills in.
func codeDirectory(cd cdSpec) []byte {
	version, shift := cmp.Or(cd.version, 0x20400), cmp.Or(cd.pageShift, 12)
	header := 44
	switch {
	case version >= 0x20400:
		header = 88
	case version >= 0x20300:
		header = 64
	case version >= 0x20200:
		header = 52
	case version >= 0x20100:
		header = 48
	}
	ident := []byte("synthetic\x00")
	size := len(hash(cd.hashType, nil))
	page := 1 << shift
	nCode := (synthLimit + page - 1) / page
	hashOffset := header + len(ident) + 2*size
	b := make([]byte, hashOffset+nCode*size)
	be := binary.BigEndian
	be.PutUint32(b[0:], 0xfade0c02)
	be.PutUint32(b[4:], uint32(len(b)))
	be.PutUint32(b[8:], version)
	be.PutUint32(b[12:], 0x2) // adhoc
	be.PutUint32(b[16:], uint32(hashOffset))
	be.PutUint32(b[20:], uint32(header))
	be.PutUint32(b[24:], 2)
	be.PutUint32(b[28:], uint32(nCode))
	be.PutUint32(b[32:], synthLimit)
	b[36], b[37], b[39] = byte(size), cd.hashType, shift
	if cd.limit64 {
		be.PutUint32(b[32:], 1)
		be.PutUint64(b[56:], synthLimit)
	}
	copy(b[header:], ident)
	return b
}

// superBlob returns an embedded signature SuperBlob of blobs, and each
// blob's offset in it.
func superBlob(blobs [][]byte) ([]byte, []int) {
	be := binary.BigEndian
	b := make([]byte, 12+8*len(blobs))
	offs := make([]int, len(blobs))
	for i, blob := range blobs {
		slot := uint32(0x1000 + i)
		if i == 0 {
			slot = 0
		}
		if be.Uint32(blob) == 0xfade0b01 {
			slot = 0x10000
		}
		offs[i] = len(b)
		be.PutUint32(b[12+8*i:], slot)
		be.PutUint32(b[16+8*i:], uint32(len(b)))
		b = append(b, blob...)
	}
	be.PutUint32(b[0:], 0xfade0cc0)
	be.PutUint32(b[4:], uint32(len(b)))
	be.PutUint32(b[8:], uint32(len(blobs)))
	return b, offs
}

// resign recomputes every page hash of the CodeDirectory at off in b.
func resign(b []byte, off int, cd cdSpec) {
	be := binary.BigEndian
	hashOffset, nCode := int(be.Uint32(b[off+16:])), int(be.Uint32(b[off+28:]))
	size, page := int(b[off+36]), 1<<cmp.Or(cd.pageShift, 12)
	for p := range nCode {
		h := hash(cd.hashType, b[p*page:min((p+1)*page, synthLimit)])
		copy(b[off+hashOffset+p*size:], h)
	}
}

// hash returns the page hash of the given hash type, or 32 zero bytes for a
// type that doesn't exist.
func hash(hashType uint8, b []byte) []byte {
	switch hashType {
	case 1:
		s := sha1.Sum(b)
		return s[:]
	case 3:
		s := sha256.Sum256(b)
		return s[:20]
	case 4:
		s := sha512.Sum384(b)
		return s[:]
	case 2:
		s := sha256.Sum256(b)
		return s[:]
	}
	return make([]byte, 32)
}
