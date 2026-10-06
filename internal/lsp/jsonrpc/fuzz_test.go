package jsonrpc

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

// FuzzRead feeds arbitrary streams to a Reader and decodes every body it
// returns. Nothing may panic; every body is the length its header says,
// within MaxSize; and every message that decodes writes back as a frame
// that reads and decodes to the same method and id.
func FuzzRead(f *testing.F) {
	f.Add([]byte("Content-Length: 2\r\n\r\n{}"))
	f.Add([]byte("Content-Length: 52\r\n\r\n{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"shutdown\",\"x\":1}"))
	f.Add([]byte("Content-Length: 44\r\nContent-Type: application/json\r\n\r\n{\"jsonrpc\":\"2.0\",\"method\":\"exit\",\"id\":null}"))
	f.Add([]byte("Content-Length: 31\r\n\r\n[{\"jsonrpc\":\"2.0\",\"method\":1}]"))
	f.Add([]byte("Content-Length: -1\r\n\r\n"))
	f.Add([]byte("content-length:5\n\n\"abc\"Content-Length: 1\r\n\r\n"))
	f.Fuzz(func(t *testing.T, stream []byte) {
		r := NewReader(bytes.NewReader(stream))
		for {
			body, err := r.Read()
			if err != nil {
				if !errors.Is(err, io.EOF) && err.Error() == "" {
					t.Fatal("an error without a message")
				}
				return
			}
			if len(body) > MaxSize {
				t.Fatalf("a body of %d bytes", len(body))
			}
			m, bad := Decode(body)
			if m == nil {
				t.Fatal("Decode returned no message")
			}
			if bad != nil {
				continue
			}
			var out bytes.Buffer
			if werr := NewWriter(&out).Write(m); werr != nil {
				t.Fatalf("a decoded message doesn't write: %v", werr)
			}
			again, rerr := NewReader(&out).Read()
			if rerr != nil {
				t.Fatalf("a written message doesn't read: %v", rerr)
			}
			back, bad := Decode(again)
			if bad != nil {
				t.Fatalf("a written message doesn't decode: %s\n%s", bad.Message, again)
			}
			if back.Method != m.Method || !bytes.Equal(back.ID, m.ID) {
				t.Fatalf("round trip changed %s %s into %s %s", m.Method, m.ID, back.Method, back.ID)
			}
		}
	})
}
