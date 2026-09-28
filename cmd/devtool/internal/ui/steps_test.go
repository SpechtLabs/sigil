package ui

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/spechtlabs/sigil/cmd/internal/pretty"
)

// sgr matches the color codes a terminal printer writes; the tests compare
// the text around them.
var sgr = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestSteps(t *testing.T) {
	type call struct {
		op       string // start, update, context, done, failed, clear
		subject  string
		context  string
		detail   string
		progress float64
	}
	lexer := call{op: "start", subject: "FuzzLexer", context: "./internal/lexer"}
	parse := call{op: "start", subject: "FuzzParse", context: "./a"}

	tests := []struct {
		name   string
		tty    bool
		stream bool
		calls  []call
		// want is the whole output, when it's deterministic; wantParts are
		// pieces it must contain, in order, and wantNot pieces it mustn't.
		want         string
		wantParts    []string
		wantNot      []string
		wantFinished int
	}{
		{
			name: "plain: only finished steps",
			calls: []call{
				lexer, {op: "update", detail: "3s", progress: 0.5}, {op: "done", detail: "160k execs"},
				parse, {op: "failed", detail: "failed"},
			},
			want: "✓ [1/3] FuzzLexer  ./internal/lexer  160k execs\n" +
				"✗ [2/3] FuzzParse  ./a               failed\n",
			wantFinished: 1,
		},
		{
			name:   "streaming: a line before each step's output",
			stream: true,
			calls:  []call{lexer, {op: "done", detail: "160k execs"}, parse, {op: "done"}},
			want: "  [1/3] FuzzLexer  ./internal/lexer\n" +
				"✓ [1/3] FuzzLexer  ./internal/lexer  160k execs\n" +
				"  [2/3] FuzzParse  ./a\n" +
				"✓ [2/3] FuzzParse  ./a\n",
			wantFinished: 2,
		},
		{
			name:   "streaming on a terminal: no status line",
			tty:    true,
			stream: true,
			calls:  []call{lexer, {op: "update", detail: "3s", progress: 0.5}, {op: "done"}},
			want: "  [1/3] FuzzLexer  ./internal/lexer\n" +
				"✓ [1/3] FuzzLexer  ./internal/lexer\n",
			wantFinished: 1,
		},
		{
			name:  "live: the status line redraws, then leaves the finished line",
			tty:   true,
			calls: []call{lexer, {op: "update", detail: "3s · 1.2M execs", progress: 0.5}, {op: "done", detail: "160k execs"}},
			wantParts: []string{
				clearLine + "› [1/3] FuzzLexer  ./internal/lexer  ",
				clearLine + "› [1/3] FuzzLexer  ./internal/lexer  3s · 1.2M execs  1m00s, about 5m00s left",
				clearLine + "✓ [1/3] FuzzLexer  ./internal/lexer  160k execs\n",
			},
			wantFinished: 1,
		},
		{
			name:         "live: no time left before any progress",
			tty:          true,
			calls:        []call{lexer, {op: "update", detail: "starting"}},
			wantParts:    []string{"starting  1m00s"},
			wantNot:      []string{"left"},
			wantFinished: 0,
		},
		{
			name:      "live: the context changes",
			tty:       true,
			calls:     []call{lexer, {op: "context", context: "./other"}, {op: "update", detail: "x"}},
			wantParts: []string{"FuzzLexer  ./other           x"},
		},
		{
			name:      "live: clear erases the status line",
			tty:       true,
			calls:     []call{lexer, {op: "clear"}},
			wantParts: []string{"1m00s" + clearLine},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			s := NewSteps(printer(&out, tt.tty), 3, tt.stream)
			s.line.width = 200
			s.Align("FuzzLexer", "./internal/lexer")
			s.Align("FuzzParse", "./a")
			// A minute in, so the time left is predictable.
			s.start = time.Now().Add(-time.Minute)

			for _, c := range tt.calls {
				var err error
				switch c.op {
				case "start":
					err = s.Next(c.subject, c.context)
				case "update":
					err = s.Progress(c.detail, c.progress)
				case "context":
					s.Context(c.context)
				case "done":
					err = s.Done(c.detail)
				case "failed":
					err = s.Failed(c.detail)
				case "clear":
					err = s.Clear()
				}
				if err != nil {
					t.Fatal(err)
				}
			}

			got := sgr.ReplaceAllString(out.String(), "")
			if tt.want != "" && got != tt.want {
				t.Errorf("output =\n%q\nwant\n%q", got, tt.want)
			}
			rest := got
			for _, part := range tt.wantParts {
				i := strings.Index(rest, part)
				if i < 0 {
					t.Fatalf("output doesn't contain %q after the earlier parts:\n%q", part, got)
				}
				rest = rest[i+len(part):]
			}
			for _, not := range tt.wantNot {
				if strings.Contains(got, not) {
					t.Errorf("output contains %q:\n%q", not, got)
				}
			}
			if s.Finished() != tt.wantFinished {
				t.Errorf("Finished() = %d, want %d", s.Finished(), tt.wantFinished)
			}
			if s.Elapsed() < time.Minute {
				t.Errorf("Elapsed() = %v, want at least a minute", s.Elapsed())
			}
		})
	}
}

func TestStepsCounterWidth(t *testing.T) {
	var out bytes.Buffer
	s := NewSteps(printer(&out, false), 10, false)
	s.Align("round", "")
	for range 3 {
		if err := s.Next("round", ""); err != nil {
			t.Fatal(err)
		}
		if err := s.Done("1s"); err != nil {
			t.Fatal(err)
		}
	}
	want := "✓ [ 1/10] round  1s\n✓ [ 2/10] round  1s\n✓ [ 3/10] round  1s\n"
	if out.String() != want {
		t.Errorf("output = %q, want %q", out.String(), want)
	}
}

// printer returns a Printer that writes to w without colors, as a
// terminal when tty is set.
func printer(w *bytes.Buffer, tty bool) *pretty.Printer {
	env := []string{"TERM=xterm-256color", "NO_COLOR=1"}
	if tty {
		env = append(env, "CLICOLOR_FORCE=1")
	}
	return pretty.New(w, pretty.WithEnviron(env), pretty.WithDarkBackground(true))
}
