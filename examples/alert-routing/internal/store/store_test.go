package store_test

import (
	"context"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/alert-routing/internal/routing"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/store"
	"github.com/spechtlabs/sigil/examples/alert-routing/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/alert-routing/policies"
)

// brokenPolicy doesn't compile: it invokes a policy nothing defines.
const brokenPolicy = `policy checkout.alerts: AlertRouting@1

use platform.paging

paging()
nonexistent()
`

// unpagedPolicy compiles on its own but skips the required platform.paging.
const unpagedPolicy = `policy checkout.alerts: AlertRouting@1

use platform.routing

routing()
`

// gatedPaging invokes platform.paging only for some alerts, which the Require
// refuses: the paging has to hold for every alert.
const gatedPaging = `policy checkout.alerts: AlertRouting@1

use platform.paging

when alert.labels["tier"] == "gold" {
  paging()
}
`

func TestLoad(t *testing.T) {
	tests := []struct {
		name      string
		teams     []string
		files     map[string]string // overrides on top of the embedded team files
		wantErr   string
		wantNames []string
	}{
		{
			name:      "embedded teams",
			teams:     []string{"checkout", "payments"},
			wantNames: []string{"checkout.alerts", "payments.alerts"},
		},
		{
			name:      "configured order is kept",
			teams:     []string{"payments", "checkout"},
			wantNames: []string{"payments.alerts", "checkout.alerts"},
		},
		{
			name:    "no team",
			wantErr: "no AlertRouting policy is configured",
		},
		{
			name:    "a team without a policy",
			teams:   []string{"checkout", "billing"},
			wantErr: "billing.alerts failed to compile",
		},
		{
			name:    "broken policy",
			teams:   []string{"checkout"},
			files:   map[string]string{"checkout/alerts.sigil": brokenPolicy},
			wantErr: "nonexistent",
		},
		{
			name:    "paging skipped",
			teams:   []string{"checkout"},
			files:   map[string]string{"checkout/alerts.sigil": unpagedPolicy},
			wantErr: "platform.paging",
		},
		{
			name:    "paging behind a condition",
			teams:   []string{"checkout"},
			files:   map[string]string{"checkout/alerts.sigil": gatedPaging},
			wantErr: "platform.paging",
		},
		{
			name:    "a team bundle redefining the platform's paging",
			teams:   []string{"checkout"},
			files:   map[string]string{"platform/paging.sigil": "policy platform.paging: AlertRouting@1\n"},
			wantErr: "platform.paging",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := teamsDir(t, tt.files)
			m := telemetry.NewMetrics()
			at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			st := store.NewRouting(
				store.WithTeams(tt.teams...),
				store.WithBundleDir(dir),
				store.WithMetrics(m),
				store.WithClock(&fakeClock{now: at}),
			)

			err := st.Load(context.Background())
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Load succeeded, want an error containing %q", tt.wantErr)
				}
				if !strings.Contains(chain(err), tt.wantErr) {
					t.Errorf("Load error = %q, want it to contain %q", chain(err), tt.wantErr)
				}
				if len(err.Advice()) == 0 {
					t.Error("Load error has no advice")
				}
				if _, ok := st.Snapshot(); ok {
					t.Error("a failed first load left a snapshot")
				}
				if got := reloads(t, m, "failure"); got != 1 {
					t.Errorf("failure reloads = %v, want 1", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %s", chain(err))
			}

			snap, ok := st.Snapshot()
			if !ok {
				t.Fatal("no snapshot after a successful load")
			}
			if got := strings.Join(snap.PolicyNames(), ","); got != strings.Join(tt.wantNames, ",") {
				t.Errorf("policies = %s, want %s", got, strings.Join(tt.wantNames, ","))
			}
			if got := strings.Join(snap.TeamNames(), ","); got != strings.Join(tt.teams, ",") {
				t.Errorf("teams = %s, want %s", got, strings.Join(tt.teams, ","))
			}
			if snap.Kind != "AlertRouting" || snap.KindVersion != 1 {
				t.Errorf("kind = %s@%d, want AlertRouting@1", snap.Kind, snap.KindVersion)
			}
			if snap.Source != dir || st.Source() != dir {
				t.Errorf("source = %q, %q, want %q", snap.Source, st.Source(), dir)
			}
			if want, _ := store.Fingerprint(os.DirFS(dir)); snap.Fingerprint != want {
				t.Errorf("fingerprint = %q, want the bundle's %q", snap.Fingerprint, want)
			}
			if !snap.LoadedAt.Equal(at) {
				t.Errorf("loaded at %v, want the clock's %v", snap.LoadedAt, at)
			}
			for _, team := range tt.teams {
				if p, ok := st.Policy(team); !ok || p.Name() != team+".alerts" {
					t.Errorf("Policy(%q) = %v, %v", team, p, ok)
				}
			}
			if _, ok := st.Policy("billing"); ok {
				t.Error("Policy(billing) found a team that isn't served")
			}
			if got := reloads(t, m, "success"); got != 1 {
				t.Errorf("success reloads = %v, want 1", got)
			}
			if _, ok := metricValue(t, m, "alertrouter_policy_loaded_info", map[string]string{
				"team": tt.teams[0], "policy": tt.wantNames[0], "fingerprint": snap.Fingerprint, "source": dir,
			}); !ok {
				t.Errorf("no loaded-policy series for %s", tt.wantNames[0])
			}
		})
	}
}

func TestLoadEmbeddedByDefault(t *testing.T) {
	st := store.NewRouting(store.WithTeams("checkout", "payments"))
	if st.Kind() != "AlertRouting" {
		t.Errorf("Kind() = %q", st.Kind())
	}
	if _, ok := st.Policy("checkout"); ok {
		t.Error("Policy before the first load found a policy")
	}
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load: %s", chain(err))
	}
	snap, _ := st.Snapshot()
	if snap.Source != store.SourceEmbedded {
		t.Errorf("source = %q, want %q", snap.Source, store.SourceEmbedded)
	}
	if snap.Fingerprint == "" {
		t.Error("the embedded bundle has no fingerprint")
	}
}

// TestEmbeddedPolicyRoutes evaluates the embedded checkout policy through the
// store, so the snapshot's policies are known to be the ones the platform
// requires: a critical production alert pages the on-call.
func TestEmbeddedPolicyRoutes(t *testing.T) {
	st := store.NewRouting(store.WithTeams("checkout"))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load: %s", chain(err))
	}
	p, _ := st.Policy("checkout")
	res, err := p.Eval(context.Background(), routing.Input{
		Alert: routing.Alert{Name: "CheckoutErrorRate", Severity: routing.Critical, Labels: map[string]string{"env": "production"}},
		Team:  routing.Team{Name: "checkout", Oncall: "checkout-primary", Channel: "#checkout-alerts"},
	})
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	page, ok := routing.Page.Match(res)
	if !ok || page.Target != "checkout-primary" || !routing.CriticalAlert.Is(res) {
		t.Errorf("result = %s(%s) %+v, want page(critical_alert) to checkout-primary", res.Decision, res.Reason, page)
	}
}

// TestReloadKeepsLastKnownGood breaks the bundle after a good load and checks
// that the old snapshot keeps serving, the error carries the diagnostics, and
// the reload gauges report the broken bundle.
func TestReloadKeepsLastKnownGood(t *testing.T) {
	dir := teamsDir(t, nil)
	m := telemetry.NewMetrics()
	clock := &fakeClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	goodAt := float64(clock.now.Unix())
	st := store.NewRouting(store.WithTeams("checkout", "payments"), store.WithBundleDir(dir), store.WithMetrics(m), store.WithClock(clock))

	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("first Load: %s", chain(err))
	}
	good, _ := st.Snapshot()
	if at, ok := reloadHealth(t, m); at != goodAt || ok != 1 {
		t.Errorf("after a good load: last reload %v, successful %v, want %v and 1", at, ok, goodAt)
	}

	clock.now = clock.now.Add(time.Hour)
	writeFile(t, dir, "checkout/alerts.sigil", brokenPolicy)
	err := st.Load(context.Background())
	if err == nil {
		t.Fatal("Load of a broken bundle succeeded")
	}
	for _, want := range []string{"checkout.alerts failed to compile", "previous bundle keeps serving", "nonexistent"} {
		if !strings.Contains(chain(err), want) {
			t.Errorf("error %q doesn't contain %q", chain(err), want)
		}
	}
	// The compiler's error stays reachable, the way the Go API documents it.
	var compileErr *policy.CompileError
	if !errors.As(err, &compileErr) || len(compileErr.Diagnostics) == 0 {
		t.Errorf("errors.As(*policy.CompileError) = %v, want the diagnostics", compileErr)
	}

	if now, _ := st.Snapshot(); now != good {
		t.Error("a failed reload replaced the snapshot")
	}
	if got := reloads(t, m, "failure"); got != 1 {
		t.Errorf("failure reloads = %v, want 1", got)
	}
	// The failure marks the bundle unhealthy and leaves the time of the
	// load that still serves, and its policies on the gauge.
	if at, ok := reloadHealth(t, m); at != goodAt || ok != 0 {
		t.Errorf("after a failed load: last reload %v, successful %v, want %v and 0", at, ok, goodAt)
	}
	if _, ok := metricValue(t, m, "alertrouter_policy_loaded_info", map[string]string{
		"team": "checkout", "policy": "checkout.alerts", "fingerprint": good.Fingerprint, "source": dir,
	}); !ok {
		t.Error("a failed reload removed the serving bundle's loaded-policy series")
	}

	// Fixing the file makes the next load succeed with a new snapshot.
	writeFile(t, dir, "checkout/alerts.sigil", "// fixed\n"+readEmbedded(t, "checkout/alerts.sigil"))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load after the fix: %s", chain(err))
	}
	fixed, _ := st.Snapshot()
	if fixed == good || fixed.Fingerprint == good.Fingerprint {
		t.Error("a successful reload of changed content kept the old snapshot or fingerprint")
	}
	if at, ok := reloadHealth(t, m); at != float64(clock.now.Unix()) || ok != 1 {
		t.Errorf("after the fix: last reload %v, successful %v, want %v and 1", at, ok, clock.now.Unix())
	}
}

func TestWatch(t *testing.T) {
	tests := []struct {
		act         func(t *testing.T, dir string, clock *fakeClock, sighup chan<- os.Signal)
		name        string
		wantReloads float64
		wantFailed  float64
	}{
		{
			name: "poll without a change doesn't reload",
			act: func(_ *testing.T, _ string, clock *fakeClock, _ chan<- os.Signal) {
				clock.tick()
			},
			wantReloads: 1,
		},
		{
			name: "poll after a change reloads",
			act: func(t *testing.T, dir string, clock *fakeClock, _ chan<- os.Signal) {
				writeFile(t, dir, "checkout/alerts.sigil", "// a comment is a change\n"+readEmbedded(t, "checkout/alerts.sigil"))
				clock.tick()
			},
			wantReloads: 2,
		},
		{
			name: "a file that isn't sigil is ignored",
			act: func(t *testing.T, dir string, clock *fakeClock, _ chan<- os.Signal) {
				writeFile(t, dir, "checkout/notes.md", "not a policy")
				clock.tick()
			},
			wantReloads: 1,
		},
		{
			name: "a broken change is reported once",
			act: func(t *testing.T, dir string, clock *fakeClock, _ chan<- os.Signal) {
				writeFile(t, dir, "checkout/alerts.sigil", brokenPolicy)
				clock.tick()
				clock.tick()
			},
			wantReloads: 1,
			wantFailed:  1,
		},
		{
			name: "sighup always reloads",
			act: func(_ *testing.T, _ string, _ *fakeClock, sighup chan<- os.Signal) {
				sighup <- syscall.SIGHUP
			},
			wantReloads: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := teamsDir(t, nil)
			m := telemetry.NewMetrics()
			clock := newFakeClock()
			st := store.NewRouting(
				store.WithTeams("checkout"),
				store.WithBundleDir(dir),
				store.WithMetrics(m),
				store.WithClock(clock),
			)
			if err := st.Load(context.Background()); err != nil {
				t.Fatalf("Load: %s", chain(err))
			}

			ctx, cancel := context.WithCancel(context.Background())
			sighup := make(chan os.Signal)
			done := make(chan struct{})
			go func() {
				st.Watch(ctx, time.Minute, sighup)
				close(done)
			}()

			tt.act(t, dir, clock, sighup)
			// A second tick can only be taken once the first one was handled,
			// so after it the reload the first one triggered has finished.
			clock.tick()
			cancel()
			<-done

			if got := reloads(t, m, "success"); got != tt.wantReloads {
				t.Errorf("successful loads = %v, want %v", got, tt.wantReloads)
			}
			if got := reloads(t, m, "failure"); got != tt.wantFailed {
				t.Errorf("failed loads = %v, want %v", got, tt.wantFailed)
			}
		})
	}
}

// TestWatchWithoutPolling checks that a zero interval disables polling: a
// changed bundle isn't reloaded until a SIGHUP asks for it.
func TestWatchWithoutPolling(t *testing.T) {
	dir := teamsDir(t, nil)
	m := telemetry.NewMetrics()
	st := store.NewRouting(store.WithTeams("checkout"), store.WithBundleDir(dir), store.WithMetrics(m), store.WithClock(newFakeClock()))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load: %s", chain(err))
	}

	ctx, cancel := context.WithCancel(context.Background())
	sighup := make(chan os.Signal)
	done := make(chan struct{})
	go func() {
		st.Watch(ctx, 0, sighup)
		close(done)
	}()
	writeFile(t, dir, "checkout/alerts.sigil", "// changed\n"+readEmbedded(t, "checkout/alerts.sigil"))
	sighup <- syscall.SIGHUP
	sighup <- syscall.SIGHUP // taken only after the first reload finished
	cancel()
	<-done

	if got := reloads(t, m, "success"); got != 3 {
		t.Errorf("successful loads = %v, want 3: the first and two signals", got)
	}
}

func TestInitialLoad(t *testing.T) {
	m := telemetry.NewMetrics()
	dir := teamsDir(t, map[string]string{"checkout/alerts.sigil": brokenPolicy})
	st := store.NewRouting(store.WithTeams("checkout"), store.WithBundleDir(dir), store.WithMetrics(m))

	err := st.InitialLoad(context.Background())
	if err == nil || !strings.Contains(chain(err), "no earlier bundle to fall back to") {
		t.Fatalf("InitialLoad error = %v, want the failure without a fallback", err)
	}
	if got := reloads(t, m, "failure"); got != 1 {
		t.Errorf("failure reloads = %v, want 1", got)
	}
}

// TestLoadSpan checks the span every load runs in: its name, the trigger, and
// the error status of a rejected bundle.
func TestLoadSpan(t *testing.T) {
	tests := []struct {
		name        string
		files       map[string]string
		load        func(context.Context, *store.Store[routing.Input]) error
		wantTrigger string
		wantError   bool
	}{
		{
			name:        "a manual load",
			load:        func(ctx context.Context, st *store.Store[routing.Input]) error { return st.Load(ctx) },
			wantTrigger: store.TriggerManual,
		},
		{
			name:        "a failed startup",
			files:       map[string]string{"checkout/alerts.sigil": brokenPolicy},
			load:        func(ctx context.Context, st *store.Store[routing.Input]) error { return st.InitialLoad(ctx) },
			wantTrigger: store.TriggerStartup,
			wantError:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spans := tracetest.NewInMemoryExporter()
			tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
			t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
			st := store.NewRouting(
				store.WithTeams("checkout"),
				store.WithBundleDir(teamsDir(t, tt.files)),
				store.WithTracer(tp.Tracer(telemetry.TracerName)),
			)

			_ = tt.load(context.Background(), st)

			got := spans.GetSpans()
			if len(got) != 1 || got[0].Name != "alertrouter.policy.load" {
				t.Fatalf("spans = %v, want one alertrouter.policy.load", got.Snapshots())
			}
			attrs := map[attribute.Key]string{}
			for _, kv := range got[0].Attributes {
				attrs[kv.Key] = kv.Value.String()
			}
			if attrs["alertrouter.reload.trigger"] != tt.wantTrigger || attrs["sigil.kind"] != "AlertRouting" {
				t.Errorf("attributes = %v, want trigger %s of AlertRouting", attrs, tt.wantTrigger)
			}
			if isErr := got[0].Status.Code.String() == "Error"; isErr != tt.wantError {
				t.Errorf("span status = %v, want error %v", got[0].Status, tt.wantError)
			}
		})
	}
}

// TestNewWithoutBundle checks that a store built with New and nothing else
// reports what is missing instead of panicking.
func TestNewWithoutBundle(t *testing.T) {
	tests := []struct {
		name    string
		opts    []store.Option
		wantErr string
	}{
		{name: "no roots", opts: []store.Option{store.WithBundle(policies.Teams, "embedded")}, wantErr: "no AlertRouting policy is configured"},
		{name: "no bundle", opts: []store.Option{store.WithTeams("checkout")}, wantErr: "no bundle is configured"},
		{name: "a missing directory", opts: []store.Option{store.WithTeams("checkout"), store.WithBundleDir("/nonexistent/alertrouter")}, wantErr: "reading the AlertRouting policies from /nonexistent/alertrouter failed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.New(routing.Kind, tt.opts...)
			err := st.Load(context.Background())
			if err == nil || !strings.Contains(chain(err), tt.wantErr) {
				t.Fatalf("Load error = %v, want one containing %q", err, tt.wantErr)
			}
			if _, ok := st.Snapshot(); ok {
				t.Error("a failed load left a snapshot")
			}
		})
	}
}

// TestFingerprintFollowsLinkedDirectories lays out a directory the way
// kubelet mounts a ConfigMap with items that have paths: the team directory
// is a link into ..data, which is itself a link to a timestamped directory.
// Swapping ..data must change the fingerprint.
func TestFingerprintFollowsLinkedDirectories(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "..2026_09_28_1/checkout/alerts.sigil", "one")
	writeFile(t, dir, "..2026_09_28_2/checkout/alerts.sigil", "two")
	link(t, "..2026_09_28_1", filepath.Join(dir, "..data"))
	link(t, filepath.Join("..data", "checkout"), filepath.Join(dir, "checkout"))

	before, err := store.Fingerprint(os.DirFS(dir))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	empty, _ := store.Fingerprint(mapDir(t, nil))
	if before == empty {
		t.Fatal("the fingerprint ignored the linked directory")
	}

	// kubelet's swap: a new link renamed over ..data.
	link(t, "..2026_09_28_2", filepath.Join(dir, "..data_tmp"))
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}

	after, err := store.Fingerprint(os.DirFS(dir))
	if err != nil {
		t.Fatalf("Fingerprint after the swap: %v", err)
	}
	if after == before {
		t.Error("the fingerprint didn't change when ..data was swapped")
	}
}

func TestFingerprint(t *testing.T) {
	base := map[string]string{"a.sigil": "one", "sub/b.sigil": "two"}
	tests := []struct {
		files map[string]string
		name  string
		same  bool
	}{
		{name: "identical", files: base, same: true},
		{name: "other files ignored", files: merge(base, map[string]string{"README.md": "x"}), same: true},
		{name: "hidden entries ignored", files: merge(base, map[string]string{".hidden.sigil": "x", "..data/c.sigil": "y"}), same: true},
		{name: "content changed", files: merge(base, map[string]string{"a.sigil": "uno"})},
		{name: "file added", files: merge(base, map[string]string{"c.sigil": "three"})},
		{name: "file renamed", files: map[string]string{"z.sigil": "one", "sub/b.sigil": "two"}},
	}

	want, err := store.Fingerprint(mapDir(t, base))
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := store.Fingerprint(mapDir(t, tt.files))
			if err != nil {
				t.Fatalf("Fingerprint: %v", err)
			}
			if (got == want) != tt.same {
				t.Errorf("fingerprint equal = %v, want %v", got == want, tt.same)
			}
		})
	}

	if _, err := store.Fingerprint(os.DirFS(filepath.Join(t.TempDir(), "missing"))); err == nil {
		t.Error("Fingerprint of a missing directory succeeded")
	}
	dangling := t.TempDir()
	link(t, "nowhere", filepath.Join(dangling, "checkout"))
	if _, err := store.Fingerprint(os.DirFS(dangling)); err == nil {
		t.Error("Fingerprint over a dangling link succeeded")
	}
}

// fakeClock is a Clock whose ticks the test sends by hand.
type fakeClock struct {
	now   time.Time
	ticks chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Unix(0, 0), ticks: make(chan time.Time)}
}

func (c *fakeClock) Now() time.Time { return c.now }

func (c *fakeClock) Tick(time.Duration) (<-chan time.Time, func()) {
	return c.ticks, func() {}
}

// tick blocks until Watch takes the tick.
func (c *fakeClock) tick() { c.ticks <- c.now }

// reloads returns alertrouter_policy_reloads_total for result, zero when the
// series doesn't exist.
func reloads(t *testing.T, m *telemetry.Metrics, result string) float64 {
	t.Helper()
	v, _ := metricValue(t, m, "alertrouter_policy_reloads_total", map[string]string{"result": result})
	return v
}

// reloadHealth returns the two reload gauges: the Unix time of the last
// successful load, and whether the latest load succeeded.
func reloadHealth(t *testing.T, m *telemetry.Metrics) (at, successful float64) {
	t.Helper()
	at, ok := metricValue(t, m, "alertrouter_policy_last_reload_timestamp_seconds", map[string]string{})
	if !ok {
		t.Fatal("no last reload time")
	}
	successful, ok = metricValue(t, m, "alertrouter_policy_last_reload_successful", map[string]string{})
	if !ok {
		t.Fatal("no reload health")
	}
	return at, successful
}

// metricValue returns the value of the counter or gauge series called name
// with exactly want as its labels, and false when there is no such series.
func metricValue(t *testing.T, m *telemetry.Metrics, name string, want map[string]string) (float64, bool) {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != name {
			continue
		}
		for _, metric := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range metric.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if !maps.Equal(labels, want) {
				continue
			}
			if metric.GetGauge() != nil {
				return metric.GetGauge().GetValue(), true
			}
			return metric.GetCounter().GetValue(), true
		}
	}
	return 0, false
}

// teamsDir copies the embedded team policies into a temporary directory and
// applies overrides, so a test can change files while the store reads them.
func teamsDir(t *testing.T, overrides map[string]string) string {
	t.Helper()

	dir := t.TempDir()
	err := fs.WalkDir(policies.Teams, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(name) != ".sigil" {
			return err
		}
		writeFile(t, dir, name, readEmbedded(t, name))
		return nil
	})
	if err != nil {
		t.Fatalf("copying the embedded teams: %v", err)
	}
	for name, content := range overrides {
		writeFile(t, dir, name, content)
	}
	return dir
}

// chain joins the messages of err and everything it wraps, since a humane
// error's message leaves out its cause.
func chain(err error) string {
	var parts []string
	for ; err != nil; err = errors.Unwrap(err) {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, ": ")
}

func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Fatal(err)
	}
}

func mapDir(t *testing.T, files map[string]string) fs.FS {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		writeFile(t, dir, name, content)
	}
	return os.DirFS(dir)
}

func readEmbedded(t *testing.T, name string) string {
	t.Helper()
	data, err := fs.ReadFile(policies.Teams, name)
	if err != nil {
		t.Fatalf("reading embedded %s: %v", name, err)
	}
	return string(data)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func merge(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	maps.Copy(out, a)
	maps.Copy(out, b)
	return out
}
