package stamp

import (
	"bytes"
	"encoding/binary"
)

// The parts of a PE's headers that peSigned reads.
const (
	peOffsetField  = 0x3c
	peCOFFSize     = 20
	pe32Magic      = 0x10b
	pe32PlusMagic  = 0x20b
	peSecurityDir  = 4
	peDirEntrySize = 8
)

// peSigned reports whether a PE has an Authenticode signature: whether
// its security data directory isn't empty. Windows checks that signature
// only when there is one, so an unsigned PE runs after a patch as before.
func peSigned(exe []byte) (bool, error) {
	le := binary.LittleEndian
	if len(exe) < peOffsetField+4 {
		return false, malformed("the DOS header is cut short")
	}
	pe := uint64(le.Uint32(exe[peOffsetField:]))
	if pe > uint64(len(exe)) || uint64(len(exe))-pe < 4+peCOFFSize+2 || !bytes.Equal(exe[pe:pe+4], []byte("PE\x00\x00")) {
		return false, malformed("the PE header is missing")
	}
	opt := pe + 4 + peCOFFSize
	optSize := uint64(le.Uint16(exe[pe+4+16:]))
	if optSize < 2 || optSize > uint64(len(exe))-opt {
		return false, malformed("the PE optional header has a size of %d bytes", optSize)
	}
	header := exe[opt : opt+optSize]

	var count, dirs uint64
	switch magic := le.Uint16(header); magic {
	case pe32Magic:
		count, dirs = 92, 96
	case pe32PlusMagic:
		count, dirs = 108, 112
	default:
		return false, malformed("the PE optional header has the unknown magic %#x", magic)
	}
	if uint64(len(header)) < dirs || uint64(le.Uint32(header[count:])) <= peSecurityDir {
		return false, nil
	}
	entry := dirs + peSecurityDir*peDirEntrySize
	if uint64(len(header)) < entry+peDirEntrySize {
		return false, malformed("the PE data directories run past the optional header")
	}
	return le.Uint32(header[entry+4:]) != 0, nil
}
