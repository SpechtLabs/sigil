package eval

import "context"

// WithContext makes a bare frame poll ctx in its loops, as a frame of a
// policy evaluation under ctx does.
func WithContext(f *Frame, ctx context.Context) {
	f.run = &run{ctx: ctx}
	if ctx.Done() == nil {
		f.run.ctx = nil
	}
}

// Canceled returns the context's error a cancellation panic carries, or
// nil when r is any other panic value.
func Canceled(r any) error { //nolint:emptyinterface // a recovered panic value
	if c, ok := r.(*canceled); ok {
		return c.err
	}
	return nil
}
