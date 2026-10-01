package stamp

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// format is a binary format, as its magic number tells.
type format int

const (
	formatUnknown format = iota
	formatELF
	formatMachO
	formatPE
)

// Patch writes data into exe's reserved area, the one region of size bytes
// that starts with marker, right after the marker, and zero-fills the rest
// of the area, so a previous payload never shows through. exe is modified
// in place. A Mach-O's ad-hoc code signature is updated to match.
//
// Patch checks everything before it writes, so exe is unchanged when it
// returns an error. The error is ErrNoArea, ErrManyAreas, ErrMarkerInData,
// ErrSigned, ErrUnsupported or ErrMalformed (match them with errors.Is), or a
// *TooLargeError. An empty marker or one longer than size is a bug in the
// caller, and the error says so.
func Patch(exe, marker []byte, size int, data []byte) error {
	if len(marker) == 0 || size < len(marker) {
		return fmt.Errorf("stamp: a %d-byte area can't start with a %d-byte marker", size, len(marker))
	}

	var sig machoSig
	switch f, err := detect(exe); {
	case err != nil:
		return err
	case f == formatMachO:
		if sig, err = machoSignature(exe); err != nil {
			return err
		}
		if sig.signed {
			return ErrSigned
		}
	case f == formatPE:
		signed, err := peSigned(exe)
		if err != nil {
			return err
		}
		if signed {
			return ErrSigned
		}
	}

	start, end, err := findArea(exe, marker, size)
	if err != nil {
		return err
	}
	limit := size - len(marker)
	if len(data) > limit {
		return &TooLargeError{Size: len(data), Max: limit}
	}
	if addsMarker(exe, end, marker, data, limit) {
		return ErrMarkerInData
	}
	if err := sig.covers(start, end); err != nil {
		return err
	}

	area := exe[start+uint64(len(marker)) : end]
	clear(area[copy(area, data):])
	for i := range sig.cds {
		sig.cds[i].rehash(exe, start, end)
	}
	return nil
}

// Verify reports whether exe's code signature, if it has one that Patch
// maintains, matches its contents: every page hash of every CodeDirectory.
// It's nil for formats without one (ELF, PE) and for an unsigned Mach-O.
// A page that doesn't match is an ErrMismatch; a format or signature it
// can't read is an ErrUnsupported or ErrMalformed, as for Patch.
func Verify(exe []byte) error {
	f, err := detect(exe)
	if err != nil || f != formatMachO {
		return err
	}
	sig, err := machoSignature(exe)
	if err != nil {
		return err
	}
	for i := range sig.cds {
		if err := sig.cds[i].verify(exe); err != nil {
			return err
		}
	}
	return nil
}

// detect tells exe's format from its magic number. A universal Mach-O and
// anything it doesn't know are an ErrUnsupported.
func detect(exe []byte) (format, error) {
	switch {
	case bytes.HasPrefix(exe, []byte("\x7fELF")):
		return formatELF, nil
	case bytes.HasPrefix(exe, []byte("MZ")):
		return formatPE, nil
	case len(exe) < 4:
		return formatUnknown, unsupported("the file is too short to be an executable")
	}
	switch magic := binary.LittleEndian.Uint32(exe); magic {
	case machoMagic64:
		return formatMachO, nil
	case fatMagic, fatCigam, fatMagic64, fatCigam64:
		return formatUnknown, unsupported("a universal Mach-O holds several architectures; patch a thin one, as `lipo -thin` extracts it")
	default:
		return formatUnknown, unsupported("unknown format (magic %#08x); patch an ELF, a 64-bit little-endian Mach-O or a PE", magic)
	}
}

// findArea returns where the area is in exe: from the marker to size bytes
// later. The marker must occur once in the whole file. A stray copy before
// the area would otherwise be taken for its start, and Patch never writes
// a payload that holds one (see addsMarker), so a copy inside the area is
// a stray too.
func findArea(exe, marker []byte, size int) (start, end uint64, err error) {
	off := bytes.Index(exe, marker)
	switch {
	case off < 0:
		return 0, 0, ErrNoArea
	case bytes.Contains(exe[off+1:], marker):
		return 0, 0, ErrManyAreas
	case size > len(exe)-off:
		return 0, 0, malformed("the reserved area at %#x runs past the end of the file", off)
	}
	area := exe[off : off+size]
	start = uint64(off)
	return start, start + uint64(len(area)), nil
}

// addsMarker reports whether writing data into the area that ends at end
// would put another copy of the marker into exe: inside data, or across
// its edges, where it meets the marker, the zeros after it, or the bytes
// after the area. room is what the area holds after the marker.
func addsMarker(exe []byte, end uint64, marker, data []byte, room int) bool {
	reach := len(marker) - 1
	zeros := min(reach, room-len(data))
	w := make([]byte, 0, len(marker)+len(data)+2*reach)
	w = append(append(w, marker...), data...)
	w = append(w, make([]byte, zeros)...)
	if zeros < reach {
		after := exe[end:]
		w = append(w, after[:min(reach-zeros, len(after))]...)
	}
	return bytes.Contains(w[1:], marker)
}
