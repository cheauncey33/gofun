package logger

import (
	"context"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type requestIDKey struct{}

func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// FromContext attaches correlation fields without retaining request payloads.
func FromContext(ctx context.Context) *zap.Logger {
	base := Log
	if base == nil {
		base = zap.NewNop()
	}
	fields := []zap.Field{}
	if id := RequestID(ctx); id != "" {
		fields = append(fields, zap.String("request_id", id))
	}
	sc := trace.SpanContextFromContext(ctx)
	if sc.IsValid() {
		fields = append(fields, zap.String("trace_id", sc.TraceID().String()), zap.String("span_id", sc.SpanID().String()))
	}
	return base.With(fields...)
}
