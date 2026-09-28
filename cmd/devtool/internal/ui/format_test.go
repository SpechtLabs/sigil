package ui

import (
	"testing"
	"time"
)

func TestDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{d: 822 * time.Millisecond, want: "822ms"},
		{d: 11400 * time.Millisecond, want: "11s"},
		{d: 3*time.Minute + 5*time.Second, want: "3m05s"},
		{d: time.Hour + 2*time.Minute + 20*time.Second, want: "1h02m"},
	}
	for _, tt := range tests {
		if got := Duration(tt.d); got != tt.want {
			t.Errorf("Duration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestCount(t *testing.T) {
	tests := []struct {
		n    float64
		want string
	}{
		{n: 0, want: "0"},
		{n: 36, want: "36"},
		{n: 1.5, want: "1.5"},
		{n: 1800, want: "1.8k"},
		{n: 62486, want: "62.5k"},
		{n: 187512, want: "188k"},
		{n: 4.56e6, want: "4.56M"},
		{n: 2e9, want: "2G"},
	}
	for _, tt := range tests {
		if got := Count(tt.n); got != tt.want {
			t.Errorf("Count(%v) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestRemaining(t *testing.T) {
	tests := []struct {
		elapsed  time.Duration
		progress float64
		want     time.Duration
	}{
		{elapsed: time.Minute, progress: 0, want: 0},
		{elapsed: time.Minute, progress: 0.2, want: 4 * time.Minute},
		{elapsed: time.Minute, progress: 1, want: 0},
	}
	for _, tt := range tests {
		if got := Remaining(tt.elapsed, tt.progress); got != tt.want {
			t.Errorf("Remaining(%v, %v) = %v, want %v", tt.elapsed, tt.progress, got, tt.want)
		}
	}
}
