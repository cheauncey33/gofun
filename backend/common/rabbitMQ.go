package common

import (
	"WHU_Snack_GO/config"
	"context"
	"fmt"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

var MQConn *amqp.Connection
var MQChannel *amqp.Channel
var MQQueueName string
var MQRetryQueueName string
var MQDLXName string
var MQDLQName string

var publishMu sync.Mutex

func InitRabbitMQ(cfg config.RabbitMQConfig) {
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		panic(fmt.Errorf("rabbitMQ connection error:%w", err))
	}
	MQConn = conn
	MQQueueName = cfg.QueueName
	ch, err := conn.Channel()
	if err != nil {
		panic(fmt.Errorf("rabbit getting channel error:%w", err))
	}
	MQChannel = ch
	MQRetryQueueName = cfg.RetryQueueName
	MQDLXName = cfg.DLXName
	MQDLQName = cfg.DLQName
	if err := declareOrderQueues(MQChannel, cfg.QueueName, cfg.RetryQueueName, cfg.DLXName, cfg.DLQName); err != nil {
		panic(err)
	}
	if err := MQChannel.Confirm(false); err != nil {
		panic(fmt.Errorf("开启发布确认失败: %w", err))
	}
	fmt.Println("Success to init rabbitMQ")
}

func NewMQChannel() (*amqp.Channel, error) {
	if MQConn == nil {
		return nil, fmt.Errorf("rabbitMQ connection is not initialized")
	}
	ch, err := MQConn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbit getting channel error:%w", err)
	}
	if err := declareOrderQueues(ch, MQQueueName, MQRetryQueueName, MQDLXName, MQDLQName); err != nil {
		_ = ch.Close()
		return nil, err
	}
	return ch, nil
}

func PublishPersistent(ctx context.Context, body []byte) error {
	if MQChannel == nil {
		return fmt.Errorf("rabbitMQ publish channel is not initialized")
	}
	publishMu.Lock()
	defer publishMu.Unlock()

	confirm, err := MQChannel.PublishWithDeferredConfirmWithContext(
		ctx,
		"",
		MQQueueName,
		true,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
		},
	)
	if err != nil {
		return err
	}
	if confirm == nil {
		return fmt.Errorf("rabbitMQ publish confirm is not enabled")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	acked, err := confirm.WaitContext(waitCtx)
	if err != nil {
		return err
	}
	if !acked {
		return fmt.Errorf("rabbitMQ publish not acknowledged")
	}
	return nil
}

func declareOrderQueues(ch *amqp.Channel, queueName, retryQueueName, dlxName, dlqName string) error {
	if queueName == "" {
		return fmt.Errorf("rabbitMQ queue name is empty")
	}
	if retryQueueName == "" {
		retryQueueName = queueName + ".retry"
	}
	if dlxName == "" {
		dlxName = queueName + ".dlx"
	}
	if dlqName == "" {
		dlqName = queueName + ".dlq"
	}
	if err := ch.ExchangeDeclare(dlxName, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明死信交换机失败: %w", err)
	}
	if _, err := ch.QueueDeclare(dlqName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明死信队列失败: %w", err)
	}
	if err := ch.QueueBind(dlqName, dlqName, dlxName, false, nil); err != nil {
		return fmt.Errorf("绑定死信队列失败: %w", err)
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明队列失败: %w", err)
	}
	if _, err := ch.QueueDeclare(retryQueueName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明重试队列失败: %w", err)
	}
	return nil
}
