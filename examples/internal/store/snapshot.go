package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
)

// Snapshot is one loaded bundle: the compiled root policy of every team, and
// when and from where it was loaded. It is immutable once built.
type Snapshot struct {
	// Kind is the name of the kind every policy is checked against.
	Kind string
	// KindVersion is the kind's contract version.
	KindVersion int
	// Source is where the team bundle was read from: SourceEmbedded or a
	// directory.
	Source string
	// LoadedAt is when the bundle finished compiling.
	LoadedAt time.Time
	// Teams lists each served team and its root policy, in the configured
	// order.
	Teams []TeamPolicy

	policies map[string]*policy.Policy[deploy.Input]
}

// TeamPolicy is one served team and the name of the policy it evaluates.
type TeamPolicy struct {
	Team   string
	Policy string
}

// Policy returns team's compiled root policy, and false when the team isn't
// served.
func (s *Snapshot) Policy(team string) (*policy.Policy[deploy.Input], bool) {
	p, ok := s.policies[team]
	return p, ok
}

// PolicyNames lists the served policies' names in the configured order.
func (s *Snapshot) PolicyNames() []string {
	names := make([]string, 0, len(s.Teams))
	for _, tp := range s.Teams {
		names = append(names, tp.Policy)
	}
	return names
}

// TeamNames lists the served teams in the configured order.
func (s *Snapshot) TeamNames() []string {
	names := make([]string, 0, len(s.Teams))
	for _, tp := range s.Teams {
		names = append(names, tp.Team)
	}
	return names
}

// newSnapshot assembles a snapshot from compiled policies.
func newSnapshot(kindName string, kindVer int, source string, teams []string, loaded map[string]*policy.Policy[deploy.Input], at time.Time) *Snapshot {
	entries := make([]TeamPolicy, 0, len(teams))
	for _, team := range teams {
		entries = append(entries, TeamPolicy{Team: team, Policy: loaded[team].Name()})
	}
	return &Snapshot{
		Kind:        kindName,
		KindVersion: kindVer,
		Source:      source,
		LoadedAt:    at,
		Teams:       entries,
		policies:    loaded,
	}
}

// teamPolicies maps each team to its policy name, the shape the loaded-policy
// gauge takes.
func (s *Snapshot) teamPolicies() map[string]string {
	out := make(map[string]string, len(s.Teams))
	for _, tp := range s.Teams {
		out[tp.Team] = tp.Policy
	}
	return out
}

// kindVersion reads the contract version from a kind file, the kind's
// Schema, whose first line is `kind <Name> version <N>`. policy.Kind has no
// accessor for it, and the kind file is the public, stable rendering of the
// contract, so it is the right place to read it from.
func kindVersion(schema string) int {
	header, _, _ := strings.Cut(schema, "\n")
	var (
		name    string
		version int
	)
	if _, err := fmt.Sscanf(header, "kind %s version %d", &name, &version); err != nil {
		return 0
	}
	return version
}
