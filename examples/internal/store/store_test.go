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

	"github.com/spechtlabs/sigil/examples/internal/deploy"
	"github.com/spechtlabs/sigil/examples/internal/store"
	"github.com/spechtlabs/sigil/examples/internal/telemetry"
	"github.com/spechtlabs/sigil/examples/policies"
)

// brokenPolicy doesn't compile: it invokes a policy nothing defines.
const brokenPolicy = `policy payments.production: DeployApproval@1

use deploy.guardrails

guardrails()
nonexistent()
`

// ungatedPolicy compiles on its own but skips the required guardrails.
const ungatedPolicy = `policy payments.production: DeployApproval@1

when true {
  approve(payments_sre)
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
			wantErr: "no team is configured",
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
			st := store.New(deploy.Kind,
				store.WithTeams(tt.teams...),
				store.WithTeamsDir(dir),
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
				t.Fatalf("Load: %v", err)
			}

			snap, ok := st.Snapshot()
			if !ok {
				t.Fatal("no snapshot after a successful load")
			}
			if got := strings.Join(snap.PolicyNames(), ","); got != strings.Join(tt.wantNames, ",") {
				t.Errorf("policies = %s, want %s", got, strings.Join(tt.wantNames, ","))
			}
			if snap.Kind != "DeployApproval" || snap.KindVersion != 1 {
				t.Errorf("kind = %s@%d, want DeployApproval@1", snap.Kind, snap.KindVersion)
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
			if got := reloads(t, m, "success"); got != 1 {
				t.Errorf("success reloads = %v, want 1", got)
			}
		})
	}
}

func TestLoadEmbeddedByDefault(t *testing.T) {
	st := store.New(deploy.Kind, store.WithTeams("payments"))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	snap, _ := st.Snapshot()
	if snap.Source != store.SourceEmbedded {
		t.Errorf("source = %q, want %q", snap.Source, store.SourceEmbedded)
	}
}

// TestReloadKeepsLastKnownGood breaks the bundle after a good load and checks
// that the old snapshot keeps serving and the error carries the diagnostics.
func TestReloadKeepsLastKnownGood(t *testing.T) {
	dir := teamsDir(t, nil)
	m := telemetry.NewMetrics()
	st := store.New(deploy.Kind, store.WithTeams("payments", "checkout"), store.WithTeamsDir(dir), store.WithMetrics(m))

	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("first Load: %v", err)
	}
	good, _ := st.Snapshot()

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
	if got := reloads(t, m, "failure"); got != 1 {
		t.Errorf("failure reloads = %v, want 1", got)
	}

	// Fixing the file makes the next load succeed with a new snapshot.
	writeFile(t, dir, "payments/production.sigil", readEmbedded(t, "payments/production.sigil"))
	if err := st.Load(context.Background()); err != nil {
		t.Fatalf("Load after the fix: %v", err)
	}
	if now, _ := st.Snapshot(); now == good {
		t.Error("a successful reload kept the old snapshot")
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
			st := store.New(deploy.Kind,
				store.WithTeams("payments"),
				store.WithTeamsDir(dir),
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

			if got := reloads(t, m, "success"); got != tt.wantReloads {
				t.Errorf("successful loads = %v, want %v", got, tt.wantReloads)
			}
		})
	}
}

func TestInitialLoad(t *testing.T) {
	m := telemetry.NewMetrics()
	dir := teamsDir(t, map[string]string{"payments/production.sigil": brokenPolicy})
	st := store.New(deploy.Kind, store.WithTeams("payments"), store.WithTeamsDir(dir), store.WithMetrics(m))

	err := st.InitialLoad(context.Background())
	if err == nil || !strings.Contains(chain(err), "no earlier bundle to fall back to") {
		t.Fatalf("InitialLoad error = %v, want the failure without a fallback", err)
	}
	if got := reloads(t, m, "failure"); got != 1 {
		t.Errorf("failure reloads = %v, want 1", got)
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

// reloads returns deploygate_policy_reloads_total for result.
func reloads(t *testing.T, m *telemetry.Metrics, result string) float64 {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	for _, f := range families {
		if f.GetName() != "deploygate_policy_reloads_total" {
			continue
		}
		for _, metric := range f.GetMetric() {
			for _, l := range metric.GetLabel() {
				if l.GetName() == "result" && l.GetValue() == result {
					return metric.GetCounter().GetValue()
				}
			}
		}
	}
	return 0
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
