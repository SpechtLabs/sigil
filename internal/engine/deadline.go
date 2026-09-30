package engine

import (
	"context"
	"sync"
	"time"
)

// deadline is a context that's done once its deadline passes, which it
// notices when it's polled rather than with a timer. A wasip1 module runs
// on one thread without preemption, so the timer of a
// [context.WithDeadline] can't fire while an evaluation runs, and the
// evaluation runs to its end however late; the evaluator polls its
// context before every rule and every few hundred loop steps, and each
// poll here reads the clock instead.
type deadline struct {
	context.Context
	at   time.Time
	now  func() time.Time
	done chan struct{}
	once sync.Once
}

// withDeadline returns a context done at at, by the clock now.
func withDeadline(now func() time.Time, at time.Time) *deadline {
	return &deadline{Context: context.Background(), at: at, now: now, done: make(chan struct{})}
}

// Deadline implements [context.Context]. It returns the deadline.
func (d *deadline) Deadline() (time.Time, bool) { return d.at, true }

// Done implements [context.Context]. The channel is closed by the first
// call after the deadline.
func (d *deadline) Done() <-chan struct{} {
	if !d.now().Before(d.at) {
		d.once.Do(func() { close(d.done) })
	}
	return d.done
}

// Err implements [context.Context]. It returns
// [context.DeadlineExceeded] once the deadline has passed, and nil
// before.
func (d *deadline) Err() error {
	select {
	case <-d.Done():
		return context.DeadlineExceeded
	default:
		return nil
	}
}
