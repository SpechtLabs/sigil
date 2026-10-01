package stamp_test

import (
	"bytes"
	"testing"

	"github.com/spechtlabs/sigil/internal/stamp"
)

// Patch and Verify must never panic, whatever the bytes. A Patch that fails
// leaves the binary as it was. One that succeeds keeps a signature that
// matched before matching after, and leaves exactly one marker, so the same
// Patch succeeds again.
func FuzzPatch(f *testing.F) {
	for _, s := range []machoSpec{
		{noSig: true},
		{cds: []cdSpec{{hashType: 2}}, cms: synthCMSNone},
		{cds: []cdSpec{{hashType: 1}, {hashType: 2, pageShift: 14}}, cms: 0},
		{cds: []cdSpec{{hashType: 3, version: 0x20100}, {hashType: 4, version: 0x20300, limit64: true}}, cms: 100},
	} {
		b, _ := buildMachO(s)
		f.Add(b, newPayload, uint16(synthAreaSize))
	}
	f.Add(buildELF(), newPayload, uint16(synthAreaSize))
	f.Add(buildPE(0x20b, 16, 0), []byte{}, uint16(synthAreaSize))
	f.Add(buildPE(0x10b, 16, 0x1000), newPayload, uint16(synthAreaSize))
	f.Add([]byte("\xca\xfe\xba\xbe"), newPayload, uint16(16))
	f.Add(append([]byte("\xcf\xfa\xed\xfe"), marker()...), []byte("x"), uint16(len(marker())+1))

	f.Fuzz(func(t *testing.T, exe, data []byte, size uint16) {
		valid := stamp.Verify(exe) == nil
		before := bytes.Clone(exe)
		if err := stamp.Patch(exe, marker(), int(size), data); err != nil {
			if !bytes.Equal(exe, before) {
				t.Fatalf("Patch() = %v and changed the binary", err)
			}
			return
		}
		if err := stamp.Verify(exe); valid && err != nil {
			t.Fatalf("Verify() after Patch() = %v, but was nil before", err)
		}
		if err := stamp.Patch(exe, marker(), int(size), data); err != nil {
			t.Fatalf("Patch() again = %v", err)
		}
	})
}
