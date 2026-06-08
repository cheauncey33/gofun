package service

import (
	"WHU_Snack_GO/config"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type OrderConsumerService struct {
	newMQChannel   func() (*amqp.Channel, error)
	queueName      string
	retryQueueName string
	dlxName        string
	dlqName        string
	orderSvc       *OrderService
}

func NewOrderConsumerService(newMQChannel func() (*amqp.Channel, error), queueName string, orderSvc *OrderService) *OrderConsumerService {
	return &OrderConsumerService{
		newMQChannel:   newMQChannel,
		queueName:      queueName,
		retryQueueName: queueName + ".retry",
		dlxName:        queueName + ".dlx",
		dlqName:        queueName + ".dlq",
		orderSvc:       orderSvc,
	}
}

func (s *OrderConsumerService) SetDeadLetterConfig(retryQueueName, dlxName, dlqName string) {
	if retryQueueName != "" {
		s.retryQueueName = retryQueueName
	}
	if dlxName != "" {
		s.dlxName = dlxName
	}
	if dlqName != "" {
		s.dlqName = dlqName
	}
}

func (s *OrderConsumerService) Start(ctx context.Context, cfg config.OrderConsumerConfig) {
	if cfg.WorkerCount <= 0 {
		cfg.WorkerCount = 4
	}
	if cfg.PrefetchCount <= 0 {
		cfg.PrefetchCount = 5
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}

	log.Printf("order consumers starting: workers=%d prefetch=%d max_retries=%d", cfg.WorkerCount, cfg.PrefetchCount, cfg.MaxRetries)

	var wg sync.WaitGroup
	for i := 0; i < cfg.WorkerCount; i++ {
		workerID := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runWorker(ctx, workerID, cfg.PrefetchCount, cfg.MaxRetries)
		}()
	}

	<-ctx.Done()
	wg.Wait()
	log.Println("all order consumers exited")
}

func (s *OrderConsumerService) runWorker(ctx context.Context, workerID, prefetchCount, maxRetries int) {
	for {
		select {
		case <-ctx.Done():
			log.Printf("order consumer worker %d received cancel, exiting", workerID)
			return
		default:
			if err := s.consumeLoop(ctx, workerID, prefetchCount, maxRetries); err != nil {
				log.Printf("order consumer worker %d error, reconnecting in 3s: %v\n", workerID, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}
	}
}

func (s *OrderConsumerService) consumeLoop(ctx context.Context, workerID, prefetchCount, maxRetries int) error {
	ch, err := s.newMQChannel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.Qos(prefetchCount, 0, false); err != nil {
		return fmt.Errorf("consumer worker %d qos error: %w", workerID, err)
	}

	msgs, err := ch.Consume(
		s.queueName,
		fmt.Sprintf("order-worker-%d", workerID),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consumer worker %d channel error: %w", workerID, err)
	}
	log.Printf("order consumer worker %d listening on %s", workerID, s.queueName)

	retryMsgs, err := ch.Consume(
		s.retryQueueName,
		fmt.Sprintf("order-retry-worker-%d", workerID),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return fmt.Errorf("consumer worker %d retry channel error: %w", workerID, err)
	}
	log.Printf("order consumer worker %d listening on retry queue %s", workerID, s.retryQueueName)

	for {
		select {
		case <-ctx.Done():
			log.Printf("order consumer worker %d exiting via context", workerID)
			return nil
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("consumer worker %d MQ channel closed", workerID)
			}
			if err := s.handleDelivery(ctx, ch, workerID, maxRetries, d); err != nil {
				return err
			}
		case d, ok := <-retryMsgs:
			if !ok {
				return fmt.Errorf("consumer worker %d retry MQ channel closed", workerID)
			}
			if err := s.handleDelivery(ctx, ch, workerID, maxRetries, d); err != nil {
				return err
			}
		}
	}
}

func (s *OrderConsumerService) handleDelivery(ctx context.Context, ch *amqp.Channel, workerID, maxRetries int, d amqp.Delivery) error {
	var msg OrderMessage
	if err := json.Unmarshal(d.Body, &msg); err != nil {
		log.Printf("order consumer worker %d parsing error: %v\n", workerID, err)
		return d.Ack(false)
	}
	if err := s.orderSvc.ProcessOrderTask(msg); err != nil {
		log.Printf("order consumer worker %d order %d processing failed: %v\n", workerID, msg.OrderID, err)
		if errors.Is(err, ErrOrderNonRetryable) {
			s.orderSvc.RollbackReservedStock(msg)
			log.Printf("order consumer worker %d order %d non-retryable, reserved stock rolled back\n", workerID, msg.OrderID)
			return d.Ack(false)
		}
		if handleErr := s.handleFailedDelivery(ctx, ch, d, maxRetries); handleErr != nil {
			return fmt.Errorf("consumer worker %d failed to handle retry: %w", workerID, handleErr)
		}
		return nil
	}
	return d.Ack(false)
}

func (s *OrderConsumerService) handleFailedDelivery(ctx context.Context, ch *amqp.Channel, d amqp.Delivery, maxRetries int) error {
	retryCount := readRetryCount(d.Headers)
	if retryCount >= maxRetries {
		headers := cloneHeaders(d.Headers)
		headers["x-retry-count"] = retryCount
		headers["x-dead-reason"] = "max retries exceeded"
		if err := ch.PublishWithContext(ctx, s.dlxName, s.dlqName, true, false, amqp.Publishing{
			ContentType:  d.ContentType,
			Body:         d.Body,
			DeliveryMode: amqp.Persistent,
			Headers:      headers,
		}); err != nil {
			return err
		}
		return d.Ack(false)
	}

	headers := cloneHeaders(d.Headers)
	headers["x-retry-count"] = retryCount + 1
	if err := ch.PublishWithContext(ctx, "", s.retryQueueName, true, false, amqp.Publishing{
		ContentType:  d.ContentType,
		Body:         d.Body,
		DeliveryMode: amqp.Persistent,
		Headers:      headers,
	}); err != nil {
		return err
	}
	return d.Ack(false)
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
