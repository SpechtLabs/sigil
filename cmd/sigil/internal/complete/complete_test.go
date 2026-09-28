package complete_test

import (
	"testing"

	"github.com/spf13/cobra"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/complete"
)

func TestSigilFiles(t *testing.T) {
	got, directive := complete.SigilFiles(nil, nil, "")
	if len(got) != 1 || got[0] != "sigil" || directive != cobra.ShellCompDirectiveFilterFileExt {
		t.Errorf("SigilFiles() = %v, %v", got, directive)
	}
	upTo := complete.SigilFilesUpTo(1)
	if got, directive := upTo(nil, nil, ""); len(got) != 1 || directive != cobra.ShellCompDirectiveFilterFileExt {
		t.Errorf("SigilFilesUpTo(1) with no args = %v, %v", got, directive)
	}
	if got, directive := upTo(nil, []string{"one"}, ""); len(got) != 0 || directive != cobra.ShellCompDirectiveNoFileComp {
		t.Errorf("SigilFilesUpTo(1) with one arg = %v, %v", got, directive)
	}
}
