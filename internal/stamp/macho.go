package stamp

import (
	"bytes"
	"crypto/sha1" //nolint:gosec // CodeDirectories of hash type 1 are SHA-1 by definition; nothing here relies on it resisting collisions.
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
)

// The magic numbers of the Mach-O header and of the code signature's blobs.
// The header is read little-endian, the code signature big-endian.
const (
	machoMagic64 = 0xfeedfacf
	fatMagic     = 0xcafebabe
	fatCigam     = 0xbebafeca
	fatMagic64   = 0xcafebabf
	fatCigam64   = 0xbfbafeca

	lcCodeSignature = 0x1d

	csMagicEmbeddedSignature = 0xfade0cc0
	csMagicCodeDirectory     = 0xfade0c02
	csMagicBlobWrapper       = 0xfade0b01

	machoHeaderSize = 32
	superBlobHeader = 12
	blobIndexSize   = 8
	blobHeaderSize  = 8
	cdHeaderSize    = 44
	maxPageShift    = 30
)

// The CodeDirectory versions that added the header fields Patch reads or
// must not overwrite.
const (
	cdVersionScatter     = 0x20100
	cdVersionTeam        = 0x20200
	cdVersionCodeLimit64 = 0x20300
)

// machoSig is what Patch needs to know about a Mach-O: where its load
// commands end, its CodeDirectories, and whether it's signed with an
// identity. The zero value stands for a binary without a code signature.
type machoSig struct {
	cds       []codeDirectory
	headerEnd uint64
	signed    bool
}

// codeDirectory is one CodeDirectory of a code signature: a hash of every
// page of the file up to codeLimit, in slots that alias the binary, so
// writing a slot updates the binary.
type codeDirectory struct {
	sum       func([]byte) []byte
	name      string
	hashes    []byte
	pageSize  uint64
	codeLimit uint64
	hashSize  uint64
	start     uint64 // where it is in the SuperBlob
	end       uint64
}

// hashKind is a CodeDirectory hash type Patch can compute.
type hashKind struct {
	sum  func([]byte) []byte
	name string
	size uint64
}

// covers returns an error unless every CodeDirectory covers the area from
// off to end, and the area keeps clear of the Mach-O's load commands,
// which a payload must never overwrite.
func (s *machoSig) covers(off, end uint64) error {
	if off < s.headerEnd {
		return malformed("the reserved area at %#x overlaps the Mach-O load commands", off)
	}
	for i := range s.cds {
		if end > s.cds[i].codeLimit {
			return malformed("the reserved area at %#x reaches past the end of the signed code at %#x", off, s.cds[i].codeLimit)
		}
	}
	return nil
}

// rehash recomputes the hash of every page that the bytes from off to end
// touch.
func (cd *codeDirectory) rehash(exe []byte, off, end uint64) {
	for p := off / cd.pageSize; p <= (end-1)/cd.pageSize; p++ {
		copy(cd.hashes[p*cd.hashSize:], cd.page(exe, p))
	}
}

// verify returns an ErrMismatch for the first page that doesn't match its
// hash.
func (cd *codeDirectory) verify(exe []byte) error {
	for p := range uint64(len(cd.hashes)) / cd.hashSize {
		if !bytes.Equal(cd.hashes[p*cd.hashSize:(p+1)*cd.hashSize], cd.page(exe, p)) {
			return fmt.Errorf("%w: page %d at %#x doesn't match its %s hash", ErrMismatch, p, p*cd.pageSize, cd.name)
		}
	}
	return nil
}

// page returns the hash of page p, which ends early at codeLimit.
func (cd *codeDirectory) page(exe []byte, p uint64) []byte {
	return cd.sum(exe[p*cd.pageSize : min((p+1)*cd.pageSize, cd.codeLimit)])[:cd.hashSize]
}

// machoSignature walks a thin 64-bit Mach-O's load commands to its
// LC_CODE_SIGNATURE and reads the code signature that it points to.
func machoSignature(exe []byte) (machoSig, error) {
	if len(exe) < machoHeaderSize {
		return machoSig{}, malformed("the Mach-O header is cut short")
	}
	le := binary.LittleEndian
	ncmds := le.Uint32(exe[16:])
	end := machoHeaderSize + uint64(le.Uint32(exe[20:]))
	if end > uint64(len(exe)) {
		return machoSig{}, malformed("the Mach-O load commands run past the end of the file")
	}

	sig := machoSig{headerEnd: end}
	off := uint64(machoHeaderSize)
	for i := range ncmds {
		if end-off < 8 {
			return machoSig{}, malformed("load command %d runs past the load commands", i)
		}
		cmd, size := le.Uint32(exe[off:]), uint64(le.Uint32(exe[off+4:]))
		if size < 8 || size > end-off {
			return machoSig{}, malformed("load command %d has a size of %d bytes", i, size)
		}
		if cmd == lcCodeSignature {
			if size < 16 {
				return machoSig{}, malformed("LC_CODE_SIGNATURE has a size of %d bytes", size)
			}
			err := sig.read(exe, uint64(le.Uint32(exe[off+8:])), uint64(le.Uint32(exe[off+12:])))
			return sig, err
		}
		off += size
	}
	return sig, nil
}

// read reads the code signature, a SuperBlob of size bytes at off: its
// CodeDirectories, and whether its CMS blob holds a signature.
func (s *machoSig) read(exe []byte, off, size uint64) error {
	if off < s.headerEnd {
		return malformed("the code signature overlaps the Mach-O load commands")
	}
	if off > uint64(len(exe)) || size > uint64(len(exe))-off {
		return malformed("the code signature runs past the end of the file")
	}
	sb := exe[off : off+size]
	be := binary.BigEndian
	if len(sb) < superBlobHeader || be.Uint32(sb) != csMagicEmbeddedSignature {
		return malformed("the code signature isn't an embedded signature SuperBlob")
	}
	n := uint64(be.Uint32(sb[4:]))
	if n < superBlobHeader || n > size {
		return malformed("the code signature SuperBlob has a length of %d bytes", n)
	}
	sb = sb[:n]
	count := uint64(be.Uint32(sb[8:]))
	if count > (uint64(len(sb))-superBlobHeader)/blobIndexSize {
		return malformed("the code signature's index of %d blobs runs past its end", count)
	}

	for i := range count {
		start := uint64(be.Uint32(sb[superBlobHeader+i*blobIndexSize+4:]))
		if start > uint64(len(sb))-blobHeaderSize {
			return malformed("blob %d of the code signature starts past its end", i)
		}
		magic, length := be.Uint32(sb[start:]), uint64(be.Uint32(sb[start+4:]))
		if length < blobHeaderSize || length > uint64(len(sb))-start {
			return malformed("blob %d of the code signature has a length of %d bytes", i, length)
		}
		blob := sb[start : start+length]
		switch magic {
		case csMagicBlobWrapper:
			s.signed = s.signed || length > blobHeaderSize
		case csMagicCodeDirectory:
			if err := s.add(blob, start, off); err != nil {
				return err
			}
		}
	}
	return nil
}

// add reads the CodeDirectory blob at start in the SuperBlob of the code
// signature at sigOff and adds it. Another index entry for a
// CodeDirectory that's there already adds nothing.
func (s *machoSig) add(blob []byte, start, sigOff uint64) error {
	end := start + uint64(len(blob))
	for i := range s.cds {
		if c := &s.cds[i]; start < c.end && c.start < end {
			if c.start == start {
				return nil
			}
			return malformed("two CodeDirectories of the code signature overlap")
		}
	}
	cd, err := readCodeDirectory(blob, sigOff)
	if err != nil {
		return err
	}
	cd.start, cd.end = start, end
	s.cds = append(s.cds, cd)
	return nil
}

// readCodeDirectory reads a CodeDirectory blob of the code signature at
// sigOff. Its pages must end before the signature.
func readCodeDirectory(blob []byte, sigOff uint64) (codeDirectory, error) {
	if len(blob) < cdHeaderSize {
		return codeDirectory{}, malformed("a CodeDirectory is cut short")
	}
	be := binary.BigEndian
	version := be.Uint32(blob[8:])
	hashOffset := uint64(be.Uint32(blob[16:]))
	nCodeSlots := uint64(be.Uint32(blob[28:]))
	codeLimit := uint64(be.Uint32(blob[32:]))
	hashSize, hashType, pageShift := uint64(blob[36]), blob[37], blob[39]

	header := uint64(cdHeaderSize)
	switch {
	case version >= cdVersionCodeLimit64:
		header = 64
	case version >= cdVersionTeam:
		header = 52
	case version >= cdVersionScatter:
		header = 48
	}
	if uint64(len(blob)) < header {
		return codeDirectory{}, malformed("a CodeDirectory of version %#x is cut short", version)
	}
	if version >= cdVersionScatter && be.Uint32(blob[44:]) != 0 {
		return codeDirectory{}, unsupported("a CodeDirectory with scatter vectors")
	}
	if version >= cdVersionCodeLimit64 {
		if limit := be.Uint64(blob[56:]); limit != 0 {
			codeLimit = limit
		}
	}

	kind, ok := hashFor(hashType)
	switch {
	case !ok:
		return codeDirectory{}, unsupported("a CodeDirectory with hash type %d", hashType)
	case hashSize != kind.size:
		return codeDirectory{}, malformed("a %s CodeDirectory has hashes of %d bytes", kind.name, hashSize)
	case pageShift == 0 || pageShift > maxPageShift:
		return codeDirectory{}, unsupported("a CodeDirectory with a page size of 2^%d bytes", pageShift)
	case codeLimit > sigOff:
		return codeDirectory{}, malformed("a CodeDirectory covers the code signature itself")
	}
	pageSize := uint64(1) << pageShift
	switch {
	case nCodeSlots != (codeLimit+pageSize-1)/pageSize:
		return codeDirectory{}, malformed("a CodeDirectory has %d page hashes for %d bytes", nCodeSlots, codeLimit)
	case hashOffset < header:
		return codeDirectory{}, malformed("a CodeDirectory's page hashes overlap its header")
	case hashOffset > uint64(len(blob)) || nCodeSlots*hashSize > uint64(len(blob))-hashOffset:
		return codeDirectory{}, malformed("a CodeDirectory's page hashes run past its end")
	}
	return codeDirectory{
		hashes:    blob[hashOffset : hashOffset+nCodeSlots*hashSize],
		sum:       kind.sum,
		name:      kind.name,
		pageSize:  pageSize,
		codeLimit: codeLimit,
		hashSize:  hashSize,
	}, nil
}

// hashFor returns the hash of a CodeDirectory's hash type.
func hashFor(hashType uint8) (hashKind, bool) {
	switch hashType {
	case 1:
		return hashKind{name: "SHA-1", size: sha1.Size, sum: func(b []byte) []byte { s := sha1.Sum(b); return s[:] }}, true //nolint:gosec // see the import
	case 2:
		return hashKind{name: "SHA-256", size: sha256.Size, sum: sha256Sum}, true
	case 3:
		return hashKind{name: "truncated SHA-256", size: sha1.Size, sum: sha256Sum}, true
	case 4:
		return hashKind{name: "SHA-384", size: sha512.Size384, sum: func(b []byte) []byte { s := sha512.Sum384(b); return s[:] }}, true
	}
	return hashKind{}, false
}

// sha256Sum returns b's SHA-256; page truncates it for hash type 3.
func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}
