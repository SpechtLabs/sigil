package fuzz

import (
	"net/http"
	"os"
	"time"

	"github.com/spechtlabs/sigil/cmd/devtool/internal/cmdflag"
)

// Option configures the fuzz commands.
type Option func(*options)

type options struct {
	getenv func(string) string
	client *http.Client

	// root is the module whose targets are fuzzed; empty means the module
	// containing the working directory.
	root string
	// fuzzCache is where the corpus commands read and write go test's fuzz
	// cache; empty means $(go env GOCACHE)/fuzz. Tests set it so they leave
	// the real cache alone.
	fuzzCache string
}

// reportOptions configures `fuzz report`.
type reportOptions struct {
	targets string
	fuzz    string
	time    string
	results string // where the fuzz jobs' artifacts were downloaded, or empty
}

func defaultOptions() *options {
	return &options{
		getenv: os.Getenv,
		client: &http.Client{Timeout: time.Minute},
	}
}

func defaultRunOptions() cmdflag.Run {
	return cmdflag.Run{
		Time:    "10s",
		CPU:     2,
		Timeout: 30 * time.Minute,
		Results: "fuzz-results",
	}
}

// WithRoot sets the module whose fuzz targets are discovered. It defaults
// to the module containing the working directory.
func WithRoot(dir string) Option {
	return func(o *options) { o.root = dir }
}

// withFuzzCache sets the directory the corpus commands use as go test's
// fuzz cache.
func withFuzzCache(dir string) Option {
	return func(o *options) { o.fuzzCache = dir }
}

// WithGetenv sets how the commands read their FUZZ_* variables and the
// GitHub Actions environment. It defaults to os.Getenv; a nil func keeps
// the default.
func WithGetenv(getenv func(string) string) Option {
	return func(o *options) {
		if getenv != nil {
			o.getenv = getenv
		}
	}
}

// WithHTTPClient sets the client `fuzz report` calls the GitHub API with.
// A nil client keeps the default.
func WithHTTPClient(client *http.Client) Option {
	return func(o *options) {
		if client != nil {
			o.client = client
		}
	}
}
