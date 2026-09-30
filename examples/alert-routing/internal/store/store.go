package store

import (
	"context"
	"fmt"
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

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/alert-routing/policies"
)

// SourceEmbedded is the source a snapshot reports for a bundle built into the
// binary.
const SourceEmbedded = "embedded"

// RequiredPolicy is the platform policy every team's root has to invoke
// unconditionally, read from the platform's own source: critical production
// alerts page the team's on-call, and no team can switch that off.
const RequiredPolicy = "platform.paging"

// RootSuffix makes a team's root policy name: team t evaluates t.alerts.
const RootSuffix = ".alerts"

// What started a load, as the load span and log record it.
const (
	TriggerStartup = "startup"
	TriggerManual  = "manual"
	TriggerSignal  = "sighup"
	TriggerPoll    = "poll"
)

// Store serves the current policy bundle of one kind and replaces it on
// Load. Its configuration is fixed by New; the only thing that changes
// afterwards is the snapshot pointer, and loads are serialized by the guard.
// A Store is safe for concurrent use, and reading the snapshot never waits
// for a load.
type Store[In any] struct {
	kind *policy.Kind[In]
	cfg  config

	current atomic.Pointer[Snapshot[In]]
	guard   *loadGuard
}

// loadGuard serializes loads, so a SIGHUP, a poll and an API call never
// compile at the same time, and remembers the fingerprint of the bundle the
// last load read.
type loadGuard struct {
	mu          sync.Mutex
	fingerprint string
}

// New creates a store for kind. It has no bundle, roots or required policy of
// its own; WithBundle, WithTeams and WithRequired set them, and NewRouting
// sets them for alertrouter's kind. Without WithMetrics the store reports on
// a private set of metrics, and without WithTracer it uses the global tracer
// provider. Nothing is loaded until Load succeeds.
func New[In any](kind *policy.Kind[In], opts ...Option) *Store[In] {
	cfg := config{source: SourceEmbedded, clock: WallClock{}}
	for _, opt := range opts {
		opt(&cfg)
	}
	if cfg.metrics == nil {
		cfg.metrics = telemetry.NewMetrics()
	}
	if cfg.tracer == nil {
		cfg.tracer = otel.Tracer(telemetry.TracerName)
	}
	return &Store[In]{kind: kind, cfg: cfg, guard: &loadGuard{}}
}

// NewRouting creates the store for the AlertRouting team policies: the team
// bundle embedded in the binary, and platform.paging required from the
// embedded platform documents. It has no roots until WithTeams sets one per
// team in the directory. opts come after those defaults, so WithBundleDir
// points it at a mounted directory.
func NewRouting(opts ...Option) *Store[routing.Input] {
	all := make([]Option, 0, 2+len(opts))
	all = append(all,
		WithBundle(policies.Teams, SourceEmbedded),
		WithRequired(RequiredPolicy, policies.Platform),
	)
	return New(routing.Kind, append(all, opts...)...)
}

// InitialLoad is the first Load of a process. It behaves the same, but
// doesn't log a failure: the caller is about to exit with the error, and
// printing the diagnostics twice would only make them harder to read.
func (s *Store[In]) InitialLoad(ctx context.Context) humane.Error {
	return s.load(ctx, TriggerStartup)
}

// Load compiles every root policy from the bundle and, when all of them
// compile, makes them the snapshot that serves. When any fails, the previous
// snapshot keeps serving and the error's cause is the compiler's
// [*policy.CompileError] with its diagnostics. It records the trigger
// manual; the reload endpoint calls it.
func (s *Store[In]) Load(ctx context.Context) humane.Error {
	return s.load(ctx, TriggerManual)
}

// Snapshot returns the bundle that serves right now, and false before the
// first successful Load. A snapshot never changes, so a request that took one
// sees the same policies from start to end, even across a webhook batch.
func (s *Store[In]) Snapshot() (*Snapshot[In], bool) {
	snap := s.current.Load()
	return snap, snap != nil
}

// Policy returns the compiled root policy of team from the current snapshot.
// It returns false before the first successful Load and for a team the
// snapshot doesn't serve.
func (s *Store[In]) Policy(team string) (*policy.Policy[In], bool) {
	snap, ok := s.Snapshot()
	if !ok {
		return nil, false
	}
	return snap.Policy(team)
}

// Kind names the kind the store's policies implement.
func (s *Store[In]) Kind() string {
	return s.kind.Name()
}

// Source names where the bundle is read from.
func (s *Store[In]) Source() string {
	return s.cfg.source
}

// Watch reloads the bundle on every signal from sighup, and every interval
// when the bundle's content changed since the last load. It returns when ctx
// is done. A zero interval disables polling and a nil sighup disables
// signals. Load logs and counts a failed reload itself, and the previous
// bundle keeps serving.
func (s *Store[In]) Watch(ctx context.Context, interval time.Duration, sighup <-chan os.Signal) {
	var tick <-chan time.Time
	if interval > 0 {
		ticks, stop := s.cfg.clock.Tick(interval)
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
func (s *Store[In]) load(ctx context.Context, trigger string) humane.Error {
	s.guard.mu.Lock()
	defer s.guard.mu.Unlock()
	s.cfg.metrics.PrepareReloads()

	ctx, span := s.cfg.tracer.Start(ctx, "alertrouter.policy.load", trace.WithAttributes(
		attribute.String("sigil.source", s.cfg.source),
		attribute.String("sigil.kind", s.kind.Name()),
		attribute.String("alertrouter.reload.trigger", trigger),
	))
	defer span.End()

	if s.cfg.bundle == nil {
		return s.fail(ctx, span, trigger, humane.New("no bundle is configured for "+s.kind.Name(),
			"pass store.WithBundle or store.WithBundleDir to store.New"))
	}

	// Taken before compiling, so a change made while the compile runs shows
	// up as a new fingerprint at the next poll rather than getting lost.
	fingerprint, herr := Fingerprint(s.cfg.bundle)
	s.guard.fingerprint = fingerprintOrError(fingerprint, herr)
	if herr != nil {
		return s.fail(ctx, span, trigger, humane.Wrap(herr, "reading the "+s.kind.Name()+" policies from "+s.cfg.source+" failed",
			"check that the policies directory exists and is readable"))
	}

	snap, herr := s.compile(fingerprint)
	if herr != nil {
		return s.fail(ctx, span, trigger, herr)
	}
	s.current.Store(snap)

	names := snap.PolicyNames()
	span.SetAttributes(
		attribute.StringSlice("sigil.policies", names),
		attribute.String("sigil.fingerprint", fingerprint),
	)
	span.SetStatus(codes.Ok, "")
	s.cfg.metrics.ObserveReloadSuccess(snap.LoadedAt, snap.Source, snap.Fingerprint, snap.loaded())
	telemetry.FromContext(ctx).InfoContext(ctx, "policy bundle loaded",
		zap.String("kind", snap.Kind),
		zap.String("source", snap.Source),
		zap.String("fingerprint", snap.Fingerprint),
		zap.String("trigger", trigger),
		zap.Strings("policies", names),
	)
	return nil
}

// compile loads every root and builds the snapshot, or returns the first
// root's failure.
func (s *Store[In]) compile(fingerprint string) (*Snapshot[In], humane.Error) {
	if len(s.cfg.roots) == 0 {
		return nil, humane.New("no "+s.kind.Name()+" policy is configured, so there is nothing to load",
			"list at least one team in the team directory, --teams-file or ALERTROUTER_TEAMS_FILE")
	}
	var opts []policy.LoadOption
	if s.cfg.required != "" {
		opts = append(opts, policy.Require(s.cfg.required, policy.From(s.cfg.trusted)))
	}

	loaded := make(map[string]*policy.Policy[In], len(s.cfg.roots))
	for _, root := range s.cfg.roots {
		p, err := s.kind.Load(s.cfg.bundle, root.Policy, opts...)
		if err != nil {
			fallback := ", and there is no earlier bundle to fall back to"
			if _, ok := s.Snapshot(); ok {
				fallback = ", so the previous bundle keeps serving"
			}
			// The cause is the *policy.CompileError itself, so a caller can
			// still reach its diagnostics with errors.As.
			return nil, humane.Wrap(err,
				fmt.Sprintf("the %s policies from %s don't load: %s failed to compile%s",
					s.kind.Name(), s.cfg.source, root.Policy, fallback),
				"fix the diagnostics in the policies and reload",
				"run `sigilc check` on the policies directory to see the same diagnostics before deploying",
			)
		}
		loaded[root.Team] = p
	}

	return &Snapshot[In]{
		Kind:        s.kind.Name(),
		KindVersion: kindVersion(s.kind.Schema()),
		Source:      s.cfg.source,
		Fingerprint: fingerprint,
		LoadedAt:    s.cfg.clock.Now(),
		Roots:       append([]Root(nil), s.cfg.roots...),
		policies:    loaded,
	}, nil
}

// fail records a rejected load on the span and the metrics, and logs it,
// except at startup, where the caller reports it.
func (s *Store[In]) fail(ctx context.Context, span trace.Span, trigger string, err humane.Error) humane.Error {
	span.RecordError(err)
	span.SetStatus(codes.Error, "policy bundle rejected")
	s.cfg.metrics.ObserveReloadFailure()
	if trigger == TriggerStartup {
		return err
	}

	msg := "policy bundle rejected, nothing is loaded yet"
	if _, ok := s.Snapshot(); ok {
		msg = "policy bundle rejected, the previous bundle keeps serving"
	}
	telemetry.FromContext(ctx).ErrorContext(ctx, msg,
		zap.String("kind", s.kind.Name()),
		zap.String("source", s.cfg.source),
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
func (s *Store[In]) reloadIfChanged(ctx context.Context) {
	if s.cfg.bundle == nil {
		return
	}
	if s.guard.changed(fingerprintOrError(Fingerprint(s.cfg.bundle))) {
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
