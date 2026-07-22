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
		return delivery.Ack(false)
	}
	err := c.service.ProcessOrderTask(ctx, message)
	if err == nil {
		return delivery.Ack(false)
	}
	if errors.Is(err, ErrTicketOrderNonRetryable) {
		c.service.FinalizeFailedMessage(ctx, message, err.Error())
		return delivery.Ack(false)
	}

	retryCount := readRetryCount(delivery.Headers)
	if retryCount >= maxRetries {
		headers := cloneHeaders(delivery.Headers)
		headers["x-retry-count"] = retryCount
		headers["x-dead-reason"] = err.Error()
		if publishErr := channel.PublishWithContext(
			ctx, c.dlxName, c.dlqName, true, false,
			amqp.Publishing{
				ContentType:  "application/json",
				Body:         delivery.Body,
				DeliveryMode: amqp.Persistent,
				Headers:      headers,
			},
		); publishErr != nil {
			return publishErr
		}
		c.service.FinalizeFailedMessage(ctx, message, "消息消费超过最大重试次数")
		return delivery.Ack(false)
	}

	headers := cloneHeaders(delivery.Headers)
	headers["x-retry-count"] = retryCount + 1
	if err := channel.PublishWithContext(
		ctx, "", c.retryQueueName, true, false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         delivery.Body,
			DeliveryMode: amqp.Persistent,
			Headers:      headers,
		},
	); err != nil {
		return err
	}
	return delivery.Ack(false)
}
