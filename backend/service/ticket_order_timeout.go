package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"gofun/metrics"
	"gofun/models"
	apptelemetry "gofun/pkg/telemetry"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

// 票务支付超时：延时队列（消息级 TTL）→ DLX → 超时队列。
// 与其他订单流的延时队列隔离，避免混用。
const (
	defaultTicketTimeoutExchange = "fuchang.order.timeout.ex"
	defaultTicketDelayQueue      = "fuchang.order.delay"
	defaultTicketTimeoutQueue    = "fuchang.order.timeout"
	defaultTicketTimeoutRK       = "timeout"
)

type TicketPaymentTimeoutMessage struct {
	OrderID      int64             `json:"order_id"`
	UserID       int64             `json:"user_id"`
	TraceContext map[string]string `json:"trace_context,omitempty"`
}

type ticketTimeoutInfra struct {
	exchange   string
	delayQueue string
	timeoutQ   string
	routingKey string
}

func (s *TicketOrderService) timeoutInfra() ticketTimeoutInfra {
	ex := s.timeoutExchange
	if ex == "" {
		ex = defaultTicketTimeoutExchange
	}
	delay := s.timeoutDelayQueue
	if delay == "" {
		delay = defaultTicketDelayQueue
	}
	timeoutQ := s.timeoutQueue
	if timeoutQ == "" {
		timeoutQ = defaultTicketTimeoutQueue
	}
	rk := s.timeoutRoutingKey
	if rk == "" {
		rk = defaultTicketTimeoutRK
	}
	return ticketTimeoutInfra{exchange: ex, delayQueue: delay, timeoutQ: timeoutQ, routingKey: rk}
}

// SetupPaymentTimeoutInfrastructure 声明延时队 + 超时队（消息级 TTL，避免队头阻塞）。
func (s *TicketOrderService) SetupPaymentTimeoutInfrastructure() error {
	if s.newMQChannel == nil {
		return fmt.Errorf("rabbitMQ channel factory is not initialized")
	}
	ch, err := s.newMQChannel()
	if err != nil {
		return fmt.Errorf("ticket timeout setup: channel: %w", err)
	}
	defer ch.Close()

	infra := s.timeoutInfra()
	if err := ch.ExchangeDeclare(infra.exchange, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("ticket timeout setup: exchange: %w", err)
	}
	args := amqp.Table{
		"x-dead-letter-exchange":    infra.exchange,
		"x-dead-letter-routing-key": infra.routingKey,
	}
	if s.mqQueueType == "quorum" {
		args["x-queue-type"] = "quorum"
	}
	if _, err := ch.QueueDeclare(infra.delayQueue, true, false, false, false, args); err != nil {
		return fmt.Errorf("ticket timeout setup: delay queue: %w", err)
	}
	timeoutArgs := amqp.Table(nil)
	if s.mqQueueType == "quorum" {
		timeoutArgs = amqp.Table{"x-queue-type": "quorum"}
	}
	if _, err := ch.QueueDeclare(infra.timeoutQ, true, false, false, false, timeoutArgs); err != nil {
		return fmt.Errorf("ticket timeout setup: timeout queue: %w", err)
	}
	if err := ch.QueueBind(infra.timeoutQ, infra.routingKey, infra.exchange, false, nil); err != nil {
		return fmt.Errorf("ticket timeout setup: bind: %w", err)
	}
	return nil
}

func paymentTimeoutExpirationMs(d time.Duration) string {
	if d < time.Second {
		return "1000"
	}
	return strconv.FormatInt(d.Milliseconds(), 10)
}

// PublishPaymentTimeout 在订单进入 pending_payment 后投递延时关单消息。
func (s *TicketOrderService) PublishPaymentTimeout(ctx context.Context, orderID, userID int64) error {
	if s.newMQChannel == nil {
		return fmt.Errorf("rabbitMQ channel factory is not initialized")
	}
	ch, err := s.newMQChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	ctx, span := otel.Tracer("gofun-ticketing/order").Start(
		ctx,
		"ticket.payment_timeout.publish",
		trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.Int64("ticket.order.id", orderID)),
	)
	defer span.End()
	body, err := json.Marshal(TicketPaymentTimeoutMessage{
		OrderID:      orderID,
		UserID:       userID,
		TraceContext: apptelemetry.InjectMap(ctx),
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	ttlMs := paymentTimeoutExpirationMs(s.paymentTimeout)
	infra := s.timeoutInfra()
	return ch.PublishWithContext(
		ctx,
		"",
		infra.delayQueue,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
			Expiration:   ttlMs,
			Headers:      apptelemetry.InjectAMQP(ctx),
		},
	)
}

// StartPaymentTimeoutConsumer 消费超时队列，条件取消仍待支付订单。
func (s *TicketOrderService) StartPaymentTimeoutConsumer(ctx context.Context, workerCount int) {
	if workerCount <= 0 {
		workerCount = 2
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		workerID := i + 1
		go func() {
			defer wg.Done()
			s.runPaymentTimeoutWorker(ctx, workerID)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	log.Println("ticket payment timeout workers exited")
}

func (s *TicketOrderService) runPaymentTimeoutWorker(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if err := s.consumePaymentTimeoutLoop(ctx, workerID); err != nil {
				log.Printf("ticket timeout worker %d: %v (reconnect in 3s)", workerID, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}
	}
}

func (s *TicketOrderService) consumePaymentTimeoutLoop(ctx context.Context, workerID int) error {
	ch, err := s.newMQChannel()
	if err != nil {
		return err
	}
	defer ch.Close()
	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}
	infra := s.timeoutInfra()
	msgs, err := ch.Consume(
		infra.timeoutQ,
		fmt.Sprintf("fuchang-timeout-%d", workerID),
		false, false, false, false, nil,
	)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("timeout channel closed")
			}
			var msg TicketPaymentTimeoutMessage
			if err := json.Unmarshal(d.Body, &msg); err != nil {
				metrics.TicketTimeoutMessages.WithLabelValues("malformed").Inc()
				_ = d.Ack(false)
				continue
			}
			timeoutCtx := apptelemetry.ExtractAMQP(ctx, d.Headers, msg.TraceContext)
			timeoutCtx, span := otel.Tracer("gofun-ticketing/order").Start(
				timeoutCtx,
				"ticket.payment_timeout.consume",
				trace.WithSpanKind(trace.SpanKindConsumer),
				trace.WithAttributes(attribute.Int64("ticket.order.id", msg.OrderID)),
			)
			if err := s.ProcessPaymentTimeout(timeoutCtx, msg.OrderID, msg.UserID); err != nil {
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				span.End()
				log.Printf("ticket timeout worker %d order %d: %v", workerID, msg.OrderID, err)
				if !shouldRequeuePaymentTimeout(err) {
					log.Printf("ticket timeout worker %d order %d: permanent failure, discard message", workerID, msg.OrderID)
					metrics.TicketTimeoutMessages.WithLabelValues("permanent_discard").Inc()
					_ = d.Ack(false)
					continue
				}
				metrics.TicketTimeoutMessages.WithLabelValues("retry").Inc()
				_ = d.Nack(false, true)
				continue
			}
			span.End()
			metrics.TicketTimeoutMessages.WithLabelValues("success").Inc()
			_ = d.Ack(false)
		}
	}
}

func shouldRequeuePaymentTimeout(err error) bool {
	return classifyPaymentTimeoutError(err) == paymentTimeoutErrorTransient
}

type paymentTimeoutErrorClass string

const (
	paymentTimeoutErrorNone      paymentTimeoutErrorClass = "none"
	paymentTimeoutErrorPermanent paymentTimeoutErrorClass = "permanent"
	paymentTimeoutErrorTransient paymentTimeoutErrorClass = "transient"
)

func classifyPaymentTimeoutError(err error) paymentTimeoutErrorClass {
	if err == nil {
		return paymentTimeoutErrorNone
	}
	if errors.Is(err, ErrTicketOrderNonRetryable) {
		return paymentTimeoutErrorPermanent
	}
	return paymentTimeoutErrorTransient
}

// ProcessPaymentTimeout 仅取消仍为 pending_payment 且已到期的订单（与支付竞态安全）。
func (s *TicketOrderService) ProcessPaymentTimeout(ctx context.Context, orderID, userID int64) error {
	db := s.asyncDB()
	var order models.TicketOrder
	err := db.WithContext(ctx).First(&order, orderID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if order.UserID != userID {
		return nil
	}
	if order.Status != models.TicketOrderStatusPendingPayment {
		return nil
	}
	if s.paymentWindowOpen(&order) {
		// 时钟回拨或 TTL 略早于 expires_at：交给扫描器兜底，本消息直接丢弃。
		return nil
	}
	err = s.cancelPendingPaymentOnlyDB(ctx, db, userID, orderID, "支付超时自动取消")
	if errors.Is(err, ErrTicketOrderState) || errors.Is(err, ErrTicketOrderNotFound) {
		return nil
	}
	return err
}
