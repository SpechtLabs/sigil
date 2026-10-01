package engine

import (
	"errors"
	"io/fs"
	"math"
	"strings"
	"testing"
)

// TestEncodeFailure checks that a response JSON can't carry, such as an
// infinite float, is still an answer.
func TestEncodeFailure(t *testing.T) {
	got := string(encode(map[string]any{"x": math.Inf(1)}))
	if !strings.HasPrefix(got, `{"ok":false,"error":{"message":"the response couldn't be encoded"`) {
		t.Errorf("encode(+Inf) = %s", got)
	}
}

// TestBlocked checks that an op's error with diagnostics reads as a
// humane error too.
func TestBlocked(t *testing.T) {
	b := stopped(nil, "the bundle doesn't check")
	if b.Error() != "the bundle doesn't check" || !strings.Contains(b.Display(), checkAdvice) || b.Cause() != nil || len(b.Advice()) != 1 {
		t.Errorf("blocked = %q, %q, %v, %v", b.Error(), b.Display(), b.Cause(), b.Advice())
	}
}

// TestJoinAdvice checks that advice reads as prose: a sentence, such as a
// did-you-mean question, is followed by a space, and a clause by a
// semicolon.
func TestJoinAdvice(t *testing.T) {
	tests := []struct {
		advice []string
		want   string
	}{
		{advice: nil, want: ""},
		{advice: []string{"fix it"}, want: "fix it"},
		{advice: []string{`did you mean "unused-let"?`, "the lints are unused-import, unused-let"}, want: `did you mean "unused-let"? the lints are unused-import, unused-let`},
		{advice: []string{"Fix the file.", "then check again"}, want: "Fix the file. then check again"},
		{advice: []string{"fix the file", "then check again"}, want: "fix the file; then check again"},
	}
	for _, tt := range tests {
		if got := joinAdvice(tt.advice); got != tt.want {
			t.Errorf("joinAdvice(%q) = %q, want %q", tt.advice, got, tt.want)
		}
	}
}

// TestDataFS checks that a data file reads as a read-only regular file
// of its source, named by its base name, and that a path the request
// didn't give doesn't exist.
func TestDataFS(t *testing.T) {
	d := dataFS{"shared/owner.json": []byte(`{"user": {}}`)}
	src, err := fs.ReadFile(d, "shared/owner.json")
	if err != nil || string(src) != `{"user": {}}` {
		t.Fatalf("ReadFile() = %q, %v", src, err)
	}
	info, err := fs.Stat(d, "shared/owner.json")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name() != "owner.json" || info.Size() != 12 || info.Mode() != 0o444 || !info.ModTime().IsZero() || info.IsDir() || info.Sys() != nil {
		t.Errorf("Stat() = %q, %d, %v, %v, %v, %v", info.Name(), info.Size(), info.Mode(), info.ModTime(), info.IsDir(), info.Sys())
	}
	if _, err := d.Open("shared/nope.json"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Open() of a missing file = %v, want fs.ErrNotExist", err)
	}
}
