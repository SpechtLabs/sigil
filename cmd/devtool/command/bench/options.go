package bench

import (
	"os"
	"time"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/cmdflag"
)

// Option configures the bench commands.
type Option func(*options)

type options struct {
	getenv func(string) string

	// root is the module whose benchmarks run; empty means the module
	// containing the working directory.
	root string
	// fixtures is the directory, relative to root, whose shared workload
	// helpers are copied onto the base revision with the benchmarks.
	fixtures string
}

// runOptions configures `bench run`: the flags every run command takes,
// and the ones only a comparison needs.
type runOptions struct {
	cmdflag.Run
	count      int
	baseline   string
	noBaseline bool
	benchstat  string
}

func defaultOptions() *options {
	return &options{
		getenv:   os.Getenv,
		fixtures: "internal/benchtest",
	}
}

func defaultRunOptions() runOptions {
	return runOptions{
		Time:      "200ms",
		CPU:       2,
		Timeout:   3 * time.Minute,
		Results:   "benchmark-results",
		count:     10,
		benchstat: "benchstat",
	}
}

// WithRoot sets the module to benchmark. It defaults to the module
// containing the working directory.
func WithRoot(dir string) Option {
	return func(o *options) { o.root = dir }
}

// WithFixtures sets the directory, relative to the module root, that holds
// the shared workload helpers. It defaults to internal/benchtest.
func WithFixtures(dir string) Option {
	return func(o *options) { o.fixtures = dir }
}

// WithGetenv sets how the commands read their BENCH_* variables. It
// defaults to os.Getenv; a nil func keeps the default.
func WithGetenv(getenv func(string) string) Option {
	return func(o *options) {
		if getenv != nil {
			o.getenv = getenv
		}
	}
}
