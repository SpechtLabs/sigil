// Package store holds the policies deploygate serves and reloads them in
// place. A compiled policy is immutable, so a reload compiles the whole new
// bundle aside and swaps one pointer; in-flight evaluations finish on the
// bundle they started with, and a bundle that doesn't compile never replaces
// the one that serves.
//
// A Store is generic over a kind's input type. deploygate runs two: one for
// the DeployApproval team policies, one root per team, and one for the
// AccessGrant bundle, with the single root access.main.
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

	"github.com/spechtlabs/sigil/examples/internal/access"
	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/policies"
)

// SourceEmbedded is the source a snapshot reports for a bundle built into the
// binary.
const SourceEmbedded = "embedded"

// The platform policies each kind's roots have to invoke unconditionally,
// read from the platform's own source.
const (
	DeployGuardrails = "deploy.guardrails"
	AccessGuardrails = "access.guardrails"
)

// AccessRoot is the AccessGrant policy deploygate evaluates.
const AccessRoot = "access.main"

// RootSuffix makes a team's root policy name: team t evaluates t.production.
const RootSuffix = ".production"

// What started a load, as the reload span and log record it.
const (
	TriggerStartup = "startup"
	TriggerManual  = "manual"
	TriggerSignal  = "sighup"
	TriggerPoll    = "poll"
)

// Store serves the current policy bundle of one kind and replaces it on
// Load. Its configuration is fixed by New; the only thing that changes
// afterwards is the snapshot pointer, and loads are serialized by the guard.
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

// New creates a store for kind. It has no bundle, root or required policy of
// its own; WithBundle, WithTeams or WithRoots, and WithRequired set them.
// NewDeploy and NewAccess set them for deploygate's two kinds. Nothing is
// loaded until Load succeeds.
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

// NewDeploy creates the store for the DeployApproval team policies: the team
// bundle embedded in the binary, one root per team from WithTeams, and
// deploy.guardrails required from the embedded platform documents. opts come
// after those defaults, so WithBundleDir points it at a mounted directory.
func NewDeploy(opts ...Option) *Store[deploy.Input] {
	all := make([]Option, 0, 2+len(opts))
	all = append(all,
		WithBundle(policies.Teams, SourceEmbedded),
		WithRequired(DeployGuardrails, policies.PlatformDeploy),
	)
	return New(deploy.Kind, append(all, opts...)...)
}

// NewAccess creates the store for the AccessGrant bundle: the access bundle
// embedded in the binary, the single root access.main, and
// access.guardrails required from the embedded platform documents.
func NewAccess(opts ...Option) *Store[access.Input] {
	all := make([]Option, 0, 3+len(opts))
	all = append(all,
		WithBundle(policies.Access, SourceEmbedded),
		WithRoots(AccessRoot),
		WithRequired(AccessGuardrails, policies.PlatformAccess),
	)
	return New(access.Kind, append(all, opts...)...)
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
// *policy.CompileError with its diagnostics.
func (s *Store[In]) Load(ctx context.Context) humane.Error {
	return s.load(ctx, TriggerManual)
}

// Snapshot returns the bundle that serves right now, and false before the
// first successful Load. A snapshot never changes, so a request that took one
// sees the same policies from start to end.
func (s *Store[In]) Snapshot() (*Snapshot[In], bool) {
	snap := s.current.Load()
	return snap, snap != nil
}

// Policy returns the compiled root policy for key, a team or a fixed root's
// name, from the current snapshot.
func (s *Store[In]) Policy(key string) (*policy.Policy[In], bool) {
	snap, ok := s.Snapshot()
	if !ok {
		return nil, false
	}
	return snap.Policy(key)
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
	s.cfg.metrics.PrepareReloads(s.kind.Name())

	ctx, span := s.cfg.tracer.Start(ctx, "deploygate.policies.reload", trace.WithAttributes(
		attribute.String("sigil.source", s.cfg.source),
		attribute.String("sigil.kind", s.kind.Name()),
		attribute.String("deploygate.reload.trigger", trigger),
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

	snap, herr := s.compile()
	if herr != nil {
		return s.fail(ctx, span, trigger, herr)
	}
	s.current.Store(snap)

	names := snap.PolicyNames()
	span.SetAttributes(attribute.StringSlice("sigil.policies", names))
	span.SetStatus(codes.Ok, "")
	s.cfg.metrics.ObserveReloadSuccess(snap.Kind, snap.LoadedAt, snap.Source, snap.loaded())
	telemetry.FromContext(ctx).InfoContext(ctx, "policy bundle loaded",
		zap.String("kind", snap.Kind),
		zap.String("source", snap.Source),
		zap.String("trigger", trigger),
		zap.Strings("policies", names),
	)
	return nil
}

// compile loads every root and builds the snapshot, or returns the first
// root's failure.
func (s *Store[In]) compile() (*Snapshot[In], humane.Error) {
	if len(s.cfg.roots) == 0 {
		return nil, humane.New("no "+s.kind.Name()+" policy is configured, so there is nothing to load",
			"set --team or DEPLOYGATE_TEAMS for the team policies")
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
		loaded[root.key()] = p
	}

	return &Snapshot[In]{
		Kind:        s.kind.Name(),
		KindVersion: kindVersion(s.kind.Schema()),
		Source:      s.cfg.source,
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
	s.cfg.metrics.ObserveReloadFailure(s.kind.Name())
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
