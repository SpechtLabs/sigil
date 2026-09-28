package fuzz

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/ui"
)

var (
	// statusLine matches the line go test -fuzz prints every few seconds.
	statusLine = regexp.MustCompile(`^fuzz: elapsed: (\S+), execs: (\d+) \((\d+)/sec\), new interesting: (\d+)`)
	// baselineLine matches the lines printed while the seed corpus runs.
	baselineLine = regexp.MustCompile(`^fuzz: elapsed: \S+, gathering baseline coverage: (\d+/\d+)`)
	// failingInput matches where go test saved an input that failed.
	failingInput = regexp.MustCompile(`Failing input written to (\S+)`)
)

// stats is what go test last reported about a fuzzing run.
type stats struct {
	elapsed string
	execs   int64
	rate    int64
	found   int64
	// baseline is the seed-corpus progress, e.g. "120/354", until fuzzing
	// proper starts.
	baseline string
}

// progress collects go test -fuzz output: it keeps everything for when
// the run fails, passes it on to stream when that's set, and hands each
// complete line to its parser.
type progress struct {
	mu      sync.Mutex
	stream  io.Writer
	out     bytes.Buffer
	partial []byte
	parser
}

// parser reads go test -fuzz output line by line, and calls update
// whenever the run's stats change.
type parser struct {
	update func(stats)
	stats  stats
	// input and rerun are where go test saved a failing input and the
	// command it printed to replay it.
	input        string
	rerun        string
	rerunFollows bool
}

// String describes a run in progress for the status line.
func (s stats) String() string {
	if s.execs == 0 && s.baseline != "" {
		return "gathering baseline coverage " + s.baseline
	}
	if s.execs == 0 {
		return "starting"
	}
	return fmt.Sprintf("%s · %s execs (%s/s) · %d new", s.elapsed, ui.Count(float64(s.execs)), ui.Count(float64(s.rate)), s.found)
}

// Write implements io.Writer. exec writes a command's standard output and
// error through one Write at a time when both are the same writer, but the
// lock keeps it safe either way.
func (p *progress) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.out.Write(b)
	if p.stream != nil {
		if _, err := p.stream.Write(b); err != nil {
			return 0, err //nolint:errorwrap // an io.Writer passes its destination's error on
		}
	}
	p.partial = append(p.partial, b...)
	for {
		i := bytes.IndexByte(p.partial, '\n')
		if i < 0 {
			break
		}
		p.line(strings.TrimRight(string(p.partial[:i]), "\r"))
		p.partial = p.partial[i+1:]
	}
	return len(b), nil
}

// output returns everything go test printed.
func (p *progress) output() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.out.String()
}

func (p *parser) line(l string) {
	if p.rerunFollows {
		p.rerunFollows = false
		p.rerun = strings.TrimSpace(l)
		return
	}
	switch {
	case strings.TrimSpace(l) == "To re-run:":
		p.rerunFollows = true
	case failingInput.MatchString(l):
		p.input = failingInput.FindStringSubmatch(l)[1]
	case statusLine.MatchString(l):
		m := statusLine.FindStringSubmatch(l)
		p.stats = stats{elapsed: m[1], execs: atoi(m[2]), rate: atoi(m[3]), found: atoi(m[4])}
		p.notify()
	case baselineLine.MatchString(l):
		p.stats.baseline = baselineLine.FindStringSubmatch(l)[1]
		p.notify()
	}
}

func (p *parser) notify() {
	if p.update != nil {
		p.update(p.stats)
	}
}

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// withoutProgress returns go test's output without the progress lines
// the status line already showed.
func withoutProgress(out string) string {
	var b strings.Builder
	for line := range strings.Lines(out) {
		if !strings.HasPrefix(line, "fuzz: ") {
			b.WriteString(line)
		}
	}
	return b.String()
}
