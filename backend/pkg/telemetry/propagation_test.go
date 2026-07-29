package telemetry

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

func TestTraceContextSurvivesPayloadAndAMQPHeaders(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	ctx, span := provider.Tracer("test").Start(context.Background(), "http.request")
	source := trace.SpanContextFromContext(ctx)
	payload := InjectMap(ctx)
	headers := InjectAMQP(ctx)
	span.End()

	fromPayload := trace.SpanContextFromContext(ExtractMap(context.Background(), payload))
	if fromPayload.TraceID() != source.TraceID() {
		t.Fatalf("payload trace id = %s, want %s", fromPayload.TraceID(), source.TraceID())
	}
	fromHeaders := trace.SpanContextFromContext(ExtractAMQP(context.Background(), headers, nil))
	if fromHeaders.TraceID() != source.TraceID() {
		t.Fatalf("AMQP trace id = %s, want %s", fromHeaders.TraceID(), source.TraceID())
	}
}
