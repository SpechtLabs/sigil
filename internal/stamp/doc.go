// Package stamp writes data into a compiled binary and keeps the binary
// runnable. It's how sigil compile puts a policy bundle into a copy of the
// sigil binary; stamp itself doesn't know what the data is.
//
// # The reserved area
//
// The binary reserves room for the data at link time: a package-level
// array of a fixed size that starts with a marker and is otherwise zero.
// Because it's initialized with non-zero bytes, the linker puts it into
// the binary's data, not into BSS, so it's in the file. [Patch] finds the
// marker, writes the data right after it, and zero-fills the rest of the
// area, so a longer payload that was there before never shows through.
// The marker has to occur exactly once in the whole binary: a stray copy
// less than an area before the real one would otherwise be taken for its
// start. So Patch refuses data that would put a second copy into the area
// ([ErrMarkerInData]), which keeps a patched binary patchable again, and
// stamp never spells a marker out itself. Callers build theirs at runtime,
// so that a binary that links stamp and its caller doesn't hold a second
// copy.
//
// # Why in place
//
// Appending the data, or adding a section for it, would move or grow the
// binary's segments, and on darwin it would also invalidate the code
// signature as a whole: the signature covers every byte up to where it
// starts, and its offset and size are recorded in the load commands. A
// payload written into bytes that already exist changes nothing but those
// bytes. The layout, the load commands and the signature's place stay as
// they are, and only the hashes of the pages the area touches go stale.
//
// # Code signatures
//
// darwin/arm64 runs nothing without a valid signature, so Go's linker
// signs every darwin/arm64 binary ad hoc. (darwin/amd64 binaries come out
// unsigned, unless `codesign -s -` signed them.) The signature is a
// SuperBlob holding a CodeDirectory, which lists a hash of every page of
// the file up to its code limit, and no certificate. The kernel checks each page against its
// hash as it pages it in and kills the process on a mismatch. An ad-hoc
// signature is a pure function of the file's contents, so Patch recomputes
// it: for every CodeDirectory in the SuperBlob (an alternate one with
// another hash type is possible), it re-hashes the pages the area overlaps,
// with the CodeDirectory's own page size and hash type, the last one cut
// short at the code limit. Every other page hash, and the special slots
// for the requirements and entitlements, stay as they are, since they
// don't cover the area. The CodeDirectory itself is part of no other hash
// in an ad-hoc signature, so nothing further changes. [Verify] checks
// every page hash, which lets tests check the result for darwin binaries
// cross-built on a host that can't run them.
//
// # What Patch refuses
//
//   - A Mach-O signed with an identity (a Developer ID, say), whose CMS
//     blob holds a signature over the CodeDirectory, and a PE with an
//     Authenticode signature: recomputing those takes the private key.
//     Patch returns [ErrSigned]; patch an unsigned or ad-hoc-signed binary
//     and sign the result afterwards. The empty CMS wrapper `codesign -s -`
//     writes counts as ad hoc.
//   - A universal (fat) Mach-O, a format other than ELF, 64-bit
//     little-endian Mach-O and PE, and a CodeDirectory that Patch can't
//     recompute (an unknown hash type, scatter vectors or no paging):
//     [ErrUnsupported].
//   - Headers or a code signature that contradict themselves, point outside
//     the file, or overlap the area: [ErrMalformed]. Writing anyway could
//     corrupt the binary in ways that only show when it runs.
//
// ELF binaries and unsigned PEs carry nothing that covers the area's
// bytes, so Patch only writes. Patch checks everything before it writes
// the first byte, so a binary it refuses is left as it was.
package stamp
