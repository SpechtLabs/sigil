package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
)

// Snapshot is one loaded bundle: the compiled policy of every root, and when,
// from where and from which content it was loaded. It is immutable once
// built.
type Snapshot[In any] struct {
	// Kind is the name of the kind every policy is checked against.
	Kind string
	// KindVersion is the kind's contract version.
	KindVersion int
	// Source is where the bundle was read from: SourceEmbedded or a
	// directory.
	Source string
	// Fingerprint is the [Fingerprint] of the bundle's content the snapshot
	// was compiled from, so an operator can tell which revision of a mounted
	// directory serves.
	Fingerprint string
	// LoadedAt is when the bundle finished compiling.
	LoadedAt time.Time
	// Roots lists each root in the configured order.
	Roots []Root

	policies map[string]*policy.Policy[In]
}

// Root is one policy a store compiles and serves, looked up by its team.
type Root struct {
	// Team is the team the root serves.
	Team string
	// Policy is the root policy's name, such as checkout.alerts.
	Policy string
}

// Policy returns the compiled root policy of team, and false when the
// snapshot serves no such team.
func (s *Snapshot[In]) Policy(team string) (*policy.Policy[In], bool) {
	p, ok := s.policies[team]
	return p, ok
}

// PolicyNames lists the served policies' names in the configured order.
func (s *Snapshot[In]) PolicyNames() []string {
	names := make([]string, 0, len(s.Roots))
	for _, r := range s.Roots {
		names = append(names, r.Policy)
	}
	return names
}

// TeamNames lists the served teams in the configured order.
func (s *Snapshot[In]) TeamNames() []string {
	names := make([]string, 0, len(s.Roots))
	for _, r := range s.Roots {
		names = append(names, r.Team)
	}
	return names
}

// loaded lists the roots the way the loaded-policy gauge takes them.
func (s *Snapshot[In]) loaded() []telemetry.LoadedPolicy {
	out := make([]telemetry.LoadedPolicy, 0, len(s.Roots))
	for _, r := range s.Roots {
		out = append(out, telemetry.LoadedPolicy{Team: r.Team, Policy: r.Policy})
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
