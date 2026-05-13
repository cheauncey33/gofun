package common

import (
	"WHU_Snack_GO/config"
	"context"
	"fmt"
	"sync"

	amqp "github.com/rabbitmq/amqp091-go"
)

var MQConn *amqp.Connection
var MQChannel *amqp.Channel
var MQQueueName string

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
	_, err = MQChannel.QueueDeclare(
		cfg.QueueName,
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		panic(fmt.Errorf("声明队列失败: %w", err))
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
	if _, err := ch.QueueDeclare(
		MQQueueName,
		true,
		false,
		false,
		false,
		nil,
	); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("声明队列失败: %w", err)
	}
	return ch, nil
}

func PublishPersistent(ctx context.Context, body []byte) error {
	if MQChannel == nil {
		return fmt.Errorf("rabbitMQ publish channel is not initialized")
	}
	publishMu.Lock()
	defer publishMu.Unlock()

	return MQChannel.PublishWithContext(
		ctx,
		"",
		MQQueueName,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
		},
	)
}
