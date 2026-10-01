package testrun_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/spechtlabs/sigil/internal/testrun"
)

// TestUnreadable checks the record of a test file that couldn't be read:
// its error names the file, and its cases are an empty list, not null.
func TestUnreadable(t *testing.T) {
	got, err := json.Marshal(testrun.Unreadable("a_test.yaml", errors.New("permission denied")))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"file":"a_test.yaml","policy":"","error":"a_test.yaml couldn't be read: permission denied","cases":[]}`
	if string(got) != want {
		t.Errorf("Unreadable() = %s, want %s", got, want)
	}
}
