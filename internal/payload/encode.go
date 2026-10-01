package payload

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"unicode/utf8"

	"github.com/sierrasoftworks/humane-errors-go"
)

const (
	// formatVersion is the format Encode writes, and the newest Decode
	// reads.
	formatVersion = 1
	// headerSize is the size of the header before the body: the format
	// version, the body's length and its SHA-256.
	headerSize = 4 + 4 + sha256.Size
	// maxUnpacked bounds what a body may unpack to. A bundle that fits the
	// area unpacks to a few MiB; more is a damaged or hostile body.
	maxUnpacked = 64 << 20
	// damaged starts the message of every error for data that can't be
	// what Encode wrote.
	damaged = "the compiled bundle is damaged: "
	// recompile is the advice for a damaged bundle.
	recompile = "the binary changed after it was compiled; compile it again from its policies"
)

// Encode returns what stamp writes after the marker: a header (format
// version uint32 LE, starting at 1; body length uint32 LE; SHA-256 of the
// body) and the body, the payload as JSON, compressed with compress/flate.
//
// Every string in the bundle has to be UTF-8, since JSON can't carry
// other bytes unchanged, and a bundle that unpacked differently from what
// was checked would have another digest. Encode doesn't check that the
// result fits the area; stamp does.
func Encode(p *Payload) ([]byte, humane.Error) {
	if p == nil {
		return nil, humane.New("there's no payload to encode", "pass the payload to compile into the binary")
	}
	if err := p.Bundle.text(); err != nil {
		return nil, err
	}
	js, err := json.Marshal(p)
	if err != nil {
		return nil, humane.Wrap(err, "the payload can't be encoded", "give it a build time between the years 0 and 9999, which JSON can hold")
	}
	var body bytes.Buffer
	zw, _ := flate.NewWriter(&body, flate.BestCompression) // fails only for an invalid level
	_, _ = zw.Write(js)                                    // a bytes.Buffer never fails to write
	_ = zw.Close()
	if int64(body.Len()) > math.MaxUint32 {
		return nil, humane.New(fmt.Sprintf("the payload compresses to %d bytes, more than its header can describe", body.Len()), "compile fewer files")
	}
	out := make([]byte, headerSize, headerSize+body.Len())
	binary.LittleEndian.PutUint32(out[0:], formatVersion)
	binary.LittleEndian.PutUint32(out[4:], uint32(body.Len())) //nolint:gosec // checked against math.MaxUint32 above
	sum := sha256.Sum256(body.Bytes())
	copy(out[8:], sum[:])
	return append(out, body.Bytes()...), nil
}

// Decode reads what Encode wrote. Data after the body, such as the zeros
// that fill the rest of the area, is ignored. All-zero data (format
// version 0), as in an area nothing was written to, is no payload: it
// returns nil, nil. A newer format version is an error that says the
// bundle was written by a newer sigil; a header cut short, a length beyond
// the data, a checksum mismatch or a body that doesn't unpack is one that
// starts "the compiled bundle is damaged".
func Decode(data []byte) (*Payload, humane.Error) {
	if len(data) < 4 {
		if bytes.Count(data, []byte{0}) == len(data) {
			return nil, nil
		}
		return nil, humane.New(damaged+"its header is cut short", recompile)
	}
	switch v := binary.LittleEndian.Uint32(data); {
	case v == 0:
		return nil, nil
	case v > formatVersion:
		return nil, humane.New(
			fmt.Sprintf("the compiled bundle was written by a newer sigil: its format is version %d, and this sigil reads up to version %d", v, formatVersion),
			"compile the policies again with the sigil this binary was built from",
		)
	}
	if len(data) < headerSize {
		return nil, humane.New(damaged+"its header is cut short", recompile)
	}
	n, rest := binary.LittleEndian.Uint32(data[4:]), data[headerSize:]
	if int64(n) > int64(len(rest)) {
		return nil, humane.New(fmt.Sprintf(damaged+"its header says %d bytes follow, but only %d do", n, len(rest)), recompile)
	}
	body := rest[:n]
	if sum := sha256.Sum256(body); !bytes.Equal(sum[:], data[8:headerSize]) {
		return nil, humane.New(damaged+"its checksum doesn't match its contents", recompile)
	}
	js, err := io.ReadAll(io.LimitReader(flate.NewReader(bytes.NewReader(body)), maxUnpacked+1))
	if err != nil {
		return nil, humane.Wrap(err, damaged+"it doesn't unpack", recompile)
	}
	if len(js) > maxUnpacked {
		return nil, humane.New(fmt.Sprintf(damaged+"it unpacks to more than %d MiB", maxUnpacked>>20), recompile)
	}
	var p Payload
	if err := json.Unmarshal(js, &p); err != nil {
		return nil, humane.Wrap(err, damaged+"it doesn't read as a payload", recompile)
	}
	return &p, nil
}

// text returns an error naming the first string in the bundle that isn't
// UTF-8.
func (b *Bundle) text() humane.Error {
	const advice = "save the files as UTF-8, and name them and the policies in UTF-8"
	if !utf8.ValidString(b.Root) {
		return humane.New(fmt.Sprintf("the root policy's name %q isn't UTF-8", b.Root), advice)
	}
	for _, files := range [][]File{b.Kinds, b.Paths, b.Trusted} {
		for _, f := range files {
			if !utf8.ValidString(f.Name) {
				return humane.New(fmt.Sprintf("the file name %q isn't UTF-8", f.Name), advice)
			}
			if !utf8.ValidString(f.Source) {
				return humane.New(f.Name+" isn't UTF-8 text, and a compiled bundle holds only text", advice)
			}
		}
	}
	for _, r := range b.Require {
		for _, s := range append(append([]string{r.Policy}, r.Trusted...), r.Roots...) {
			if !utf8.ValidString(s) {
				return humane.New(fmt.Sprintf("the requirement of %q names %q, which isn't UTF-8", r.Policy, s), advice)
			}
		}
	}
	return nil
}
