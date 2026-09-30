package project

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/sierrasoftworks/humane-errors-go"
)

// Match returns the policies matching any of the patterns, in the order
// given. A pattern is a name, or a name with `*` matching any run of
// characters, dots included. Every pattern must match something.
func Match(policies, patterns []string) ([]string, humane.Error) {
	seen := map[string]bool{}
	var out []string
	for _, pattern := range patterns {
		matched := matching(policies, pattern)
		if len(matched) == 0 {
			help := "the bundle defines no policies"
			if len(policies) > 0 {
				help = "the bundle defines: " + strings.Join(policies, ", ")
			}
			return nil, humane.New(fmt.Sprintf("no policy matches %q", pattern), help)
		}
		for _, p := range matched {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	return out, nil
}

// Root picks the one policy eval evaluates: the one named, or the
// bundle's only policy.
func Root(policies []string, name string) (string, humane.Error) {
	if name != "" {
		roots, err := Match(policies, []string{name})
		if err != nil {
			return "", err
		}
		if len(roots) > 1 {
			return "", humane.New(fmt.Sprintf("%q matches %d policies", name, len(roots)), "name one policy: "+strings.Join(roots, ", "))
		}
		return roots[0], nil
	}
	switch len(policies) {
	case 0:
		return "", humane.New("the bundle holds no policies", "name a file or directory that holds one")
	case 1:
		return policies[0], nil
	}
	return "", humane.New("the bundle holds several policies", "name the one to evaluate with --policy: "+strings.Join(policies, ", "))
}

// matching returns the policies one pattern matches.
func matching(policies []string, pattern string) []string {
	re := regexp.MustCompile("^" + strings.ReplaceAll(regexp.QuoteMeta(pattern), `\*`, ".*") + "$")
	var out []string
	for _, p := range policies {
		if re.MatchString(p) {
			out = append(out, p)
		}
	}
	return out
}
