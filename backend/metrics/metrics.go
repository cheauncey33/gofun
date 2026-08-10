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
		[]string{"result"}, // success | anomalies_found
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
		[]string{"stage"}, // order_lock | order_items_read | rush_bucket_update | tier_bucket_update | order_state_update
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

	InventoryReservationBatchDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "inventory_reservation_batch_duration_seconds",
			Help:    "Inventory reservation batch transaction duration",
			Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
		},
		[]string{"result"}, // success | error
	)

	InventoryReservationBatches = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "inventory_reservation_batches_total",
			Help: "Inventory reservation batches by result",
		},
		[]string{"result"}, // success | error | empty
	)

	InventoryReservationRows = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "inventory_reservation_rows_total",
			Help: "Inventory reservation rows transitioned by state",
		},
		[]string{"transition"}, // reserved | released | released_without_bucket_update
	)

	InventoryReservationPending = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "inventory_reservation_pending",
			Help: "Current inventory reservations whose desired and applied states differ",
		},
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

	OutboxBufferLength = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "outbox_buffer_len",
			Help: "Current number of outbox drafts waiting in the process-local batch buffer",
		},
	)

	OutboxFlushBatchSize = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "outbox_flush_batch_size",
			Help:    "Number of outbox rows written per batch flush",
			Buckets: []float64{1, 5, 10, 25, 50, 100, 200, 500, 1000},
		},
	)

	OutboxRecoverBackfill = promauto.NewCounter(
		prometheus.CounterOpts{
			Name: "outbox_recover_backfill_total",
			Help: "Total queued orders that received a backfilled outbox row",
		},
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
