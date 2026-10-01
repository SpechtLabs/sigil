package payload_test

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"encoding/binary"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/internal/payload"
)

// TestRoundTrip checks that Decode returns what Encode was given, with
// the area's zeros after it or without, and that the digest survives.
func TestRoundTrip(t *testing.T) {
	built := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	big := strings.Repeat("policy p.q: Access@1\n\nlet x = \"ünïcödé ✓\"\n", 4096)
	tests := []struct {
		name string
		p    *payload.Payload
	}{
		{name: "empty", p: &payload.Payload{}},
		{name: "every field", p: &payload.Payload{Bundle: *base(), Sigil: "v1.2.3", Built: built}},
		{name: "no built time", p: &payload.Payload{Bundle: *base(), Sigil: "dev"}},
		{name: "built in another zone", p: &payload.Payload{Bundle: *base(), Built: built.In(time.FixedZone("CEST", 2*60*60))}},
		{name: "a large, repetitive file", p: &payload.Payload{Bundle: payload.Bundle{Paths: []payload.File{{Name: "big.sigil", Source: big}}}}},
		{name: "names and text that JSON escapes", p: &payload.Payload{Bundle: payload.Bundle{
			Root:  "a.b",
			Paths: []payload.File{{Name: "<dir> & \"quoted\"/a.sigil", Source: "\x00\t </script>\U0001F600"}},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := payload.Encode(tt.p)
			if err != nil {
				t.Fatal(err)
			}
			for _, area := range [][]byte{data, append(data, make([]byte, 1024)...)} {
				got, err := payload.Decode(area)
				if err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				if !got.Built.Equal(tt.p.Built) {
					t.Errorf("Built = %v, want %v", got.Built, tt.p.Built)
				}
				got.Built = tt.p.Built
				if !reflect.DeepEqual(got, tt.p) {
					t.Errorf("Decode() = %+v, want %+v", got, tt.p)
				}
				if got.Bundle.Digest() != tt.p.Bundle.Digest() {
					t.Error("the digest changed on the way")
				}
			}
		})
	}
	small, err := payload.Encode(&payload.Payload{Bundle: payload.Bundle{Paths: []payload.File{{Name: "big.sigil", Source: big}}}})
	if err != nil || len(small) > len(big)/20 {
		t.Errorf("Encode() = %d bytes, %v, want a repetitive file compressed", len(small), err)
	}
}

// TestDigestIgnoresSigilAndBuilt checks that two compilations of the same
// files, by another sigil at another time, have the same digest.
func TestDigestIgnoresSigilAndBuilt(t *testing.T) {
	digests := make([]string, 0, 3)
	for _, p := range []*payload.Payload{
		{Bundle: *base(), Sigil: "v1.0.0", Built: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Bundle: *base(), Sigil: "v2.0.0", Built: time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)},
		{Bundle: *base()},
	} {
		data, err := payload.Encode(p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := payload.Decode(data)
		if err != nil {
			t.Fatal(err)
		}
		digests = append(digests, got.Bundle.Digest())
	}
	if digests[0] != baseDigest || digests[1] != baseDigest || digests[2] != baseDigest {
		t.Errorf("digests = %v, want %s each time", digests, baseDigest)
	}
}

// TestEncodeErrors checks that Encode refuses what it can't carry
// unchanged.
func TestEncodeErrors(t *testing.T) {
	bad := "\xff\xfe"
	tests := []struct {
		name   string
		change func(p *payload.Payload)
		want   string
	}{
		{name: "a root that isn't UTF-8", change: func(p *payload.Payload) { p.Bundle.Root = bad }, want: `the root policy's name "\xff\xfe" isn't UTF-8`},
		{name: "a kind file's name", change: func(p *payload.Payload) { p.Bundle.Kinds[0].Name = bad }, want: `the file name "\xff\xfe" isn't UTF-8`},
		{name: "a path's source", change: func(p *payload.Payload) { p.Bundle.Paths[1].Source += bad }, want: "team/lib.sigil isn't UTF-8 text, and a compiled bundle holds only text"},
		{name: "a trusted file's source", change: func(p *payload.Payload) { p.Bundle.Trusted[0].Source = bad }, want: "platform/base.sigil isn't UTF-8 text"},
		{name: "a requirement's policy", change: func(p *payload.Payload) { p.Bundle.Require[0].Policy = bad }, want: `the requirement of "\xff\xfe" names "\xff\xfe", which isn't UTF-8`},
		{name: "a requirement's trusted path", change: func(p *payload.Payload) { p.Bundle.Require[0].Trusted[0] = bad }, want: `the requirement of "platform.base" names "\xff\xfe"`},
		{name: "a requirement's root", change: func(p *payload.Payload) { p.Bundle.Require[0].Roots[0] = bad }, want: `the requirement of "platform.base" names "\xff\xfe"`},
		{name: "a built time JSON can't hold", change: func(p *payload.Payload) { p.Built = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }, want: "the payload can't be encoded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &payload.Payload{Bundle: *base()}
			tt.change(p)
			data, err := payload.Encode(p)
			if err == nil || !strings.Contains(err.Error(), tt.want) || len(err.Advice()) == 0 {
				t.Errorf("Encode() = %d bytes, %v, want %q with advice", len(data), err, tt.want)
			}
		})
	}
	if data, err := payload.Encode(nil); err == nil || err.Error() != "there's no payload to encode" {
		t.Errorf("Encode(nil) = %d bytes, %v, want no payload", len(data), err)
	}
}

// TestDecode checks what Decode makes of data that isn't a payload.
func TestDecode(t *testing.T) {
	good, err := payload.Encode(&payload.Payload{Bundle: *base(), Sigil: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}
	with := func(change func(data []byte) []byte) []byte { return change(bytes.Clone(good)) }
	tests := []struct {
		name string
		data []byte
		want string // the start of the error; empty for no payload, and no error
	}{
		{name: "nothing", data: nil},
		{name: "a few zeros", data: make([]byte, 3)},
		{name: "an empty area", data: make([]byte, payload.AreaSize-8)},
		{name: "a version of zero before other bytes", data: with(func(d []byte) []byte { copy(d, []byte{0, 0, 0, 0}); return d })},
		{name: "a header shorter than its version", data: []byte{1, 0}, want: "the compiled bundle is damaged: its header is cut short"},
		{name: "a header cut short", data: good[:20], want: "the compiled bundle is damaged: its header is cut short"},
		{name: "a newer format", data: with(func(d []byte) []byte { d[0] = 2; return d }), want: "the compiled bundle was written by a newer sigil: its format is version 2, and this sigil reads up to version 1"},
		{name: "a body cut short", data: good[:len(good)-1], want: "the compiled bundle is damaged: its header says"},
		{name: "a length beyond any area", data: with(func(d []byte) []byte { binary.LittleEndian.PutUint32(d[4:], 0xffffffff); return d }), want: "the compiled bundle is damaged: its header says 4294967295 bytes follow"},
		{name: "a flipped bit in the body", data: with(func(d []byte) []byte { d[len(d)-1] ^= 1; return d }), want: "the compiled bundle is damaged: its checksum doesn't match its contents"},
		{name: "a flipped bit in the checksum", data: with(func(d []byte) []byte { d[8] ^= 1; return d }), want: "the compiled bundle is damaged: its checksum doesn't match"},
		{name: "a body that isn't flate", data: frame([]byte{0xff, 0xff, 0xff}), want: "the compiled bundle is damaged: it doesn't unpack"},
		{name: "a body that unpacks to too much", data: frame(deflate(make([]byte, 64<<20+1))), want: "the compiled bundle is damaged: it unpacks to more than 64 MiB"},
		{name: "a body that isn't JSON", data: frame(deflate([]byte("not json"))), want: "the compiled bundle is damaged: it doesn't read as a payload"},
		{name: "a body of the wrong shape", data: frame(deflate([]byte(`{"bundle": {"paths": "nope"}}`))), want: "the compiled bundle is damaged: it doesn't read as a payload"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := payload.Decode(tt.data)
			if tt.want == "" {
				if p != nil || err != nil {
					t.Errorf("Decode() = %v, %v, want nil, nil", p, err)
				}
				return
			}
			if p != nil || err == nil || !strings.HasPrefix(err.Error(), tt.want) || len(err.Advice()) == 0 {
				t.Errorf("Decode() = %v, %v, want an error with advice, saying %q", p, err, tt.want)
			}
		})
	}
}

// FuzzDecode checks that Decode never panics, neither on arbitrary data
// nor on an arbitrary body behind a valid header, and that a payload it
// decodes encodes again to the same payload.
func FuzzDecode(f *testing.F) {
	good, err := payload.Encode(&payload.Payload{Bundle: *base(), Sigil: "v1.2.3", Built: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte{})
	f.Add([]byte{1, 0, 0, 0})
	f.Add(deflate([]byte(`{"bundle": {"root": "a.b", "paths": [{"name": "a", "source": "b"}]}, "sigil": "v1"}`)))
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, in := range [][]byte{data, frame(data)} {
			p, err := payload.Decode(in)
			if err != nil || p == nil {
				continue
			}
			again, err := payload.Encode(p)
			if err != nil {
				t.Fatalf("Encode(Decode(data)) error = %v", err) // JSON decodes every string to UTF-8
			}
			q, err := payload.Decode(again)
			if err != nil {
				t.Fatalf("Decode(Encode(p)) error = %v", err)
			}
			if q.Bundle.Digest() != p.Bundle.Digest() || q.Sigil != p.Sigil || !q.Built.Equal(p.Built) {
				t.Fatalf("Decode(Encode(p)) = %+v, want %+v", q, p)
			}
		}
	})
}

// frame puts a valid header of format version 1 before body.
func frame(body []byte) []byte {
	out := make([]byte, 40, 40+len(body))
	binary.LittleEndian.PutUint32(out, 1)
	binary.LittleEndian.PutUint32(out[4:], uint32(len(body)))
	sum := sha256.Sum256(body)
	copy(out[8:], sum[:])
	return append(out, body...)
}

// deflate compresses data as Encode does.
func deflate(data []byte) []byte {
	var buf bytes.Buffer
	w, _ := flate.NewWriter(&buf, flate.BestCompression)
	_, _ = w.Write(data)
	_ = w.Close()
	return buf.Bytes()
}
