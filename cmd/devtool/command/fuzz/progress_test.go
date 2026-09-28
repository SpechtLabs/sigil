package fuzz

import (
	"bytes"
	"strings"
	"testing"
)

func TestProgress(t *testing.T) {
	tests := []struct {
		name string
		// chunks are written one Write at a time, the way a pipe delivers
		// them: not necessarily on line boundaries.
		chunks []string

		wantStats   stats
		wantUpdates int
		wantInput   string
		wantRerun   string
		wantFailure string
	}{
		{
			name: "baseline coverage",
			chunks: []string{
				"fuzz: elapsed: 0s, gathering baseline coverage: 0/354 completed\n",
				"fuzz: elapsed: 0s, gathering baseline coverage: 354/354 completed, now fuzzing with 2 workers\n",
			},
			wantStats:   stats{baseline: "354/354"},
			wantUpdates: 2,
		},
		{
			name: "fuzzing, split across writes",
			chunks: []string{
				"fuzz: elapsed: 3s, execs: 187512 (624",
				"86/sec), new interesting: 7 (total: 361)\nPASS\n",
			},
			wantStats:   stats{elapsed: "3s", execs: 187512, rate: 62486, found: 7},
			wantUpdates: 1,
		},
		{
			name: "a failing input",
			chunks: []string{
				"fuzz: elapsed: 0s, minimizing\n",
				"--- FAIL: FuzzSplit (0.03s)\n    --- FAIL: FuzzSplit (0.00s)\n        parse_test.go:9: broken on \"x00\"\r\n",
				"    Failing input written to testdata/fuzz/FuzzSplit/2a05b2db6d189648\n",
				"    To re-run:\n    go test -run=FuzzSplit/2a05b2db6d189648\nFAIL\n",
			},
			wantInput:   "testdata/fuzz/FuzzSplit/2a05b2db6d189648",
			wantRerun:   "go test -run=FuzzSplit/2a05b2db6d189648",
			wantFailure: "--- FAIL: FuzzSplit (0.03s)\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stream bytes.Buffer
			updates := 0
			p := &progress{stream: &stream}
			p.update = func(stats) { updates++ }
			for _, c := range tt.chunks {
				if n, err := p.Write([]byte(c)); err != nil || n != len(c) {
					t.Fatalf("Write() = %d, %v", n, err)
				}
			}

			all := strings.Join(tt.chunks, "")
			if stream.String() != all || p.output() != all {
				t.Errorf("stream = %q, output = %q, want both %q", stream.String(), p.output(), all)
			}
			if p.stats != tt.wantStats {
				t.Errorf("stats = %+v, want %+v", p.stats, tt.wantStats)
			}
			if updates != tt.wantUpdates {
				t.Errorf("updates = %d, want %d", updates, tt.wantUpdates)
			}
			if p.input != tt.wantInput || p.rerun != tt.wantRerun {
				t.Errorf("input, rerun = %q, %q; want %q, %q", p.input, p.rerun, tt.wantInput, tt.wantRerun)
			}
			if f := withoutProgress(p.output()); strings.Contains(f, "fuzz: ") || !strings.HasPrefix(f, tt.wantFailure) {
				t.Errorf("withoutProgress() = %q, want it to start with %q and drop progress lines", f, tt.wantFailure)
			}
		})
	}
}

func TestStats(t *testing.T) {
	tests := []struct {
		name        string
		s           stats
		wantString  string
		wantSummary string
	}{
		{name: "not started", s: stats{}, wantString: "starting", wantSummary: "0 execs · 0 new inputs"},
		{name: "baseline", s: stats{baseline: "12/354"}, wantString: "gathering baseline coverage 12/354", wantSummary: "0 execs · 0 new inputs"},
		{
			name:        "fuzzing",
			s:           stats{elapsed: "3s", execs: 187512, rate: 62486, found: 1},
			wantString:  "3s · 188k execs (62.5k/s) · 1 new",
			wantSummary: "188k execs (62.5k/s) · 1 new input",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.s.String(); got != tt.wantString {
				t.Errorf("String() = %q, want %q", got, tt.wantString)
			}
			if got := tt.s.summary(); got != tt.wantSummary {
				t.Errorf("summary() = %q, want %q", got, tt.wantSummary)
			}
		})
	}
}
