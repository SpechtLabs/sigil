package telemetry

import (
	"context"

	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// FromContext returns the process logger Setup installed, with the trace and
// span ids of ctx's span as fields when it has one, so every log line written
// while handling a request or a reload leads straight to its trace. The
// access log lines carry the same two fields.
func FromContext(ctx context.Context) *otelzap.Logger {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return otelzap.L()
	}
	return otelzap.L().WithOptions(zap.Fields(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	))
}

// Log writes msg at level through [FromContext], for a line whose level
// depends on what happened. It calls the level's own method rather than
// otelzap's LogContext, which appends the logger's fields a second time, so
// the line would carry trace_id and span_id twice. The line's caller is
// Log's caller, not Log.
func Log(ctx context.Context, level zapcore.Level, msg string, fields ...zap.Field) {
	logger := FromContext(ctx).WithOptions(zap.AddCallerSkip(1))
	write := logger.InfoContext
	switch {
	case level >= zapcore.ErrorLevel:
		write = logger.ErrorContext
	case level == zapcore.WarnLevel:
		write = logger.WarnContext
	case level <= zapcore.DebugLevel:
		write = logger.DebugContext
	}
	write(ctx, msg, fields...)
}
