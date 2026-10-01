package freeze_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/spechtlabs/sigil/examples/deploy-gates/internal/freeze"
)

// flagPath is where the default flag is evaluated.
const flagPath = "/ofrep/v1/evaluate/flags/change-freeze"

func TestNewOFREP(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		opts    []freeze.Option
		wantErr string
	}{
		{name: "defaults", url: "http://flagd:8016"},
		{name: "https with a trailing slash", url: "https://flags.example.com/"},
		{name: "not a URL", url: "http://[::1", wantErr: "isn't a URL"},
		{name: "no scheme", url: "flagd:8016", wantErr: "isn't an absolute http or https URL"},
		{name: "no host", url: "http://", wantErr: "isn't an absolute http or https URL"},
		{name: "another scheme", url: "ftp://flagd", wantErr: "isn't an absolute http or https URL"},
		{name: "empty flag", url: "http://f", opts: []freeze.Option{freeze.WithFlag("")}, wantErr: "flag key is empty"},
		{name: "no refresh interval", url: "http://f", opts: []freeze.Option{freeze.WithRefreshInterval(0)}, wantErr: "refresh interval 0s isn't positive"},
		{
			name:    "staleness no longer than the interval",
			url:     "http://f",
			opts:    []freeze.Option{freeze.WithRefreshInterval(time.Minute), freeze.WithMaxStaleness(time.Minute)},
			wantErr: "maximum staleness 1m0s isn't longer than its refresh interval 1m0s",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src, herr := freeze.NewOFREP(tt.url, tt.opts...)
			if tt.wantErr == "" {
				if herr != nil || src == nil {
					t.Fatalf("NewOFREP(%q) = %v, %v; want a source", tt.url, src, herr)
				}
				if got := src.Freeze(); !got.Unknown || got.Environments == nil || len(got.Environments) != 0 {
					t.Errorf("Freeze() before a refresh = %+v, want unknown with no environments", got)
				}
				return
			}
			if herr == nil || !strings.Contains(herr.Error(), tt.wantErr) || len(herr.Advice()) == 0 {
				t.Fatalf("NewOFREP(%q) error = %v, want one containing %q, with advice", tt.url, herr, tt.wantErr)
			}
		})
	}
}

// TestOFREPRefresh answers the evaluation with each shape an OFREP service
// can give, and checks what the source makes of it: the frozen environments,
// or a failed refresh that names why.
func TestOFREPRefresh(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		want       []string
		wantErr    string
		wantAdvice string
	}{
		{name: "a list", body: `{"key":"change-freeze","value":["staging","production"],"reason":"TARGETING_MATCH","variant":"frozen"}`, want: []string{"production", "staging"}},
		{name: "a comma-separated string", body: `{"key":"change-freeze","value":"production, staging,","reason":"TARGETING_MATCH"}`, want: []string{"production", "staging"}},
		{name: "an empty string, the flag off", body: `{"key":"change-freeze","value":"","reason":"DISABLED","variant":"off"}`, want: []string{}},
		{name: "an empty list", body: `{"key":"change-freeze","value":[],"reason":"STATIC"}`, want: []string{}},
		{name: "a null value", body: `{"key":"change-freeze","value":null,"reason":"STATIC"}`, wantErr: "the flag change-freeze has no value"},
		{name: "no value", body: `{"key":"change-freeze","reason":"STATIC"}`, wantErr: "the flag change-freeze has no value"},
		{name: "a boolean", body: `{"key":"change-freeze","value":true,"reason":"STATIC"}`, wantErr: "isn't a list of environments: true"},
		{name: "a list of numbers", body: `{"key":"change-freeze","value":[1,2],"reason":"STATIC"}`, wantErr: "isn't a list of environments: [1,2]"},
		{
			name:    "a failed evaluation answered with 200",
			body:    `{"key":"change-freeze","value":"","reason":"ERROR","variant":"off","metadata":{"sigil.error":"timeout"}}`,
			wantErr: "the flag service answered 200 evaluating change-freeze: ERROR",
		},
		{
			name:       "an unknown flag",
			status:     http.StatusNotFound,
			body:       `{"key":"change-freeze","errorCode":"FLAG_NOT_FOUND","errorDetails":"no policy serves change-freeze"}`,
			wantErr:    "answered 404 evaluating change-freeze: FLAG_NOT_FOUND (no policy serves change-freeze)",
			wantAdvice: "create the flag change-freeze",
		},
		{
			name:    "an error code with 400",
			status:  http.StatusBadRequest,
			body:    `{"key":"change-freeze","errorCode":"INVALID_CONTEXT","errorDetails":"region must be a string"}`,
			wantErr: "answered 400 evaluating change-freeze: INVALID_CONTEXT (region must be a string)",
		},
		{name: "an error without a code", status: http.StatusInternalServerError, body: `{}`, wantErr: "the flag service answered 500 evaluating change-freeze"},
		{name: "not JSON", status: http.StatusBadGateway, body: `<html>bad gateway</html>`, wantErr: "answered 502 for change-freeze with a body that isn't an OFREP evaluation"},
		{name: "too large", body: `{"value":"` + strings.Repeat("x", 70<<10) + `"}`, wantErr: "larger than 65536 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodPost || r.URL.Path != flagPath || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("request = %s %s (%s), want POST %s with JSON", r.Method, r.URL.Path, r.Header.Get("Content-Type"), flagPath)
				}
				var body map[string]map[string]string
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body["context"]["targetingKey"] != freeze.DefaultTargetingKey {
					t.Errorf("body = %v (%v), want a context with targetingKey %s", body, err, freeze.DefaultTargetingKey)
				}
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				_, _ = io.WriteString(w, tt.body)
			}))
			defer srv.Close()

			src := newSource(t, srv.URL, newFakeClock())
			herr := src.Refresh(context.Background())
			if requests.Load() != 1 {
				t.Errorf("Refresh sent %d requests, want 1", requests.Load())
			}

			got := src.Freeze()
			if tt.wantErr != "" {
				if herr == nil || !strings.Contains(herr.Error(), tt.wantErr) {
					t.Fatalf("Refresh error = %v, want one containing %q", herr, tt.wantErr)
				}
				if !hasAdvice(herr, tt.wantAdvice) {
					t.Errorf("advice = %q, want one containing %q", herr.Advice(), tt.wantAdvice)
				}
				if !got.Unknown {
					t.Errorf("Freeze() after a failed first refresh = %+v, want unknown", got)
				}
				return
			}
			if herr != nil {
				t.Fatalf("Refresh: %v", herr)
			}
			if got.Unknown || got.Environments == nil || !slices.Equal(got.Environments, tt.want) {
				t.Errorf("Freeze() = %+v, want environments %v, known", got, tt.want)
			}
		})
	}
}

// TestOFREPRequest checks what the source sends with a flag key that needs
// escaping and an evaluation context of its own.
func TestOFREPRequest(t *testing.T) {
	var got struct {
		path string
		body map[string]map[string]string
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.EscapedPath()
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		_, _ = io.WriteString(w, `{"value":"production"}`)
	}))
	defer srv.Close()

	src, herr := freeze.NewOFREP(srv.URL+"/",
		freeze.WithFlag("ops/freeze"),
		freeze.WithEvaluationContext(map[string]string{"region": "eu-1", "targetingKey": "deploygate-eu"}),
		freeze.WithHTTPClient(srv.Client()),
	)
	if herr != nil {
		t.Fatal(herr)
	}
	if herr := src.Refresh(context.Background()); herr != nil {
		t.Fatal(herr)
	}
	if got.path != "/ofrep/v1/evaluate/flags/ops%2Ffreeze" {
		t.Errorf("path = %s, want the flag key escaped", got.path)
	}
	want := map[string]string{"region": "eu-1", "targetingKey": "deploygate-eu"}
	if ctx := got.body["context"]; len(ctx) != len(want) || ctx["region"] != want["region"] || ctx["targetingKey"] != want["targetingKey"] {
		t.Errorf("context = %v, want %v", got.body["context"], want)
	}
	if f := src.Freeze(); f.Unknown || !slices.Equal(f.Environments, []string{"production"}) {
		t.Errorf("Freeze() = %+v, want production, known", f)
	}
}

func TestOFREPUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()

	src, herr := freeze.NewOFREP(url)
	if herr != nil {
		t.Fatal(herr)
	}
	herr = src.Refresh(context.Background())
	if herr == nil || !strings.Contains(herr.Error(), "can't be reached") || !hasAdvice(herr, "every deploy is denied as frozen") {
		t.Fatalf("Refresh error = %v, want an unreachable flag service with advice on the staleness", herr)
	}
}

// TestOFREPTruncated cuts the answer short of the length it announced.
func TestOFREPTruncated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = io.WriteString(w, `{"value":`)
	}))
	defer srv.Close()

	herr := newSource(t, srv.URL, newFakeClock()).Refresh(context.Background())
	if herr == nil || !strings.Contains(herr.Error(), "the flag service's answer for change-freeze can't be read") {
		t.Fatalf("Refresh error = %v, want an answer that can't be read", herr)
	}
}

// TestOFREPSpans checks the refresh span: the flag and the service, the
// environments of a successful refresh, and the error of a failed one.
func TestOFREPSpans(t *testing.T) {
	flag := newFlagServer(t)
	spans := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spans))
	defer func() { _ = tp.Shutdown(context.Background()) }()

	src, herr := freeze.NewOFREP(flag.srv.URL, freeze.WithTracer(tp.Tracer("test")))
	if herr != nil {
		t.Fatal(herr)
	}
	_ = src.Refresh(context.Background())
	flag.fail.Store(true)
	_ = src.Refresh(context.Background())

	got := spans.GetSpans()
	if len(got) != 2 {
		t.Fatalf("recorded %d spans, want 2", len(got))
	}
	for i, want := range []struct {
		status       codes.Code
		environments []string
	}{
		{status: codes.Ok, environments: []string{"production"}},
		{status: codes.Error},
	} {
		s := got[i]
		attrs := map[attribute.Key]attribute.Value{}
		for _, kv := range s.Attributes {
			attrs[kv.Key] = kv.Value
		}
		if s.Name != "deploygate.freeze.refresh" || s.Status.Code != want.status ||
			attrs["freeze.flag"].AsString() != freeze.DefaultFlag || attrs["freeze.source"].AsString() != flag.srv.URL {
			t.Errorf("span %d = %s %v %v, want deploygate.freeze.refresh %v with the flag and the source", i, s.Name, s.Status, attrs, want.status)
		}
		if envs := attrs["sigil.freeze.environments"].AsStringSlice(); !slices.Equal(envs, want.environments) {
			t.Errorf("span %d environments = %v, want %v", i, envs, want.environments)
		}
	}
}

// TestOFREPStaleness ages the last answer past the maximum staleness: the
// freeze turns unknown, keeps the environments it last heard of, survives a
// failed refresh that way, and is known again after the next success.
func TestOFREPStaleness(t *testing.T) {
	flag := newFlagServer(t)
	clock := newFakeClock()
	src := newSource(t, flag.srv.URL, clock)

	if herr := src.Refresh(context.Background()); herr != nil {
		t.Fatal(herr)
	}
	expectFreeze(t, src, []string{"production"}, false)

	clock.advance(freeze.DefaultMaxStaleness)
	expectFreeze(t, src, []string{"production"}, false)

	clock.advance(time.Nanosecond)
	expectFreeze(t, src, []string{"production"}, true)

	flag.fail.Store(true)
	if herr := src.Refresh(context.Background()); herr == nil {
		t.Fatal("Refresh succeeded against a failing flag service")
	}
	expectFreeze(t, src, []string{"production"}, true)

	flag.fail.Store(false)
	flag.value.Store(`"staging"`)
	if herr := src.Refresh(context.Background()); herr != nil {
		t.Fatal(herr)
	}
	expectFreeze(t, src, []string{"staging"}, false)
}

// TestOFREPRun drives the background loop through every state: a failure
// while the last answer is fresh, a failure once it's stale, the recovery,
// and a refresh still in flight when the context ends.
func TestOFREPRun(t *testing.T) {
	clock := newFakeClock()
	blocked := make(chan struct{})
	ok := func(value string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"key":"change-freeze","value":`+value+`,"reason":"TARGETING_MATCH"}`)
		}
	}
	fail := func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"errorCode":"GENERAL","errorDetails":"shutting down"}`)
	}
	// One handler per request, in order. The script, not the test, changes
	// the world between refreshes, so each refresh sees exactly its step.
	script := []http.HandlerFunc{
		ok(`["production"]`), // the first refresh, before Run
		fail,                 // a failure while the answer is fresh
		func(w http.ResponseWriter, r *http.Request) { // a failure once it's stale
			clock.advance(2 * freeze.DefaultMaxStaleness)
			fail(w, r)
		},
		ok(`["production","staging"]`), // the recovery
		func(_ http.ResponseWriter, r *http.Request) { // in flight when the context ends
			// The server notices a client that left only once the body is read.
			_, _ = io.Copy(io.Discard, r.Body)
			close(blocked)
			<-r.Context().Done()
		},
	}
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requests.Add(1)) - 1
		if n >= len(script) {
			t.Errorf("request %d beyond the script", n+1)
			return
		}
		script[n](w, r)
	}))
	defer srv.Close()

	src := newSource(t, srv.URL, clock)
	if herr := src.Refresh(context.Background()); herr != nil {
		t.Fatal(herr)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		src.Run(ctx)
	}()

	for range 4 {
		clock.tick <- clock.Now()
	}
	<-blocked
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't return after its context ended")
	}

	expectFreeze(t, src, []string{"production", "staging"}, false)
	if got := requests.Load(); got != int32(len(script)) {
		t.Errorf("the flag service got %d requests, want %d", got, len(script))
	}
}

// flagServer is an OFREP service with one flag whose value and failure a
// test switches.
type flagServer struct {
	srv   *httptest.Server
	value atomic.Value
	fail  atomic.Bool
}

func newFlagServer(t *testing.T) *flagServer {
	t.Helper()
	f := &flagServer{}
	f.value.Store(`["production"]`)
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case f.fail.Load():
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"errorCode":"GENERAL","errorDetails":"shutting down"}`)
		default:
			_, _ = io.WriteString(w, `{"key":"change-freeze","value":`+f.value.Load().(string)+`,"reason":"TARGETING_MATCH"}`)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// fakeClock is a clock the test moves by hand, with a tick channel it sends
// on itself.
type fakeClock struct {
	mu   sync.Mutex
	now  time.Time
	tick chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), tick: make(chan time.Time)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Tick(time.Duration) (<-chan time.Time, func()) {
	return c.tick, func() {}
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newSource(t *testing.T, url string, clock freeze.Clock) *freeze.OFREP {
	t.Helper()
	src, herr := freeze.NewOFREP(url, freeze.WithClock(clock))
	if herr != nil {
		t.Fatal(herr)
	}
	return src
}

func expectFreeze(t *testing.T, src freeze.Source, environments []string, unknown bool) {
	t.Helper()
	got := src.Freeze()
	if got.Unknown != unknown || !slices.Equal(got.Environments, environments) {
		t.Errorf("Freeze() = %+v, want environments %v, unknown %v", got, environments, unknown)
	}
}

// hasAdvice reports whether one of herr's advice lines contains want; an
// empty want only asks for some advice.
func hasAdvice(herr humane.Error, want string) bool {
	for _, a := range herr.Advice() {
		if strings.Contains(a, want) {
			return true
		}
	}
	return false
}
