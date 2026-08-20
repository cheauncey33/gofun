package metrics

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ===== Metric Definitions =====

var (
	HTTPRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests handled",
		},
		[]string{"method", "path", "status"},
	)

	HTTPRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency in seconds",
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"method", "path"},
	)

	OrdersCreated = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "orders_created_total",
			Help: "Total number of orders created",
		},
	)

	OrdersCancelled = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "orders_cancelled_total",
			Help: "Total number of orders cancelled",
		},
	)

	PaymentCallbacksTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "payment_callbacks_total",
			Help: "Payment provider callbacks handled by result",
		},
		[]string{"provider", "result"},
	)

	PaymentCallbackDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "payment_callback_duration_seconds",
			Help:    "Payment provider callback processing latency",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"provider"},
	)

	TicketVerificationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ticket_verifications_total",
			Help: "Ticket verification attempts by result",
		},
		[]string{"result"},
	)

	ActiveHTTPConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "active_http_connections",
			Help: "Current number of active HTTP connections",
		},
	)

	CompensationRuns = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "stock_compensation_total",
			Help: "Total stock compensation runs",
		},
		[]string{"result"}, // success | anomalies_found | error
	)

	StockReservationPending = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ticket_stock_reservation_pending",
			Help: "Current number of pending Redis stock reservations",
		},
	)

	StockReservationOldestAgeSeconds = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ticket_stock_reservation_oldest_age_seconds",
			Help: "Age in seconds of the oldest pending Redis stock reservation",
		},
	)

	StockReservationRecoveryTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ticket_stock_reservation_recovery_total",
			Help: "Stock reservation recovery attempts by result",
		},
		[]string{"result"}, // confirmed | rolled_back | error
	)

	StockRecoveryLastSuccessTimestamp = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ticket_stock_recovery_last_success_timestamp_seconds",
			Help: "Unix timestamp of the last successful stock reservation recovery scan",
		},
	)

	StockReconciliationMismatchTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ticket_stock_reconciliation_mismatch_total",
			Help: "Redis/MySQL stock reconciliation mismatches by bounded type",
		},
		[]string{"type"}, // missing | pending_missing | too_high | too_low | invalid
	)

	EventSearchSyncRuns = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "event_search_sync_total",
			Help: "Total Elasticsearch event projection reconciliation runs",
		},
		[]string{"result"}, // success | anomalies_found | skipped | error
	)

	DistributedRateLimitRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "distributed_rate_limit_requests_total",
			Help: "Total requests evaluated by the distributed write rate limiter",
		},
		[]string{"result"}, // allowed | rejected | error_fail_open | error_fail_closed
	)

	TicketTimeoutMessages = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ticket_timeout_messages_total",
			Help: "Total ticket payment timeout messages by handling result",
		},
		[]string{"result"}, // success | malformed | retry | permanent_discard
	)

	MQMessagesPublished = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "mq_messages_published_total",
			Help: "Total messages published to RabbitMQ",
		},
	)

	MQMessagesConsumed = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "mq_messages_consumed_total",
			Help: "Total messages consumed from RabbitMQ",
		},
		[]string{"result"}, // success | error
	)

	TicketOrderConsumerTransactionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ticket_order_consumer_transaction_duration_seconds",
			Help:    "Ticket order consumer MySQL transaction duration",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"result"}, // success | retryable_error | error
	)

	TicketOrderAcceptedToPendingPaymentDuration = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "ticket_order_accepted_to_pending_payment_duration_seconds",
			Help:    "End-to-end duration from queued order acceptance to pending_payment commit",
			Buckets: []float64{.01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600},
		},
	)

	TicketOrderConsumerTransactions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ticket_order_consumer_transactions_total",
			Help: "Ticket order consumer MySQL transactions by result",
		},
		[]string{"result"}, // success | retryable_error | error
	)

	// These labels are intentionally bounded. Do not add order ID, campaign ID,
	// or other high-cardinality identifiers to transaction-stage metrics.
	TicketOrderConsumerStageDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ticket_order_consumer_stage_duration_seconds",
			Help:    "Duration of individual stages inside the ticket order consumer transaction",
			Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"stage"}, // order_lock | order_items_read | rush_bucket_update | tier_bucket_update | tier_bucket_remaining_read | tier_sold_out_refresh | order_state_update | commit
	)

	TicketOrderConsumerInventoryBucketDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ticket_order_consumer_inventory_bucket_duration_seconds",
			Help:    "Duration of a ticket order consumer inventory bucket update",
			Buckets: []float64{.0005, .001, .0025, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
		},
		[]string{"kind", "bucket_no"}, // kind: rush | tier; bucket_no is bounded by configured bucket count
	)

	TicketOrderConsumerInventoryBucketOperations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ticket_order_consumer_inventory_bucket_operations_total",
			Help: "Ticket order consumer inventory bucket updates by result",
		},
		[]string{"kind", "bucket_no", "result"}, // result: success | error
	)

	TicketPaymentTimeoutTransactionDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "ticket_payment_timeout_transaction_duration_seconds",
			Help:    "Payment timeout cancellation MySQL transaction duration",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"result"}, // success | error
	)

	TicketPaymentTimeoutTransactions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "ticket_payment_timeout_transactions_total",
			Help: "Payment timeout cancellation MySQL transactions by result",
		},
		[]string{"result"}, // success | error
	)

	InventoryBucketFallback = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "inventory_bucket_fallback_total",
			Help: "Orders restored without stock_bucket_no; used userID mod N fallback",
		},
	)

	MySQLInnoDBRowLockWaits = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "mysql_innodb_row_lock_waits_total",
			Help: "Current MySQL Innodb_row_lock_waits global status value",
		},
	)

	MySQLThreadsRunning = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "mysql_threads_running",
			Help: "MySQL Threads_running global status",
		},
	)

	MySQLThreadsConnected = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "mysql_threads_connected",
			Help: "MySQL Threads_connected global status",
		},
	)

	// Unlabeled gauges mirror the HTTP pool for backward-compatible dashboards.
	GoSQLDBOpenConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "go_sql_db_open_connections",
			Help: "database/sql OpenConnections from DB.Stats() (HTTP pool)",
		},
	)

	GoSQLDBInUse = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "go_sql_db_in_use",
			Help: "database/sql InUse from DB.Stats() (HTTP pool)",
		},
	)

	GoSQLDBIdle = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "go_sql_db_idle",
			Help: "database/sql Idle from DB.Stats() (HTTP pool)",
		},
	)

	GoSQLDBWaitCount = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "go_sql_db_wait_count",
			Help: "database/sql cumulative WaitCount from DB.Stats() (HTTP pool)",
		},
	)

	GoSQLDBWaitDurationSeconds = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "go_sql_db_wait_duration_seconds",
			Help: "database/sql cumulative WaitDuration from DB.Stats() (HTTP pool)",
		},
	)

	GoSQLDBMaxOpenConnections = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "go_sql_db_max_open_connections",
			Help: "database/sql MaxOpenConnections from DB.Stats() (HTTP pool)",
		},
	)

	GoSQLDBOpenConnectionsByPool = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "go_sql_db_pool_open_connections",
			Help: "database/sql OpenConnections by pool (http|worker)",
		},
		[]string{"pool"},
	)

	GoSQLDBInUseByPool = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "go_sql_db_pool_in_use",
			Help: "database/sql InUse by pool (http|worker)",
		},
		[]string{"pool"},
	)

	GoSQLDBIdleByPool = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "go_sql_db_pool_idle",
			Help: "database/sql Idle by pool (http|worker)",
		},
		[]string{"pool"},
	)

	GoSQLDBWaitCountByPool = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "go_sql_db_pool_wait_count",
			Help: "database/sql cumulative WaitCount by pool (http|worker)",
		},
		[]string{"pool"},
	)

	GoSQLDBWaitDurationSecondsByPool = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "go_sql_db_pool_wait_duration_seconds",
			Help: "database/sql cumulative WaitDuration by pool (http|worker)",
		},
		[]string{"pool"},
	)

	GoSQLDBMaxOpenConnectionsByPool = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "go_sql_db_pool_max_open_connections",
			Help: "database/sql MaxOpenConnections by pool (http|worker)",
		},
		[]string{"pool"},
	)

	MQQueueReadyMessages = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mq_queue_ready_messages",
			Help: "Current RabbitMQ messages ready for delivery",
		},
		[]string{"queue"},
	)

	MQQueueConsumers = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "mq_queue_consumers",
			Help: "Current RabbitMQ consumers attached to the queue",
		},
		[]string{"queue"},
	)

	TicketOutboxPendingRows = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ticket_order_outbox_pending_rows",
			Help: "Current ticket order Outbox rows waiting for publish completion",
		},
	)

	TicketOutboxOldestAgeSeconds = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "ticket_order_outbox_oldest_age_seconds",
			Help: "Age in seconds of the oldest pending or publishing ticket order Outbox row",
		},
	)
)

// ===== Prometheus Middleware =====

func PrometheusMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.FullPath()
		if path == "" {
			path = c.Request.URL.Path
		}

		ActiveHTTPConnections.Inc()
		defer ActiveHTTPConnections.Dec()

		c.Next()

		status := strconv.Itoa(c.Writer.Status())
		duration := time.Since(start).Seconds()

		HTTPRequestsTotal.WithLabelValues(c.Request.Method, path, status).Inc()
		HTTPRequestDuration.WithLabelValues(c.Request.Method, path).Observe(duration)
	}
}

// ===== Register /metrics endpoint =====

func RegisterHandler(r *gin.Engine) {
	r.GET("/metrics", gin.WrapH(promhttp.Handler()))
}
