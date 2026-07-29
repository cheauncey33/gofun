package telemetry

import (
	"context"
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

func InjectMap(ctx context.Context) map[string]string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	if len(carrier) == 0 {
		return nil
	}
	return map[string]string(carrier)
}

func ExtractMap(ctx context.Context, values map[string]string) context.Context {
	if len(values) == 0 {
		return ctx
	}
	return otel.GetTextMapPropagator().Extract(ctx, propagation.MapCarrier(values))
}

type AMQPHeadersCarrier amqp.Table

func (c AMQPHeadersCarrier) Get(key string) string {
	value, ok := c[key]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func (c AMQPHeadersCarrier) Set(key, value string) {
	c[key] = value
}

func (c AMQPHeadersCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for key := range c {
		keys = append(keys, key)
	}
	return keys
}

func InjectAMQP(ctx context.Context) amqp.Table {
	carrier := AMQPHeadersCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return amqp.Table(carrier)
}

func ExtractAMQP(ctx context.Context, headers amqp.Table, fallback map[string]string) context.Context {
	if len(headers) > 0 {
		extracted := otel.GetTextMapPropagator().Extract(ctx, AMQPHeadersCarrier(headers))
		if trace.SpanContextFromContext(extracted).IsValid() {
			return extracted
		}
	}
	return ExtractMap(ctx, fallback)
}
