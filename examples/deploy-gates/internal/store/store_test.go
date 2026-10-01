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

	"github.com/spechtlabs/sigil/pkg/policy"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/deploy"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/store"
	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/deploy-gates/policies"
)

// The kinds the reload metrics are labeled with.
const (
	deployKind = "DeployApproval"
	accessKind = "AccessGrant"
)

// brokenPolicy doesn't compile: it invokes a policy nothing defines.
const brokenPolicy = `policy payments.production: DeployApproval@1

use deploy.guardrails

guardrails()
nonexistent()
`

// ungatedAccess grants a role without invoking the required guardrails.
const ungatedAccess = `policy access.main: AccessGrant@1

when team in actor.groups {
  reader(reason: team_member)
}
`

// ungatedPolicy compiles on its own but skips the required guardrails.
const ungatedPolicy = `policy payments.production: DeployApproval@1

when true {
  approve(reason: payments_sre)
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
			teams:     []string{"payments", "checkout"},
			wantNames: []string{"payments.production", "checkout.production"},
		},
		{
			name:      "configured order is kept",
			teams:     []string{"checkout", "payments"},
			wantNames: []string{"checkout.production", "payments.production"},
		},
		{
			name:    "no team",
			wantErr: "no DeployApproval policy is configured",
		},
		{
			name:    "unknown team",
			teams:   []string{"payments", "billing"},
			wantErr: "billing.production failed to compile",
		},
		{
			name:    "broken policy",
			teams:   []string{"payments"},
			files:   map[string]string{"payments/production.sigil": brokenPolicy},
			wantErr: "nonexistent",
		},
		{
			name:    "guardrails skipped",
			teams:   []string{"payments"},
			files:   map[string]string{"payments/production.sigil": ungatedPolicy},
			wantErr: "deploy.guardrails",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := teamsDir(t, tt.files)
			m := telemetry.NewMetrics()
			at := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
			st := store.NewDeploy(
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
				if got := reloads(t, m, deployKind, "failure"); got != 1 {
					t.Errorf("failure reloads = %v, want 1", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %v", err)
			}

			snap, ok := st.Snapshot()
			if !ok {
				t.Fatal("no snapshot after a successful load")
			}
			if got := strings.Join(snap.PolicyNames(), ","); got != strings.Join(tt.wantNames, ",") {
				t.Errorf("policies = %s, want %s", got, strings.Join(tt.wantNames, ","))
			}
			if snap.Kind != "DeployApproval" || snap.KindVersion != 2 {
				t.Errorf("kind = %s@%d, want DeployApproval@2", snap.Kind, snap.KindVersion)
			}
			if snap.Source != dir {
				t.Errorf("source = %q, want %q", snap.Source, dir)
			}
			if !snap.LoadedAt.Equal(at) {
				t.Errorf("loaded at %v, want the clock's %v", snap.LoadedAt, at)
			}
			for _, team := range tt.teams {
				if _, ok := st.Policy(team); !ok {
					t.Errorf("Policy(%q) missing", team)
				}
			}
			if _, ok := st.Policy("billing"); ok {
				t.Error("Policy(billing) found a team that isn't served")
			}
			if got := reloads(t, m, deployKind, "success"); got != 1 {
				t.Errorf("success reloads = %v, want 1", got)
			}
		})
	}
}

func TestLoadEmbeddedByDefault(t *testing.T) {
	st := store.NewDeploy(store.WithTeams("payments"))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap, _ := st.Snapshot()
	if snap.Source != store.SourceEmbedded {
		t.Errorf("source = %q, want %q", snap.Source, store.SourceEmbedded)
	}
}

// TestReloadKeepsLastKnownGood breaks the bundle after a good load and checks
// that the old snapshot keeps serving, the error carries the diagnostics, and
// the reload gauges report the broken bundle without touching the access
// store's, which shares the metrics as it does in deploygate.
func TestReloadKeepsLastKnownGood(t *testing.T) {
	dir := teamsDir(t, nil)
	m := telemetry.NewMetrics()
	clock := &fakeClock{now: time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)}
	goodAt := float64(clock.now.Unix())
	st := store.NewDeploy(store.WithTeams("payments", "checkout"), store.WithBundleDir(dir), store.WithMetrics(m), store.WithClock(clock))
	acc := store.NewAccess(store.WithMetrics(m), store.WithClock(clock))

	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	if err := acc.Load(context.Background()); err != nil {
		t.Fatalf("access Load: %v", err)
	}
	good, _ := st.Snapshot()
	if at, ok := reloadHealth(t, m, deployKind); at != goodAt || ok != 1 {
		t.Errorf("after a good load: last reload %v, successful %v, want %v and 1", at, ok, goodAt)
	}

	clock.now = clock.now.Add(time.Hour)
	writeFile(t, dir, "payments/production.sigil", brokenPolicy)
	err := st.Load(context.Background())
	if err == nil {
		t.Fatal("Load of a broken bundle succeeded")
	}
	for _, want := range []string{"payments.production failed to compile", "previous bundle keeps serving", "nonexistent"} {
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
	if got := reloads(t, m, deployKind, "failure"); got != 1 {
		t.Errorf("failure reloads = %v, want 1", got)
	}
	// The failure marks the bundle unhealthy and leaves the time of the
	// load that still serves.
	if at, ok := reloadHealth(t, m, deployKind); at != goodAt || ok != 0 {
		t.Errorf("after a failed load: last reload %v, successful %v, want %v and 0", at, ok, goodAt)
	}
	if at, ok := reloadHealth(t, m, accessKind); at != goodAt || ok != 1 {
		t.Errorf("access after the team bundle failed: last reload %v, successful %v, want %v and 1", at, ok, goodAt)
	}

	// Fixing the file makes the next load succeed with a new snapshot.
	writeFile(t, dir, "payments/production.sigil", readEmbedded(t, "payments/production.sigil"))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load after the fix: %v", err)
	}
	if now, _ := st.Snapshot(); now == good {
		t.Error("a successful reload kept the old snapshot")
	}
	if at, ok := reloadHealth(t, m, deployKind); at != float64(clock.now.Unix()) || ok != 1 {
		t.Errorf("after the fix: last reload %v, successful %v, want %v and 1", at, ok, clock.now.Unix())
	}
}

func TestWatch(t *testing.T) {
	tests := []struct {
		act         func(t *testing.T, dir string, clock *fakeClock, sighup chan<- os.Signal)
		name        string
		wantReloads float64
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
				writeFile(t, dir, "payments/production.sigil", "// a comment is a change\n"+readEmbedded(t, "payments/production.sigil"))
				clock.tick()
			},
			wantReloads: 2,
		},
		{
			name: "a file that isn't sigil is ignored",
			act: func(t *testing.T, dir string, clock *fakeClock, _ chan<- os.Signal) {
				writeFile(t, dir, "payments/notes.md", "not a policy")
				clock.tick()
			},
			wantReloads: 1,
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
			st := store.NewDeploy(
				store.WithTeams("payments"),
				store.WithBundleDir(dir),
				store.WithMetrics(m),
				store.WithClock(clock),
			)
			if err := st.Load(context.Background()); err != nil {
				t.Fatalf("Load: %v", err)
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

			if got := reloads(t, m, deployKind, "success"); got != tt.wantReloads {
				t.Errorf("successful loads = %v, want %v", got, tt.wantReloads)
			}
		})
	}
}

func TestInitialLoad(t *testing.T) {
	m := telemetry.NewMetrics()
	dir := teamsDir(t, map[string]string{"payments/production.sigil": brokenPolicy})
	st := store.NewDeploy(store.WithTeams("payments"), store.WithBundleDir(dir), store.WithMetrics(m))

	err := st.InitialLoad(context.Background())
	if err == nil || !strings.Contains(chain(err), "no earlier bundle to fall back to") {
		t.Fatalf("InitialLoad error = %v, want the failure without a fallback", err)
	}
	if got := reloads(t, m, deployKind, "failure"); got != 1 {
		t.Errorf("failure reloads = %v, want 1", got)
	}
}

func TestAccessStore(t *testing.T) {
	tests := []struct {
		name    string
		opts    func(t *testing.T) []store.Option
		wantErr string
	}{
		{name: "embedded", opts: func(*testing.T) []store.Option { return nil }},
		{name: "from a directory", opts: func(t *testing.T) []store.Option {
			return []store.Option{store.WithBundleDir(accessDir(t, nil))}
		}},
		{name: "broken access policy", wantErr: "access.main failed to compile", opts: func(t *testing.T) []store.Option {
			return []store.Option{store.WithBundleDir(accessDir(t, map[string]string{"main.sigil": "policy access.main: AccessGrant@1\n\nguardrails(\n"}))}
		}},
		{name: "guardrails skipped", wantErr: "access.guardrails", opts: func(t *testing.T) []store.Option {
			return []store.Option{store.WithBundleDir(accessDir(t, map[string]string{"main.sigil": ungatedAccess}))}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := telemetry.NewMetrics()
			st := store.NewAccess(append(tt.opts(t), store.WithMetrics(m))...)
			err := st.Load(context.Background())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(chain(err), tt.wantErr) {
					t.Fatalf("Load error = %v, want one containing %q", err, tt.wantErr)
				}
				if got := reloads(t, m, accessKind, "failure"); got != 1 {
					t.Errorf("access failure reloads = %v, want 1", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load: %s", chain(err))
			}

			snap, _ := st.Snapshot()
			if snap.Kind != accessKind || snap.KindVersion != 1 {
				t.Errorf("kind = %s@%d, want AccessGrant@1", snap.Kind, snap.KindVersion)
			}
			if got := snap.PolicyNames(); len(got) != 1 || got[0] != store.AccessRoot {
				t.Errorf("policies = %v, want [access.main]", got)
			}
			if len(snap.TeamNames()) != 0 {
				t.Errorf("teams = %v, want none for a fixed root", snap.TeamNames())
			}
			p, ok := snap.Single()
			if !ok || p.Name() != store.AccessRoot {
				t.Fatalf("Single() = %v, %v, want access.main", p, ok)
			}
			if _, ok := st.Policy(store.AccessRoot); !ok {
				t.Error("Policy(access.main) missing")
			}
			if got := reloads(t, m, accessKind, "success"); got != 1 {
				t.Errorf("access success reloads = %v, want 1", got)
			}
			if got := reloads(t, m, deployKind, "success"); got != 0 {
				t.Errorf("deploy success reloads = %v, want 0", got)
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
		{name: "no roots", opts: []store.Option{store.WithBundle(policies.Teams, "embedded")}, wantErr: "no DeployApproval policy is configured"},
		{name: "no bundle", opts: []store.Option{store.WithTeams("payments")}, wantErr: "no bundle is configured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := store.New(deploy.Kind, tt.opts...)
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
	writeFile(t, dir, "..2026_09_28_1/payments/production.sigil", "one")
	writeFile(t, dir, "..2026_09_28_2/payments/production.sigil", "two")
	link(t, "..2026_09_28_1", filepath.Join(dir, "..data"))
	link(t, filepath.Join("..data", "payments"), filepath.Join(dir, "payments"))

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

// reloads returns deploygate_policy_reloads_total for kind and result, zero
// when the series doesn't exist.
func reloads(t *testing.T, m *telemetry.Metrics, kind, result string) float64 {
	t.Helper()
	v, _ := metricValue(t, m, "deploygate_policy_reloads_total", map[string]string{"kind": kind, "result": result})
	return v
}

// reloadHealth returns kind's two reload gauges: the Unix time of its last
// successful load, and whether its latest load succeeded. Both series must
// exist, so a missing one never passes for a zero.
func reloadHealth(t *testing.T, m *telemetry.Metrics, kind string) (at, successful float64) {
	t.Helper()
	labels := map[string]string{"kind": kind}
	at, ok := metricValue(t, m, "deploygate_policy_last_reload_timestamp_seconds", labels)
	if !ok {
		t.Fatalf("no last reload time for %s", kind)
	}
	successful, ok = metricValue(t, m, "deploygate_policy_last_reload_successful", labels)
	if !ok {
		t.Fatalf("no reload health for %s", kind)
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

// accessDir copies the embedded access policies into a temporary directory
// and applies overrides.
func accessDir(t *testing.T, overrides map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	err := fs.WalkDir(policies.Access, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(name) != ".sigil" {
			return err
		}
		data, err := fs.ReadFile(policies.Access, name)
		if err != nil {
			return err
		}
		writeFile(t, dir, name, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("copying the embedded access policies: %v", err)
	}
	for name, content := range overrides {
		writeFile(t, dir, name, content)
	}
	return dir
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
