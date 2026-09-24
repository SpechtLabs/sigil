package version

import (
	"bytes"
	"runtime/debug"
	"testing"

	"github.com/spechtlabs/sigil/cmd/sigil/internal/output"
)

var testBuildInfo = &debug.BuildInfo{
	GoVersion: "go1.26.6",
	Main:      debug.Module{Path: "github.com/spechtlabs/sigil", Version: "v0.0.0-20260102030405-abc123def456+dirty"},
	Settings: []debug.BuildSetting{
		{Key: "GOOS", Value: "linux"},
		{Key: "GOARCH", Value: "arm64"},
		{Key: "vcs.revision", Value: "abc123def456"},
		{Key: "vcs.time", Value: "2026-01-02T03:04:05Z"},
		{Key: "vcs.modified", Value: "true"},
	},
}

func execute(t *testing.T, opts ...Option) string {
	t.Helper()

	cmd := NewCommand(opts...)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return out.String()
}

func TestVersionFormats(t *testing.T) {
	tests := []struct {
		format output.Format
		want   string
	}{
		{
			format: output.Text,
			want: "Version:     1.2.3\n" +
				"Commit:      abc123def456 (dirty)\n" +
				"Commit time: 2026-01-02T03:04:05Z\n" +
				"Go version:  go1.26.6\n" +
				"Platform:    linux/arm64\n",
		},
		{
			format: output.JSON,
			want:   `{"version":"1.2.3","commit":"abc123def456","commitTime":"2026-01-02T03:04:05Z","dirty":true,"goVersion":"go1.26.6","platform":"linux/arm64"}` + "\n",
		},
		{
			format: output.YAML,
			want: "---\n" +
				"version: \"1.2.3\"\n" +
				"commit: \"abc123def456\"\n" +
				"commitTime: \"2026-01-02T03:04:05Z\"\n" +
				"dirty: true\n" +
				"goVersion: \"go1.26.6\"\n" +
				"platform: \"linux/arm64\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			got := execute(t, WithVersion("1.2.3"), WithBuildInfo(testBuildInfo), WithOutput(&tt.format))
			if got != tt.want {
				t.Errorf("output = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestVersionRejectsUnknownFormat(t *testing.T) {
	format := output.Format("xml")
	cmd := NewCommand(WithOutput(&format))
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err == nil {
		t.Fatal("Execute() error = nil, want error for unknown output format")
	}
}

func TestNewInfo(t *testing.T) {
	tests := []struct {
		name string
		opts options
		want info
	}{
		{
			name: "falls back to module version when no version is injected",
			opts: options{buildInfo: testBuildInfo},
			want: info{
				Version:    "v0.0.0-20260102030405-abc123def456+dirty",
				Commit:     "abc123def456",
				CommitTime: "2026-01-02T03:04:05Z",
				Dirty:      true,
				GoVersion:  "go1.26.6",
				Platform:   "linux/arm64",
			},
		},
		{
			name: "no VCS info, as with go run",
			opts: options{version: "1.2.3", buildInfo: &debug.BuildInfo{GoVersion: "go1.26.6", Main: debug.Module{Version: "(devel)"}}},
			want: info{Version: "1.2.3", Commit: unknown, CommitTime: unknown, GoVersion: "go1.26.6", Platform: unknown},
		},
		{
			name: "no build info at all",
			opts: options{},
			want: info{Version: unknown, Commit: unknown, CommitTime: unknown, GoVersion: unknown, Platform: unknown},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := newInfo(tt.opts); got != tt.want {
				t.Errorf("newInfo() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
