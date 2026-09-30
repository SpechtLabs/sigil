package engine

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestDeadline checks that the deadline is done exactly from its time on,
// by its clock, without a timer.
func TestDeadline(t *testing.T) {
	start := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	now := start
	d := withDeadline(func() time.Time { return now }, start.Add(time.Second))
	if at, ok := d.Deadline(); !ok || !at.Equal(start.Add(time.Second)) {
		t.Errorf("Deadline() = %v, %v", at, ok)
	}
	tests := []struct {
		name string
		at   time.Duration // since start
		done bool
	}{
		{name: "before", at: 999 * time.Millisecond},
		{name: "at", at: time.Second, done: true},
		{name: "after", at: time.Minute, done: true},
		{name: "still done when the clock goes back", at: 0, done: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now = start.Add(tt.at)
			select {
			case <-d.Done():
				if !tt.done {
					t.Error("Done() is closed")
				}
			default:
				if tt.done {
					t.Error("Done() isn't closed")
				}
			}
			if err := d.Err(); errors.Is(err, context.DeadlineExceeded) != tt.done {
				t.Errorf("Err() = %v, want done = %v", err, tt.done)
			}
		})
	}
}
