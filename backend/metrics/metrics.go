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

	SeckillRequests = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "seckill_requests_total",
			Help: "Total seckill requests",
		},
		[]string{"activity_id", "result"}, // success | sold_out | limit_reached | token_invalid
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
