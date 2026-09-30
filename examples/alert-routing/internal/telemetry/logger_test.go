package telemetry

import (
	"context"
	"strings"
	"testing"

	"github.com/spechtlabs/go-otel-utils/otelzap"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// TestFromContext checks that a line logged in a span carries its trace and
// span ids, and one logged outside any span carries neither.
func TestFromContext(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	traced, span := tp.Tracer("test").Start(context.Background(), "alertrouter.route")
	defer span.End()

	tests := []struct {
		name      string
		ctx       context.Context
		wantTrace string
	}{
		{name: "in a span", ctx: traced, wantTrace: span.SpanContext().TraceID().String()},
		{name: "outside any span", ctx: context.Background()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.InfoLevel)
			t.Cleanup(otelzap.ReplaceGlobals(otelzap.New(zap.New(core))))

			FromContext(tt.ctx).InfoContext(tt.ctx, "alert routed")

			fields := logs.All()[0].ContextMap()
			if got, _ := fields["trace_id"].(string); got != tt.wantTrace {
				t.Errorf("trace_id = %q, want %q", got, tt.wantTrace)
			}
			if _, ok := fields["span_id"]; ok != (tt.wantTrace != "") {
				t.Errorf("span_id present = %v, want %v", ok, tt.wantTrace != "")
			}
		})
	}
}

// TestLog checks that each level lands at that level, with the trace and
// span ids once each.
func TestLog(t *testing.T) {
	tp := sdktrace.NewTracerProvider()
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	ctx, span := tp.Tracer("test").Start(context.Background(), "alertrouter.route")
	defer span.End()

	for _, level := range []zapcore.Level{zapcore.DebugLevel, zapcore.InfoLevel, zapcore.WarnLevel, zapcore.ErrorLevel} {
		t.Run(level.String(), func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			t.Cleanup(otelzap.ReplaceGlobals(otelzap.New(zap.New(core, zap.AddCaller()), otelzap.WithMinLevel(zapcore.DebugLevel), otelzap.WithCaller(true))))

			Log(ctx, level, "alert routed", zap.String("status", "routed"))

			entries := logs.All()
			if len(entries) != 1 || entries[0].Level != level {
				t.Fatalf("entries = %+v, want one at %v", entries, level)
			}
			if file := entries[0].Caller.File; !strings.HasSuffix(file, "logger_test.go") {
				t.Errorf("caller = %s, want the line that called Log", file)
			}
			seen := map[string]int{}
			for _, f := range entries[0].Context {
				seen[f.Key]++
			}
			if seen["trace_id"] != 1 || seen["span_id"] != 1 || seen["status"] != 1 {
				t.Errorf("field counts = %v, want trace_id, span_id and status once each", seen)
			}
		})
	}
}
