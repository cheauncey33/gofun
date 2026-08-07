package service

import (
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/metrics"
	apptelemetry "WHU_Snack_GO/pkg/telemetry"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type TicketOrderConsumer struct {
	newMQChannel   func() (*amqp.Channel, error)
	queueName      string
	retryQueueName string
	dlxName        string
	dlqName        string
	service        *TicketOrderService
}

func NewTicketOrderConsumer(
	newMQChannel func() (*amqp.Channel, error),
	queueName, retryQueueName, dlxName, dlqName string,
	service *TicketOrderService,
) *TicketOrderConsumer {
	return &TicketOrderConsumer{
		newMQChannel:   newMQChannel,
		queueName:      queueName,
		retryQueueName: retryQueueName,
		dlxName:        dlxName,
		dlqName:        dlqName,
		service:        service,
	}
}

func (c *TicketOrderConsumer) Start(ctx context.Context, cfg config.OrderConsumerConfig) {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 4
	}
	if cfg.PrefetchCount <= 0 {
		cfg.PrefetchCount = 5
	}
	var wg sync.WaitGroup
	for workerID := 1; workerID <= cfg.WorkerCount; workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			c.runWorker(ctx, id, cfg.PrefetchCount, cfg.MaxRetries)
		}(workerID)
	}
	<-ctx.Done()
	wg.Wait()
}

func (c *TicketOrderConsumer) runWorker(ctx context.Context, workerID, prefetch, maxRetries int) {
	for ctx.Err() == nil {
		if err := c.consume(ctx, workerID, prefetch, maxRetries); err != nil {
			log.Printf("ticket order worker %d: %v", workerID, err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(3 * time.Second):
		}
	}
}

func (c *TicketOrderConsumer) consume(
	ctx context.Context,
	workerID, prefetch, maxRetries int,
) error {
	channel, err := c.newMQChannel()
	if err != nil {
		return err
	}
	defer channel.Close()
	if err := channel.Qos(prefetch, 0, false); err != nil {
		return err
	}
	deliveries, err := channel.Consume(
		c.queueName,
		fmt.Sprintf("fuchang-order-%d", workerID),
		false, false, false, false, nil,
	)
	if err != nil {
		return err
	}
	retries, err := channel.Consume(
		c.retryQueueName,
		fmt.Sprintf("fuchang-order-retry-%d", workerID),
		false, false, false, false, nil,
	)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case delivery, ok := <-deliveries:
			if !ok {
				return errors.New("票务订单消费通道已关闭")
			}
			if err := c.handle(ctx, channel, delivery, maxRetries); err != nil {
				return err
			}
		case delivery, ok := <-retries:
			if !ok {
				return errors.New("票务订单重试通道已关闭")
			}
			if err := c.handle(ctx, channel, delivery, maxRetries); err != nil {
				return err
			}
		}
	}
}

func (c *TicketOrderConsumer) handle(
	ctx context.Context,
	channel *amqp.Channel,
	delivery amqp.Delivery,
	maxRetries int,
) error {
	var message TicketOrderMessage
	if err := json.Unmarshal(delivery.Body, &message); err != nil {
		_, span := startTicketOrderConsumerSpan(ctx, delivery, message)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		span.End()
		metrics.MQMessagesConsumed.WithLabelValues("malformed").Inc()
		return delivery.Ack(false)
	}
	consumerCtx, span := startTicketOrderConsumerSpan(ctx, delivery, message)
	defer span.End()
	err := c.service.ProcessOrderTask(consumerCtx, message)
	if err == nil {
		metrics.MQMessagesConsumed.WithLabelValues("success").Inc()
		return delivery.Ack(false)
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	if errors.Is(err, ErrTicketOrderNonRetryable) {
		c.service.FinalizeFailedMessage(consumerCtx, message, err.Error())
		metrics.MQMessagesConsumed.WithLabelValues("permanent_error").Inc()
		return delivery.Ack(false)
	}

	retryCount := readRetryCount(delivery.Headers)
	if retryCount >= maxRetries {
		headers := cloneHeaders(delivery.Headers)
		headers["x-retry-count"] = retryCount
		headers["x-dead-reason"] = err.Error()
		copyTraceHeaders(headers, apptelemetry.InjectAMQP(consumerCtx))
		if publishErr := channel.PublishWithContext(
			consumerCtx, c.dlxName, c.dlqName, true, false,
			amqp.Publishing{
				ContentType:  "application/json",
				Body:         delivery.Body,
				DeliveryMode: amqp.Persistent,
				Headers:      headers,
			},
		); publishErr != nil {
			return publishErr
		}
		c.service.FinalizeFailedMessage(consumerCtx, message, "消息消费超过最大重试次数")
		metrics.MQMessagesConsumed.WithLabelValues("dead_letter").Inc()
		return delivery.Ack(false)
	}

	headers := cloneHeaders(delivery.Headers)
	headers["x-retry-count"] = retryCount + 1
	copyTraceHeaders(headers, apptelemetry.InjectAMQP(consumerCtx))
	if err := channel.PublishWithContext(
		consumerCtx, "", c.retryQueueName, true, false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         delivery.Body,
			DeliveryMode: amqp.Persistent,
			Headers:      headers,
		},
	); err != nil {
		return err
	}
	metrics.MQMessagesConsumed.WithLabelValues("retry").Inc()
	return delivery.Ack(false)
}

func startTicketOrderConsumerSpan(
	ctx context.Context,
	delivery amqp.Delivery,
	message TicketOrderMessage,
) (context.Context, trace.Span) {
	parentCtx := apptelemetry.ExtractAMQP(ctx, delivery.Headers, message.TraceContext)
	return otel.Tracer("fuchang-ticketing/order").Start(
		parentCtx,
		"ticket.order.consume",
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(
			attribute.Int64("ticket.order.id", message.OrderID),
			attribute.Int("messaging.retry_count", readRetryCount(delivery.Headers)),
		),
	)
}

func copyTraceHeaders(target, source amqp.Table) {
	for key, value := range source {
		target[key] = value
	}
}

func readRetryCount(headers amqp.Table) int {
	if headers == nil {
		return 0
	}
	switch v := headers["x-retry-count"].(type) {
	case int:
		return v
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	default:
		return 0
	}
}

func cloneHeaders(headers amqp.Table) amqp.Table {
	cloned := amqp.Table{}
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}
