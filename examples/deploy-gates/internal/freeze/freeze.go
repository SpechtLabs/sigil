// Package freeze resolves the change freeze deploygate puts into every
// deploy input. A freeze is a fact about the world, like the time of day, so
// the host looks it up and the policy reads it: the client never sends one,
// and the server overwrites [deploy.Input.Freeze] after decoding a request.
// What a freeze means is the platform's vocabulary, deploy.freeze's
// is_frozen, and what happens to a frozen deploy is the rule in
// deploy.guardrails. The value the flag system holds is data in the input,
// never part of a policy, so flipping it doesn't reload anything, and a
// logged input replays to the same decision.
//
// A [Source] answers which environments are frozen right now, without
// blocking. [Static] is a fixed list from the configuration. [OFREP] asks a
// feature-flag service over the OpenFeature Remote Evaluation Protocol in the
// background and answers from what it last heard, until that is older than
// its maximum staleness: from then on the freeze is [deploy.Freeze.Unknown],
// and the policy treats every environment as frozen.
package freeze

import (
	"slices"
	"strings"
	"time"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
)

// Source tells which environments are frozen. Freeze is called once per
// deploy request, so it answers from memory and never waits on the network.
type Source interface {
	// Freeze returns the freeze in force now. Its Environments are never
	// nil, and the caller may keep or change them.
	Freeze() deploy.Freeze
}

// Clock is where the OFREP source reads time from: how old its last answer
// is, and when to refresh. Tests pass their own to control both. It has the
// same methods as the store's clock, so one fake serves both.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// Tick returns a channel that receives every interval, and a function
	// that stops it.
	Tick(interval time.Duration) (<-chan time.Time, func())
}

// Static is a freeze set by configuration: the same environments for as long
// as the process runs. It is what deploygate uses when no flag service is
// configured, frozen nowhere by default.
type Static struct {
	environments []string
}

// WallClock is the real clock, the OFREP source's default and the one place
// this package reads real time.
type WallClock struct{}

// NewStatic returns a source that freezes environments, with blanks dropped,
// duplicates removed and the rest sorted. Without any, nothing is frozen.
func NewStatic(environments ...string) Static {
	return Static{environments: normalize(environments)}
}

// Freeze returns the configured environments, never unknown.
func (s Static) Freeze() deploy.Freeze {
	return deploy.Freeze{Environments: slices.Clone(s.environments)}
}

// Now returns time.Now.
func (WallClock) Now() time.Time {
	return time.Now() //nolint:clockinterface // this is the Clock implementation the rule asks for
}

// Tick returns a time.Ticker's channel and its Stop.
func (WallClock) Tick(interval time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(interval) //nolint:clockinterface // this is the Clock implementation the rule asks for
	return t.C, t.Stop
}

// normalize trims every environment name, drops the blank ones and
// duplicates, and sorts the rest, so two sources that freeze the same
// environments give the same input. The result is never nil: an input logged
// with `"environments": []` replays exactly.
func normalize(environments []string) []string {
	out := make([]string, 0, len(environments))
	for _, env := range environments {
		env = strings.TrimSpace(env)
		if env != "" {
			out = append(out, env)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}
