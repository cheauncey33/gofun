package metrics

import (
	"context"
	"log"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

const runtimeMetricsInterval = 5 * time.Second

type mysqlStatusRow struct {
	VariableName string `gorm:"column:Variable_name"`
	Value        string `gorm:"column:Value"`
}

// StartRuntimeCollector periodically samples infrastructure state which is not
// observable from HTTP middleware: InnoDB row lock waits and RabbitMQ backlog.
func StartRuntimeCollector(
	ctx context.Context,
	db *gorm.DB,
	newMQChannel func() (*amqp.Channel, error),
	queueName string,
) {
	ticker := time.NewTicker(runtimeMetricsInterval)
	defer ticker.Stop()

	collectRuntimeMetrics(ctx, db, newMQChannel, queueName)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectRuntimeMetrics(ctx, db, newMQChannel, queueName)
		}
	}
}

func collectRuntimeMetrics(
	ctx context.Context,
	db *gorm.DB,
	newMQChannel func() (*amqp.Channel, error),
	queueName string,
) {
	var status mysqlStatusRow
	if err := db.WithContext(ctx).
		Raw("SHOW GLOBAL STATUS LIKE 'Innodb_row_lock_waits'").
		Scan(&status).Error; err != nil {
		log.Printf("collect Innodb_row_lock_waits: %v", err)
	} else if value, err := strconv.ParseFloat(status.Value, 64); err == nil {
		MySQLInnoDBRowLockWaits.Set(value)
	}

	if newMQChannel == nil || queueName == "" {
		return
	}
	channel, err := newMQChannel()
	if err != nil {
		log.Printf("collect RabbitMQ queue metrics: %v", err)
		return
	}
	defer channel.Close()
	queue, err := channel.QueueInspect(queueName)
	if err != nil {
		log.Printf("inspect RabbitMQ queue %s: %v", queueName, err)
		return
	}
	MQQueueReadyMessages.WithLabelValues(queueName).Set(float64(queue.Messages))
	MQQueueConsumers.WithLabelValues(queueName).Set(float64(queue.Consumers))
}
