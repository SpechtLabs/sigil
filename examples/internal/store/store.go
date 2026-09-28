// Package store holds the policies deploygate serves and reloads them in
// place. A compiled policy is immutable, so a reload compiles the whole new
// bundle aside and swaps one pointer; in-flight evaluations finish on the
// bundle they started with, and a bundle that doesn't compile never replaces
// the one that serves.
package store

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"sync/atomic"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/policies"
)

// SourceEmbedded is the source a snapshot reports for the team bundle built
// into the binary.
const SourceEmbedded = "embedded"

// GuardrailsPolicy is the platform policy every team's root has to invoke
// unconditionally, read from the platform's own source.
const GuardrailsPolicy = "deploy.guardrails"

// RootSuffix makes a team's root policy name: team t evaluates t.production.
const RootSuffix = ".production"

// What started a load, as the reload span and log record it.
const (
	TriggerStartup = "startup"
	TriggerManual  = "manual"
	TriggerSignal  = "sighup"
	TriggerPoll    = "poll"
)

// Store serves the current policy bundle and replaces it on Load. Its
// configuration is fixed by New; the only thing that changes afterwards is
// the snapshot pointer, and loads are serialized by the guard.
type Store struct {
	kind       *policy.Kind[deploy.Input]
	teams      []string
	teamsFS    fs.FS
	source     string
	platformFS fs.FS

	current atomic.Pointer[Snapshot]
	guard   *loadGuard

	metrics *telemetry.Metrics
	tracer  trace.Tracer
	clock   Clock
}

// loadGuard serializes loads, so a SIGHUP, a poll and an API call never
// compile at the same time, and remembers the fingerprint of the bundle the
// last load read.
type loadGuard struct {
	mu          sync.Mutex
	fingerprint string
}

// New creates a store for kind. Without options it serves no team, reads the
// team bundle embedded in the binary and the embedded platform documents.
// Nothing is loaded until Load succeeds.
func New(kind *policy.Kind[deploy.Input], opts ...Option) *Store {
	s := &Store{
		kind:       kind,
		teamsFS:    policies.Teams,
		source:     SourceEmbedded,
		platformFS: policies.Platform,
		clock:      WallClock{},
		guard:      &loadGuard{},
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.metrics == nil {
		s.metrics = telemetry.NewMetrics()
	}
	if s.tracer == nil {
		s.tracer = otel.Tracer(telemetry.TracerName)
	}
	return s
}

// InitialLoad is the first Load of a process. It behaves the same, but
// doesn't log a failure: the caller is about to exit with the error, and
// printing the diagnostics twice would only make them harder to read.
func (s *Store) InitialLoad(ctx context.Context) humane.Error {
	return s.load(ctx, TriggerStartup)
}

// Load compiles every team's root policy from the team bundle and, when all
// of them compile, makes them the snapshot that serves. When any fails, the
// previous snapshot keeps serving and the error carries the compiler's
// diagnostics.
func (s *Store) Load(ctx context.Context) humane.Error {
	return s.load(ctx, TriggerManual)
}

// Snapshot returns the bundle that serves right now, and false before the
// first successful Load. A snapshot never changes, so a request that took one
// sees the same policies from start to end.
func (s *Store) Snapshot() (*Snapshot, bool) {
	snap := s.current.Load()
	return snap, snap != nil
}

// Policy returns team's compiled root policy from the current snapshot.
func (s *Store) Policy(team string) (*policy.Policy[deploy.Input], bool) {
	snap, ok := s.Snapshot()
	if !ok {
		return nil, false
	}
	return snap.Policy(team)
}

// Source names where the team bundle is read from.
func (s *Store) Source() string {
	return s.source
}

// Watch reloads the bundle on every signal from sighup, and every interval
// when the team bundle's content changed since the last load. It returns when
// ctx is done. A zero interval disables polling and a nil sighup disables
// signals. Load logs and counts a failed reload itself, and the previous
// bundle keeps serving.
func (s *Store) Watch(ctx context.Context, interval time.Duration, sighup <-chan os.Signal) {
	var tick <-chan time.Time
	if interval > 0 {
		ticks, stop := s.clock.Tick(interval)
		defer stop()
		tick = ticks
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-sighup:
			_ = s.load(ctx, TriggerSignal)
		case <-tick:
			s.reloadIfChanged(ctx)
		}
	}
}

// load is Load with the trigger recorded on the span and the log line.
func (s *Store) load(ctx context.Context, trigger string) humane.Error {
	s.guard.mu.Lock()
	defer s.guard.mu.Unlock()

	ctx, span := s.tracer.Start(ctx, "deploygate.policies.reload", trace.WithAttributes(
		attribute.String("sigil.source", s.source),
		attribute.String("sigil.kind", s.kind.Name()),
		attribute.String("deploygate.reload.trigger", trigger),
	))
	defer span.End()

	// Taken before compiling, so a change made while the compile runs shows
	// up as a new fingerprint at the next poll rather than getting lost.
	fingerprint, herr := Fingerprint(s.teamsFS)
	s.guard.fingerprint = fingerprintOrError(fingerprint, herr)
	if herr != nil {
		return s.fail(ctx, span, trigger, humane.Wrap(herr, "reading the team policies from "+s.source+" failed",
			"check that the policies directory exists and is readable"))
	}

	snap, herr := s.compile()
	if herr != nil {
		return s.fail(ctx, span, trigger, herr)
	}
	s.current.Store(snap)

	names := snap.PolicyNames()
	span.SetAttributes(attribute.StringSlice("sigil.policies", names))
	span.SetStatus(codes.Ok, "")
	s.metrics.ObserveReloadSuccess(snap.LoadedAt, snap.Source, snap.teamPolicies())
	telemetry.FromContext(ctx).InfoContext(ctx, "policy bundle loaded",
		zap.String("source", snap.Source),
		zap.String("trigger", trigger),
		zap.Strings("policies", names),
	)
	return nil
}

// compile loads every team's root and builds the snapshot, or returns the
// first team's failure.
func (s *Store) compile() (*Snapshot, humane.Error) {
	if len(s.teams) == 0 {
		return nil, humane.New("no team is configured, so there is no policy to load",
			"set --team or DEPLOYGATE_TEAMS")
	}

	loaded := make(map[string]*policy.Policy[deploy.Input], len(s.teams))
	for _, team := range s.teams {
		root := team + RootSuffix
		p, err := s.kind.Load(s.teamsFS, root, policy.Require(GuardrailsPolicy, policy.From(s.platformFS)))
		if err != nil {
			fallback := ", and there is no earlier bundle to fall back to"
			if _, ok := s.Snapshot(); ok {
				fallback = ", so the previous bundle keeps serving"
			}
			// The cause is the *policy.CompileError itself, so a caller can
			// still reach its diagnostics with errors.As.
			return nil, humane.Wrap(err,
				fmt.Sprintf("the team policies from %s don't load: %s failed to compile%s",
					s.source, root, fallback),
				"fix the diagnostics in the team policies and reload",
				"run `sigilc check` on the policies directory to see the same diagnostics before deploying",
			)
		}
		loaded[team] = p
	}

	return newSnapshot(s.kind.Name(), kindVersion(s.kind.Schema()), s.source, s.teams, loaded, s.clock.Now()), nil
}

// fail records a rejected load on the span and the metrics, and logs it,
// except at startup, where the caller reports it.
func (s *Store) fail(ctx context.Context, span trace.Span, trigger string, err humane.Error) humane.Error {
	span.RecordError(err)
	span.SetStatus(codes.Error, "policy bundle rejected")
	s.metrics.ObserveReloadFailure()
	if trigger == TriggerStartup {
		return err
	}

	msg := "policy bundle rejected, nothing is loaded yet"
	if _, ok := s.Snapshot(); ok {
		msg = "policy bundle rejected, the previous bundle keeps serving"
	}
	telemetry.FromContext(ctx).ErrorContext(ctx, msg,
		zap.String("source", s.source),
		zap.String("trigger", trigger),
		zap.Error(err),
		zap.NamedError("cause", err.Cause()),
		zap.Strings("advice", err.Advice()),
	)
	return err
}

// reloadIfChanged loads the bundle when its fingerprint moved since the last
// load, successful or not, so a broken bundle is reported once rather than at
// every poll. A directory that can't be read counts as a change too, and is
// reported by the load; as long as it stays unreadable in the same way, the
// fingerprint stays the same and nothing is reported again.
func (s *Store) reloadIfChanged(ctx context.Context) {
	if s.guard.changed(fingerprintOrError(Fingerprint(s.teamsFS))) {
		_ = s.load(ctx, TriggerPoll)
	}
}

// fingerprintOrError turns a failed fingerprint into one that names the
// failure, so an unreadable directory compares equal to itself and unequal to
// any readable content.
func fingerprintOrError(fingerprint string, herr humane.Error) string {
	if herr != nil {
		return "unreadable: " + herr.Error()
	}
	return fingerprint
}

// changed reports whether fingerprint differs from the last load's.
func (g *loadGuard) changed(fingerprint string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return fingerprint != g.fingerprint
}
