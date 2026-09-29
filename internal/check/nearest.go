package check

import "github.com/spechtlabs/sigil/internal/diag"

// nearest finds the name an author most likely meant, for the "did you
// mean" hint on unknown fields, types and names, by the rule
// [diag.Nearest] applies everywhere.
func nearest(name string, candidates []string) (string, bool) {
	return diag.Nearest(name, candidates)
}
