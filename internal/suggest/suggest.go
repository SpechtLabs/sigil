// Package suggest finds the name an author most likely meant, for the
// "did you mean" hint on unknown fields, types and names.
package suggest

import "strings"

// Closest returns the candidate nearest to name when it's near enough to
// be a plausible typo, and false otherwise. Distance is Damerau-Levenshtein
// on the lowercased strings, so a transposition (`teir`) and a case slip
// (`release` for `Release`) each count one; the limit is a third of the
// name's length, and at least one. Ties go to the earlier candidate.
func Closest(name string, candidates []string) (string, bool) {
	limit := max(1, len(name)/3)
	best, bestDist := "", limit+1
	lower := strings.ToLower(name)
	for _, c := range candidates {
		if c == name {
			return "", false // it exists; whatever is wrong, it isn't the spelling
		}
		if d := distance(lower, strings.ToLower(c)); d < bestDist {
			best, bestDist = c, d
		}
	}
	return best, bestDist <= limit
}

// distance is the optimal string alignment distance between a and b:
// insertions, deletions, substitutions and adjacent transpositions each
// cost one.
func distance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev2 := make([]int, len(rb)+1)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && ra[i-1] == rb[j-2] && ra[i-2] == rb[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(rb)]
}
