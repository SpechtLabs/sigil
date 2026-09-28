package telemetry

import (
	"net/http"
	"os"
	"runtime"
	"time"

	"github.com/grafana/pyroscope-go"
	humane "github.com/sierrasoftworks/humane-errors-go"
	"go.uber.org/zap"
)

// continuousProfiler owns the runtime's contention sampling while it runs.
// Go exposes the previous mutex fraction, but no getter for the block rate;
// this service owns block sampling and disables it when profiling stops.
type continuousProfiler struct {
	profiler              *pyroscope.Profiler
	previousMutexFraction int
}

func (p *continuousProfiler) Stop() humane.Error {
	err := p.profiler.Stop()
	runtime.SetMutexProfileFraction(p.previousMutexFraction)
	runtime.SetBlockProfileRate(0)
	if err != nil {
		return humane.Wrap(err, "stopping continuous profiling failed",
			"check that Pyroscope is reachable to flush pending profiles")
	}
	return nil
}

// startProfiler enables every Go SDK profile when a backend is configured.
// Profile labels describe the process; request IDs and actors would create an
// unbounded number of series. The heap collector doesn't request extra GCs,
// but Go's goroutine-leak profile runs a GC cycle to detect leaks.
func startProfiler(version string, logger *zap.Logger) (*continuousProfiler, humane.Error) {
	address := os.Getenv("PYROSCOPE_SERVER_ADDRESS")
	if address == "" {
		return nil, nil
	}
	name := os.Getenv("OTEL_SERVICE_NAME")
	if name == "" {
		name = DefaultServiceName
	}
	p, err := pyroscope.Start(pyroscope.Config{
		ApplicationName: name,
		ServerAddress:   address,
		Tags:            map[string]string{"version": version},
		UploadRate:      15 * time.Second,
		DisableGCRuns:   true,
		HTTPClient:      &http.Client{Timeout: 5 * time.Second},
		Logger:          logger.Sugar(),
		ProfileTypes: []pyroscope.ProfileType{
			pyroscope.ProfileCPU,
			pyroscope.ProfileAllocObjects,
			pyroscope.ProfileAllocSpace,
			pyroscope.ProfileInuseObjects,
			pyroscope.ProfileInuseSpace,
			pyroscope.ProfileGoroutines,
			pyroscope.ProfileMutexCount,
			pyroscope.ProfileMutexDuration,
			pyroscope.ProfileBlockCount,
			pyroscope.ProfileBlockDuration,
			pyroscope.ProfileGoroutineLeak,
		},
	})
	if err != nil {
		return nil, humane.Wrap(err, "starting continuous profiling failed",
			"check PYROSCOPE_SERVER_ADDRESS and that another CPU profiler isn't running")
	}
	// Enable contention sampling only after startup succeeds. Sample one in
	// five mutex contentions, and about one block event per millisecond blocked.
	previousMutexFraction := runtime.SetMutexProfileFraction(5)
	runtime.SetBlockProfileRate(int(time.Millisecond))
	return &continuousProfiler{profiler: p, previousMutexFraction: previousMutexFraction}, nil
}
