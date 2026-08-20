package metrics

import (
	"context"
	"database/sql"
	"log"
	"strconv"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

const runtimeMetricsInterval = 2 * time.Second

type mysqlStatusRow struct {
	VariableName string `gorm:"column:Variable_name"`
	Value        string `gorm:"column:Value"`
}

type ticketOutboxRuntimeRow struct {
	PendingRows      int64   `gorm:"column:pending_rows"`
	OldestAgeSeconds float64 `gorm:"column:oldest_age_seconds"`
}

// StartRuntimeCollector periodically samples infrastructure state which is not
// observable from HTTP middleware: InnoDB row lock waits, DB pools, threads and
// RabbitMQ backlog.
//
// httpDB is the request-path pool. workerDB is the Consumer/Outbox/Timeout pool;
// when isolation is disabled, pass the same pointer (or nil to skip worker labels).
func StartRuntimeCollector(
	ctx context.Context,
	httpDB *gorm.DB,
	workerDB *gorm.DB,
	newMQChannel func() (*amqp.Channel, error),
	queueNames ...string,
) {
	ticker := time.NewTicker(runtimeMetricsInterval)
	defer ticker.Stop()

	httpSQL := sqlDBFromGorm(httpDB)
	workerSQL := sqlDBFromGorm(workerDB)
	statusDB := httpDB
	if statusDB == nil {
		statusDB = workerDB
	}

	collectRuntimeMetrics(ctx, statusDB, httpSQL, workerSQL, newMQChannel, queueNames...)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collectRuntimeMetrics(ctx, statusDB, httpSQL, workerSQL, newMQChannel, queueNames...)
		}
	}
}

func sqlDBFromGorm(db *gorm.DB) *sql.DB {
	if db == nil {
		return nil
	}
	raw, err := db.DB()
	if err != nil {
		return nil
	}
	return raw
}

func collectRuntimeMetrics(
	ctx context.Context,
	statusDB *gorm.DB,
	httpSQL *sql.DB,
	workerSQL *sql.DB,
	newMQChannel func() (*amqp.Channel, error),
	queueNames ...string,
) {
	if httpSQL != nil {
		setSQLPoolGauges("http", httpSQL, true)
	}
	if workerSQL != nil && workerSQL != httpSQL {
		setSQLPoolGauges("worker", workerSQL, false)
	} else if httpSQL != nil {
		// Shared pool: expose the same stats under worker for simpler dashboards.
		setSQLPoolGauges("worker", httpSQL, false)
	}

	if statusDB != nil {
		setMySQLStatusGauge(ctx, statusDB, "Innodb_row_lock_waits", MySQLInnoDBRowLockWaits)
		setMySQLStatusGauge(ctx, statusDB, "Threads_running", MySQLThreadsRunning)
		setMySQLStatusGauge(ctx, statusDB, "Threads_connected", MySQLThreadsConnected)
		setTicketOutboxGauges(ctx, statusDB)
	}

	if newMQChannel == nil || len(queueNames) == 0 {
		return
	}
	channel, err := newMQChannel()
	if err != nil {
		log.Printf("collect RabbitMQ queue metrics: %v", err)
		return
	}
	defer channel.Close()
	for _, queueName := range queueNames {
		if queueName == "" {
			continue
		}
		queue, err := channel.QueueInspect(queueName)
		if err != nil {
			log.Printf("inspect RabbitMQ queue %s: %v", queueName, err)
			continue
		}
		MQQueueReadyMessages.WithLabelValues(queueName).Set(float64(queue.Messages))
		MQQueueConsumers.WithLabelValues(queueName).Set(float64(queue.Consumers))
	}
}

func setTicketOutboxGauges(ctx context.Context, db *gorm.DB) {
	var row ticketOutboxRuntimeRow
	err := db.WithContext(ctx).Raw(`
		SELECT
			COUNT(*) AS pending_rows,
			COALESCE(
				TIMESTAMPDIFF(MICROSECOND, MIN(create_time), CURRENT_TIMESTAMP(3)) / 1000000.0,
				0
			) AS oldest_age_seconds
		FROM ticket_order_outbox
		WHERE status IN ('pending', 'publishing')
	`).Scan(&row).Error
	if err != nil {
		log.Printf("collect ticket Outbox metrics: %v", err)
		return
	}
	TicketOutboxPendingRows.Set(float64(row.PendingRows))
	TicketOutboxOldestAgeSeconds.Set(row.OldestAgeSeconds)
}

func setSQLPoolGauges(pool string, sqlDB *sql.DB, mirrorUnlabeled bool) {
	stats := sqlDB.Stats()
	GoSQLDBOpenConnectionsByPool.WithLabelValues(pool).Set(float64(stats.OpenConnections))
	GoSQLDBInUseByPool.WithLabelValues(pool).Set(float64(stats.InUse))
	GoSQLDBIdleByPool.WithLabelValues(pool).Set(float64(stats.Idle))
	GoSQLDBWaitCountByPool.WithLabelValues(pool).Set(float64(stats.WaitCount))
	GoSQLDBWaitDurationSecondsByPool.WithLabelValues(pool).Set(stats.WaitDuration.Seconds())
	GoSQLDBMaxOpenConnectionsByPool.WithLabelValues(pool).Set(float64(stats.MaxOpenConnections))
	if mirrorUnlabeled {
		GoSQLDBOpenConnections.Set(float64(stats.OpenConnections))
		GoSQLDBInUse.Set(float64(stats.InUse))
		GoSQLDBIdle.Set(float64(stats.Idle))
		GoSQLDBWaitCount.Set(float64(stats.WaitCount))
		GoSQLDBWaitDurationSeconds.Set(stats.WaitDuration.Seconds())
		GoSQLDBMaxOpenConnections.Set(float64(stats.MaxOpenConnections))
	}
}

func setMySQLStatusGauge(ctx context.Context, db *gorm.DB, name string, gauge interface{ Set(float64) }) {
	var status mysqlStatusRow
	// SHOW ... LIKE does not reliably bind placeholders across drivers; name is a fixed literal.
	if err := db.WithContext(ctx).
		Raw("SHOW GLOBAL STATUS LIKE '" + name + "'").
		Scan(&status).Error; err != nil {
		log.Printf("collect %s: %v", name, err)
		return
	}
	if value, err := strconv.ParseFloat(status.Value, 64); err == nil {
		gauge.Set(value)
	}
}
