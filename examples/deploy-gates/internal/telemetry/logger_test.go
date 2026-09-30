package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestLog(t *testing.T) {
	traced := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID:    trace.TraceID{0x01},
		SpanID:     trace.SpanID{0x02},
		TraceFlags: trace.FlagsSampled,
	}))

	tests := []struct {
		name      string
		ctx       context.Context
		lvl       zapcore.Level
		wantLevel string
		wantIDs   bool
	}{
		{name: "debug", ctx: traced, lvl: zapcore.DebugLevel, wantLevel: "debug", wantIDs: true},
		{name: "info", ctx: traced, lvl: zapcore.InfoLevel, wantLevel: "info", wantIDs: true},
		{name: "warn", ctx: traced, lvl: zapcore.WarnLevel, wantLevel: "warn", wantIDs: true},
		{name: "error", ctx: traced, lvl: zapcore.ErrorLevel, wantLevel: "error", wantIDs: true},
		{name: "fatal is logged at error", ctx: traced, lvl: zapcore.FatalLevel, wantLevel: "error", wantIDs: true},
		{name: "no span", ctx: context.Background(), lvl: zapcore.WarnLevel, wantLevel: "warn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logs := captureLogs(t)
			Log(tt.ctx, tt.lvl, "evaluation failed", zap.String("team", "payments"))

			line := strings.TrimSpace(logs.String())
			if strings.Contains(line, "\n") {
				t.Fatalf("Log wrote more than one line:\n%s", line)
			}
			for _, key := range []string{"level", "caller", "msg", "team", "trace_id", "span_id"} {
				want := 1
				if !tt.wantIDs && (key == "trace_id" || key == "span_id") {
					want = 0
				}
				if got := strings.Count(line, `"`+key+`":`); got != want {
					t.Errorf("%q appears %d times, want %d, in %s", key, got, want, line)
				}
			}

			var entry map[string]any
			if err := json.Unmarshal([]byte(line), &entry); err != nil {
				t.Fatalf("the log line isn't JSON: %v\n%s", err, line)
			}
			if entry["level"] != tt.wantLevel {
				t.Errorf("level = %v, want %s", entry["level"], tt.wantLevel)
			}
			if caller, _ := entry["caller"].(string); !strings.Contains(caller, "logger_test.go") {
				t.Errorf("caller = %q, want the line that called Log, in logger_test.go", caller)
			}
			if tt.wantIDs && (entry["trace_id"] != "01000000000000000000000000000000" || entry["span_id"] != "0200000000000000") {
				t.Errorf("trace_id, span_id = %v, %v, want the span's ids", entry["trace_id"], entry["span_id"])
			}
		})
	}
}

// captureLogs installs an otelzap logger that writes JSON at debug level to
// the returned buffer, the way Setup's logger writes to stderr, and puts the
// previous one back when the test ends.
func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	core := zapcore.NewCore(zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig()), zapcore.AddSync(&buf), zapcore.DebugLevel)
	t.Cleanup(otelzap.ReplaceGlobals(otelzap.New(zap.New(core, zap.AddCaller()), otelzap.WithMinLevel(zapcore.DebugLevel))))
	return &buf
}
