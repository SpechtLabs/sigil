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
//
// Log with its level methods, InfoContext and the like, or with [Log] when
// the level is only known at run time. otelzap's LogContext appends the
// logger's fields a second time, so its lines carry both ids twice.
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

// Log writes msg at lvl through [FromContext], for a line whose level is
// picked at run time, such as a failure logged at its classified level. It
// calls the level's own method rather than otelzap's LogContext, which would
// repeat the trace and span ids, and reports its caller as the line's caller.
// A level above error is logged at error: a log line never panics or exits.
func Log(ctx context.Context, lvl zapcore.Level, msg string, fields ...zap.Field) {
	l := FromContext(ctx).WithOptions(zap.AddCallerSkip(1))
	log := l.ErrorContext
	switch {
	case lvl <= zapcore.DebugLevel:
		log = l.DebugContext
	case lvl == zapcore.InfoLevel:
		log = l.InfoContext
	case lvl == zapcore.WarnLevel:
		log = l.WarnContext
	}
	log(ctx, msg, fields...)
}
