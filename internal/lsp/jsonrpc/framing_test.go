package jsonrpc

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestRead reads framed streams: each case lists the bodies it yields,
// then the error that ends it, io.EOF for a clean end.
func TestRead(t *testing.T) {
	long := strings.Repeat("x", maxHeaderLine)
	tests := []struct {
		name   string
		stream string
		bodies []string
		err    string // a substring of the error after the bodies; empty for io.EOF
	}{
		{name: "one message", stream: "Content-Length: 2\r\n\r\n{}", bodies: []string{"{}"}},
		{name: "two messages", stream: "Content-Length: 2\r\n\r\n{}Content-Length: 4\r\n\r\nnull", bodies: []string{"{}", "null"}},
		{name: "an empty stream", stream: ""},
		{name: "a content type", stream: "Content-Length: 2\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n{}", bodies: []string{"{}"}},
		{name: "the type first", stream: "Content-Type: x\r\nContent-Length: 2\r\n\r\n{}", bodies: []string{"{}"}},
		{name: "any case", stream: "content-length: 2\r\n\r\n{}", bodies: []string{"{}"}},
		{name: "bare newlines", stream: "Content-Length: 2\n\n{}", bodies: []string{"{}"}},
		{name: "spaces around the value", stream: "Content-Length:   2  \r\n\r\n{}", bodies: []string{"{}"}},
		{name: "an empty body", stream: "Content-Length: 0\r\n\r\n", bodies: []string{""}},
		{name: "the same length twice", stream: "Content-Length: 2\r\nContent-Length: 2\r\n\r\n{}", bodies: []string{"{}"}},
		{name: "a body of multi-byte characters", stream: "Content-Length: 7\r\n\r\n\"é€\"", bodies: []string{"\"é€\""}},
		{name: "no length", stream: "Content-Type: x\r\n\r\n{}", err: "has no Content-Length"},
		{name: "no header at all", stream: "\r\n{}", err: "has no Content-Length"},
		{name: "a line that isn't a header", stream: "hello\r\n\r\n", err: "isn't `Name: value`"},
		{name: "a length that isn't a number", stream: "Content-Length: two\r\n\r\n{}", err: `Content-Length "two" isn't a byte count`},
		{name: "a negative length", stream: "Content-Length: -1\r\n\r\n", err: "isn't a byte count"},
		{name: "a length too large", stream: fmt.Sprintf("Content-Length: %d\r\n\r\n", MaxSize+1), err: "larger than the 67108864 the server accepts"},
		{name: "a huge length", stream: "Content-Length: 99999999999999999999\r\n\r\n", err: "isn't a byte count"},
		{name: "two lengths", stream: "Content-Length: 2\r\nContent-Length: 3\r\n\r\n{}", err: "two different Content-Lengths"},
		{name: "a header line too long", stream: "X-" + long + "\r\n", err: "longer than 4096 bytes"},
		{name: "the end inside the header", stream: "Content-Length: 2\r\n", err: "ended inside a message header"},
		{name: "the end inside a header line", stream: "Content-Len", err: "ended inside a message header"},
		{name: "the end inside the body", stream: "Content-Length: 10\r\n\r\n{}", err: "ended inside a message body of 10 bytes"},
		{name: "a good message, then a broken one", stream: "Content-Length: 2\r\n\r\n{}junk\r\n\r\n", bodies: []string{"{}"}, err: "isn't `Name: value`"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(strings.NewReader(tt.stream))
			for _, want := range tt.bodies {
				got, err := r.Read()
				if err != nil {
					t.Fatalf("Read() = %v, want %q", err, want)
				}
				if string(got) != want {
					t.Fatalf("Read() = %q, want %q", got, want)
				}
			}
			_, err := r.Read()
			switch {
			case tt.err == "" && !errors.Is(err, io.EOF):
				t.Errorf("Read() at the end = %v, want io.EOF", err)
			case tt.err != "" && (err == nil || errors.Is(err, io.EOF) || !strings.Contains(err.Error(), tt.err)):
				t.Errorf("Read() at the end = %v, want an error holding %q", err, tt.err)
			}
		})
	}
}

// TestWrite checks the framing Write puts around a message, and that
// what it writes reads back.
func TestWrite(t *testing.T) {
	tests := []struct {
		name string
		msg  *Message
		want string // the body
	}{
		{name: "a response", msg: Response([]byte("1"), map[string]int{"a": 1}), want: "{\"jsonrpc\":\"2.0\",\"id\":1,\"result\":{\"a\":1}}"},
		{name: "a null result", msg: Response([]byte(`"x"`), nil), want: "{\"jsonrpc\":\"2.0\",\"id\":\"x\",\"result\":null}"},
		{name: "a failure without an id", msg: Failure(nil, ParseError, "bad"), want: "{\"error\":{\"message\":\"bad\",\"code\":-32700},\"jsonrpc\":\"2.0\",\"id\":null}"},
		{name: "a notification", msg: Notification("window/showMessage", map[string]string{"message": "é"}), want: "{\"jsonrpc\":\"2.0\",\"method\":\"window/showMessage\",\"params\":{\"message\":\"é\"}}"},
		{name: "a version filled in", msg: &Message{Method: "x"}, want: "{\"jsonrpc\":\"2.0\",\"method\":\"x\"}"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := NewWriter(&out).Write(tt.msg); err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(tt.want), tt.want)
			if out.String() != want {
				t.Errorf("Write() wrote %q, want %q", out.String(), want)
			}
			body, err := NewReader(&out).Read()
			if err != nil {
				t.Fatalf("what Write wrote doesn't read: %v", err)
			}
			if _, bad := Decode(body); bad != nil {
				t.Errorf("what Write wrote doesn't decode: %s", bad.Message)
			}
		})
	}
}

// TestWriteFails checks Write's errors: nothing to send, a value that
// can't be encoded, and a writer that fails.
func TestWriteFails(t *testing.T) {
	tests := []struct {
		name string
		w    io.Writer
		msg  *Message
		want string
	}{
		{name: "no message", w: io.Discard, want: "there is no message to send"},
		{name: "raw that isn't JSON", w: io.Discard, msg: &Message{Method: "x", Params: []byte("{")}, want: "couldn't be encoded"},
		{name: "a closed writer", w: failingWriter{}, msg: Notification("x", nil), want: "couldn't be sent"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := NewWriter(tt.w).Write(tt.msg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("Write() = %v, want an error holding %q", err, tt.want)
			}
		})
	}
}

// failingWriter fails every write.
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
