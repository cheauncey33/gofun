package common

import (
	"fmt"

	amqp "github.com/rabbitmq/amqp091-go"
)

var MQConn *amqp.Connection
var MQChannel *amqp.Channel

func InitRabbitMQ() {
	conn, err := amqp.Dial("amqp://guest:guest@localhost:5672/")
	if err != nil {
		panic(fmt.Errorf("rabbitMQ connection error:%w", err))
	}
	MQConn = conn
	ch, err := conn.Channel()
	if err != nil {
		panic(fmt.Errorf("rabbit getting channel error:%w", err))
	}
	MQChannel = ch
	// 声明一个名为 "order_queue" 的队列，准备随时接信
	_, err = MQChannel.QueueDeclare(
		"order_queue", // 队列组名
		true,          // 是否持久化
		false,         // 是否自动删除
		false,         // 是否排他
		false,         // 是否不等待
		nil,           // 额外属性
	)
	if err != nil {
		panic(fmt.Errorf("🐰 声明接头暗号队列失败: %w", err))
	}
	fmt.Println("Success to init rabbitMQ")
}
