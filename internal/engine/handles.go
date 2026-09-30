package engine

import (
	"strconv"
	"sync"

	"github.com/sierrasoftworks/humane-errors-go"
)

// handles is the table of compiled policies, by handle. Its methods are
// safe for concurrent use.
type handles struct {
	byHandle map[uint32]*compiled
	mu       sync.Mutex // guards byHandle and last
	last     uint32     // the last handle handed out
}

func newHandles() *handles {
	return &handles{byHandle: map[uint32]*compiled{}}
}

// keep puts a compiled policy in the table and returns its handle, never
// 0 and never one in use, so a handle released and kept by the host by
// mistake never reaches another policy until the numbers wrap.
func (t *handles) keep(c *compiled) uint32 {
	t.mu.Lock()
	defer t.mu.Unlock()
	for {
		t.last++
		if _, taken := t.byHandle[t.last]; t.last != 0 && !taken {
			t.byHandle[t.last] = c
			return t.last
		}
	}
}

// lookup returns the compiled policy of a handle.
func (t *handles) lookup(h uint32) (*compiled, humane.Error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.byHandle[h]
	if !ok {
		return nil, unknownHandle(h)
	}
	return c, nil
}

// drop removes a handle from the table.
func (t *handles) drop(h uint32) humane.Error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.byHandle[h]; !ok {
		return unknownHandle(h)
	}
	delete(t.byHandle, h)
	return nil
}

// unknownHandle is the error for a handle compile didn't return, or that
// was released.
func unknownHandle(h uint32) humane.Error {
	return humane.New("no compiled policy has handle "+strconv.FormatUint(uint64(h), 10), "pass a handle compile returned, before releasing it")
}
