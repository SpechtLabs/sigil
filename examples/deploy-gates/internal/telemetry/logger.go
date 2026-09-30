package telemetry

import (
	"context"

	"github.com/spechtlabs/go-otel-utils/otelzap"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
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
