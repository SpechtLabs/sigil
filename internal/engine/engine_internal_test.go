package engine

import (
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
