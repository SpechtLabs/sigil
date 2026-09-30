package telemetry

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"

	"go.uber.org/zap"
)

func TestProfilingDisabledWithoutBackend(t *testing.T) {
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "")
	p, err := startProfiler("test", zap.NewNop())
	if err != nil || p != nil {
		t.Fatalf("profiling without a backend: profiler=%v, err=%v", p, err)
	}
}

func TestProfilingRejectsInvalidBackend(t *testing.T) {
	before := runtime.SetMutexProfileFraction(-1)
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", "://invalid")
	t.Setenv("PYROSCOPE_ADHOC_SERVER_ADDRESS", "://invalid")
	p, err := startProfiler("test", zap.NewNop())
	if err == nil {
		if p != nil {
			_ = p.Stop()
		}
		t.Fatal("invalid profiling backend accepted")
	}
	if got := runtime.SetMutexProfileFraction(-1); got != before {
		t.Fatalf("failed startup changed mutex sampling from %d to %d", before, got)
	}
}

// Inspect real SDK uploads, including empty leak profiles from a healthy
// process, rather than just checking the list passed to pyroscope.Start.
func TestProfilingUploadsAllTypesAndRestoresSampling(t *testing.T) {
	var mu sync.Mutex
	uploaded := make(map[string]bool)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _, err := r.FormFile("profile")
		if err != nil {
			t.Errorf("profile upload: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = data.Close() }()
		defer func() { _ = r.MultipartForm.RemoveAll() }()
		body, err := io.ReadAll(data)
		if err != nil || len(body) == 0 {
			t.Errorf("empty or unreadable profile: %v", err)
		}

		mu.Lock()
		defer mu.Unlock()
		if r.URL.Query().Get("units") == "samples" {
			uploaded["cpu"] = true
			return
		}
		config, _, err := r.FormFile("sample_type_config")
		if err != nil {
			t.Errorf("profile sample types: %v", err)
			return
		}
		defer func() { _ = config.Close() }()
		var types map[string]struct {
			DisplayName string `json:"display-name"`
		}
		if err := json.NewDecoder(config).Decode(&types); err != nil {
			t.Errorf("decoding sample types: %v", err)
			return
		}
		for name, sample := range types {
			if sample.DisplayName != "" {
				name = sample.DisplayName
			}
			uploaded[name] = true
		}
	}))
	t.Cleanup(backend.Close)
	t.Setenv("PYROSCOPE_SERVER_ADDRESS", backend.URL)
	t.Setenv("PYROSCOPE_ADHOC_SERVER_ADDRESS", backend.URL)
	t.Setenv("OTEL_SERVICE_NAME", "profile-test")
	previous := runtime.SetMutexProfileFraction(13)
	t.Cleanup(func() { runtime.SetMutexProfileFraction(previous) })
	p, err := startProfiler("test", zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if p != nil {
			_ = p.Stop()
		}
	})
	if got := runtime.SetMutexProfileFraction(-1); got != 5 {
		t.Fatalf("mutex sampling fraction = %d, want 5", got)
	}
	p.profiler.Flush(true)
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	p = nil
	if got := runtime.SetMutexProfileFraction(-1); got != 13 {
		t.Fatalf("stopping profiler restored fraction %d, want 13", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, name := range []string{
		"cpu", "alloc_objects", "alloc_space", "inuse_objects", "inuse_space",
		"goroutines", "mutex_count", "mutex_duration", "block_count", "block_duration", "goroutine_leak",
	} {
		if !uploaded[name] {
			t.Errorf("profile %s was not uploaded; got %v", name, uploaded)
		}
	}
}
