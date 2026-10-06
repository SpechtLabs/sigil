package jsonrpc

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/sierrasoftworks/humane-errors-go"
)

// MaxSize is the largest message body a [Reader] accepts, 64 MiB: far
// more than any document an editor sends, and little enough that a
// broken header can't make the server allocate without bound.
const MaxSize = 64 << 20

// maxHeaderLine is the longest header line a [Reader] reads; a longer
// one isn't a header.
const maxHeaderLine = 4096

// Reader reads message bodies from a stream framed by the base protocol.
// It isn't safe for concurrent use.
type Reader struct {
	r *bufio.Reader
}

// Writer writes messages to a stream framed by the base protocol. It's
// safe for concurrent use: each message is written whole, in one call to
// the underlying writer.
type Writer struct {
	w  io.Writer
	mu sync.Mutex
}

// NewReader returns a Reader reading from r.
func NewReader(r io.Reader) *Reader {
	return &Reader{r: bufio.NewReaderSize(r, maxHeaderLine)}
}

// NewWriter returns a Writer writing to w.
func NewWriter(w io.Writer) *Writer {
	return &Writer{w: w}
}

// Read returns the next message's body. It returns [io.EOF] when the
// stream ends between two messages. Any other error is a header it can't
// read: a line that isn't `Name: value`, a header without a valid
// Content-Length, a body larger than [MaxSize], or a stream that ends
// inside a message. After one the stream can't be trusted to be at a
// message boundary any more, so the caller stops reading. Headers other
// than Content-Length, such as Content-Type, are accepted and ignored.
func (r *Reader) Read() ([]byte, error) {
	length := -1
	for first := true; ; first = false {
		line, err := r.r.ReadSlice('\n')
		switch {
		case errors.Is(err, io.EOF) && first && len(line) == 0:
			return nil, io.EOF
		case errors.Is(err, bufio.ErrBufferFull):
			return nil, humane.New(fmt.Sprintf("a header line is longer than %d bytes", maxHeaderLine), "every message starts with `Content-Length: N` and a blank line")
		case err != nil:
			return nil, humane.Wrap(unexpected(err), "the stream ended inside a message header", "every message starts with `Content-Length: N` and a blank line")
		}
		text := strings.TrimRight(string(line), "\r\n")
		if text == "" {
			break
		}
		name, value, ok := strings.Cut(text, ":")
		if !ok {
			return nil, humane.New(fmt.Sprintf("the header line %q isn't `Name: value`", text), "every message starts with `Content-Length: N` and a blank line")
		}
		if !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		n, herr := contentLength(strings.TrimSpace(value), length)
		if herr != nil {
			return nil, herr
		}
		length = n
	}
	if length < 0 {
		return nil, humane.New("a message header has no Content-Length", "every message starts with `Content-Length: N` and a blank line")
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(r.r, body); err != nil {
		return nil, humane.Wrap(unexpected(err), fmt.Sprintf("the stream ended inside a message body of %d bytes", length), "Content-Length counts the body's bytes, not its characters")
	}
	return body, nil
}

// Write writes m, with Version filled in, as one framed message.
func (w *Writer) Write(m *Message) error {
	if m == nil {
		return humane.New("there is no message to send", "this is a bug in the language server; please report it")
	}
	m.JSONRPC = Version
	body, err := json.Marshal(m)
	if err != nil {
		return humane.Wrap(err, "the message couldn't be encoded", "this is a bug in the language server; please report it")
	}
	frame := make([]byte, 0, len(body)+32)
	frame = append(frame, "Content-Length: "...)
	frame = strconv.AppendInt(frame, int64(len(body)), 10)
	frame = append(frame, "\r\n\r\n"...)
	frame = append(frame, body...)
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.w.Write(frame); err != nil {
		return humane.Wrap(err, "the message couldn't be sent", "the editor may have closed the connection")
	}
	return nil
}

// contentLength reads a Content-Length value: a decimal byte count of at
// most MaxSize. prev is the value of an earlier Content-Length in the same
// header, or -1; a second one must agree with it.
func contentLength(value string, prev int) (int, humane.Error) {
	n, err := strconv.Atoi(value)
	switch {
	case err != nil || n < 0:
		return 0, humane.New(fmt.Sprintf("Content-Length %q isn't a byte count", value), "Content-Length is the body's length in bytes, a whole number")
	case n > MaxSize:
		return 0, humane.New(fmt.Sprintf("a message of %d bytes is larger than the %d the server accepts", n, MaxSize), "send documents smaller than 64 MiB")
	case prev >= 0 && n != prev:
		return 0, humane.New("a message header has two different Content-Lengths", "send one Content-Length per message")
	}
	return n, nil
}

// unexpected turns the end of the stream inside a message into
// io.ErrUnexpectedEOF, so the error can't be taken for io.EOF, the clean
// end between two messages.
func unexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}
