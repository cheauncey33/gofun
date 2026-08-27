package metrics

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// PlatformSnapshot 是管理员看的进程级健康摘要：流量、延迟、错误率、限流。
// 不是某个主办方的转化漏斗。
type PlatformSnapshot struct {
	SnapshotAt                         time.Time `json:"snapshot_at"`
	HTTPWindowSeconds                  int       `json:"http_window_seconds"`
	HTTPQPS                            float64   `json:"http_qps"`
	HTTPInFlight                       float64   `json:"http_in_flight"`
	HTTPRequests                       float64   `json:"http_requests"`
	HTTP5xx                            float64   `json:"http_5xx"`
	HTTP4xx                            float64   `json:"http_4xx"`
	HTTPErrorRate                      float64   `json:"http_error_rate"`
	HTTPP50Ms                          float64   `json:"http_p50_ms"`
	HTTPP95Ms                          float64   `json:"http_p95_ms"`
	HTTPP99Ms                          float64   `json:"http_p99_ms"`
	RateLimitAllowed                   float64   `json:"rate_limit_allowed"`
	RateLimitRejected                  float64   `json:"rate_limit_rejected"`
	RateLimitRejectRate                float64   `json:"rate_limit_reject_rate"`
	MQConsumedOK                       float64   `json:"mq_consumed_ok"`
	MQConsumedErr                      float64   `json:"mq_consumed_err"`
	MQConsumedRetry                    float64   `json:"mq_consumed_retry"`
	MQConsumedMalformed                float64   `json:"mq_consumed_malformed"`
	MQConsumedPermanent                float64   `json:"mq_consumed_permanent"`
	MQConsumedDeadLetter               float64   `json:"mq_consumed_dead_letter"`
	MQErrorRate                        float64   `json:"mq_error_rate"`
	MQWorkQueueReady                   float64   `json:"mq_work_queue_ready"`
	MQWorkQueueConsumers               float64   `json:"mq_work_queue_consumers"`
	ConsumerTxOK                       float64   `json:"consumer_tx_ok"`
	ConsumerTxErr                      float64   `json:"consumer_tx_err"`
	ConsumerErrorRate                  float64   `json:"consumer_error_rate"`
	OrderAcceptP95Ms                   float64   `json:"order_accept_p95_ms"`
	ConsumerTxP95Ms                    float64   `json:"consumer_tx_p95_ms"`
	OutboxPending                      float64   `json:"outbox_pending"`
	OutboxOldestAgeSeconds             float64   `json:"outbox_oldest_age_seconds"`
	StockReservationPending            float64   `json:"stock_reservation_pending"`
	StockReservationOldestAgeSeconds   float64   `json:"stock_reservation_oldest_age_seconds"`
	StockRecoveryLastSuccessAgoSeconds float64   `json:"stock_recovery_last_success_ago_seconds"`
	DBConnectionsInUse                 float64   `json:"db_connections_in_use"`
	DBMaxOpenConnections               float64   `json:"db_max_open_connections"`
	DBPoolUsageRate                    float64   `json:"db_pool_usage_rate"`
}

func Snapshot() PlatformSnapshot {
	now := time.Now()
	out := PlatformSnapshot{
		SnapshotAt:        now,
		HTTPWindowSeconds: httpHealthWindowSeconds,
		HTTPQPS:           httpRequestWindow.rate(),
	}
	families, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		return out
	}
	for _, family := range families {
		switch family.GetName() {
		case "http_requests_total":
			out.HTTPRequests, out.HTTP4xx, out.HTTP5xx = httpStatusTotals(family)
		case "http_request_duration_seconds":
			out.HTTPP50Ms, out.HTTPP95Ms, out.HTTPP99Ms = histogramPercentilesMs(family)
		case "active_http_connections":
			out.HTTPInFlight = gaugeValue(family)
		case "distributed_rate_limit_requests_total":
			out.RateLimitAllowed = labeledSum(family, "result", "allowed")
			out.RateLimitRejected = labeledSum(family, "result", "rejected")
		case "mq_messages_consumed_total":
			applyMQConsumed(family, &out)
		case "ticket_order_consumer_transactions_total":
			out.ConsumerTxOK = labeledSum(family, "result", "success")
			out.ConsumerTxErr = labeledSum(family, "result", "error") + labeledSum(family, "result", "retryable_error")
		case "ticket_order_accepted_to_pending_payment_duration_seconds":
			_, out.OrderAcceptP95Ms, _ = histogramPercentilesMs(family)
		case "ticket_order_consumer_transaction_duration_seconds":
			_, out.ConsumerTxP95Ms, _ = histogramPercentilesMs(family)
		case "ticket_order_outbox_pending_rows":
			out.OutboxPending = gaugeValue(family)
		case "ticket_order_outbox_oldest_age_seconds":
			out.OutboxOldestAgeSeconds = gaugeValue(family)
		case "ticket_stock_reservation_pending":
			out.StockReservationPending = gaugeValue(family)
		case "ticket_stock_reservation_oldest_age_seconds":
			out.StockReservationOldestAgeSeconds = gaugeValue(family)
		case "ticket_stock_recovery_last_success_timestamp_seconds":
			lastSuccess := gaugeValue(family)
			if lastSuccess > 0 {
				out.StockRecoveryLastSuccessAgoSeconds = math.Max(0, float64(now.Unix())-lastSuccess)
			}
		case "mq_queue_ready_messages":
			out.MQWorkQueueReady = orderWorkQueueSum(family)
		case "mq_queue_consumers":
			out.MQWorkQueueConsumers = orderWorkQueueSum(family)
		case "go_sql_db_pool_in_use":
			out.DBConnectionsInUse = labeledSum(family, "pool", "http")
		case "go_sql_db_pool_max_open_connections":
			out.DBMaxOpenConnections = labeledSum(family, "pool", "http")
		}
	}
	health := httpHealth.snapshotAt(now)
	out.HTTPRequests = float64(health.Requests)
	out.HTTP4xx = float64(health.Code4xx)
	out.HTTP5xx = float64(health.Code5xx)
	out.HTTPP50Ms = health.P50 * 1000
	out.HTTPP95Ms = health.P95 * 1000
	out.HTTPP99Ms = health.P99 * 1000
	out.HTTPErrorRate = percent(out.HTTP5xx, out.HTTPRequests)
	out.RateLimitRejectRate = percent(out.RateLimitRejected, out.RateLimitAllowed+out.RateLimitRejected)
	out.MQErrorRate = percent(out.MQConsumedErr, out.MQConsumedOK+out.MQConsumedErr)
	out.ConsumerErrorRate = percent(out.ConsumerTxErr, out.ConsumerTxOK+out.ConsumerTxErr)
	out.DBPoolUsageRate = percent(out.DBConnectionsInUse, out.DBMaxOpenConnections)
	return out
}

func applyMQConsumed(family *dto.MetricFamily, out *PlatformSnapshot) {
	if family == nil || out == nil {
		return
	}
	out.MQConsumedOK = labeledSum(family, "result", "success")
	out.MQConsumedRetry = labeledSum(family, "result", "retry")
	out.MQConsumedMalformed = labeledSum(family, "result", "malformed")
	out.MQConsumedPermanent = labeledSum(family, "result", "permanent_error")
	out.MQConsumedDeadLetter = labeledSum(family, "result", "dead_letter")
	out.MQConsumedErr = out.MQConsumedMalformed + out.MQConsumedPermanent + out.MQConsumedDeadLetter
}

func orderWorkQueueSum(family *dto.MetricFamily) float64 {
	var total float64
	for _, metric := range family.GetMetric() {
		queue := labelValue(metric, "queue")
		if strings.HasSuffix(queue, ".order.queue") || strings.HasSuffix(queue, ".order.retry") {
			total += metricValue(metric)
		}
	}
	return total
}

func percent(part, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return part / total * 100
}

func httpStatusTotals(family *dto.MetricFamily) (all, code4xx, code5xx float64) {
	for _, metric := range family.GetMetric() {
		value := metricValue(metric)
		all += value
		status := labelValue(metric, "status")
		switch {
		case strings.HasPrefix(status, "5"):
			code5xx += value
		case strings.HasPrefix(status, "4"):
			code4xx += value
		}
	}
	return all, code4xx, code5xx
}

func histogramPercentilesMs(family *dto.MetricFamily) (p50, p95, p99 float64) {
	count, buckets := mergeHistogram(family)
	if count == 0 {
		return 0, 0, 0
	}
	return histogramQuantile(0.50, buckets, count) * 1000,
		histogramQuantile(0.95, buckets, count) * 1000,
		histogramQuantile(0.99, buckets, count) * 1000
}

type histBucket struct {
	upper float64
	count uint64
}

func mergeHistogram(family *dto.MetricFamily) (uint64, []histBucket) {
	incremental := map[float64]uint64{}
	var observations uint64
	for _, metric := range family.GetMetric() {
		hist := metric.GetHistogram()
		if hist == nil {
			continue
		}
		observations += hist.GetSampleCount()
		var prev uint64
		buckets := append([]*dto.Bucket(nil), hist.GetBucket()...)
		sort.Slice(buckets, func(i, j int) bool {
			return buckets[i].GetUpperBound() < buckets[j].GetUpperBound()
		})
		for _, bucket := range buckets {
			cum := bucket.GetCumulativeCount()
			if cum < prev {
				continue
			}
			incremental[bucket.GetUpperBound()] += cum - prev
			prev = cum
		}
	}
	uppers := make([]float64, 0, len(incremental))
	for upper := range incremental {
		uppers = append(uppers, upper)
	}
	sort.Float64s(uppers)
	out := make([]histBucket, 0, len(uppers))
	var cum uint64
	for _, upper := range uppers {
		cum += incremental[upper]
		out = append(out, histBucket{upper: upper, count: cum})
	}
	return observations, out
}

func histogramQuantile(q float64, buckets []histBucket, observations uint64) float64 {
	if q < 0 || q > 1 || observations == 0 || len(buckets) == 0 {
		return 0
	}
	rank := q * float64(observations)
	var prevUpper float64
	var prevCount uint64
	for _, bucket := range buckets {
		if float64(bucket.count) < rank {
			prevUpper = bucket.upper
			prevCount = bucket.count
			continue
		}
		if math.IsInf(bucket.upper, 1) {
			return prevUpper
		}
		if bucket.count == prevCount {
			return bucket.upper
		}
		frac := (rank - float64(prevCount)) / float64(bucket.count-prevCount)
		return prevUpper + (bucket.upper-prevUpper)*frac
	}
	last := buckets[len(buckets)-1]
	if math.IsInf(last.upper, 1) && len(buckets) > 1 {
		return buckets[len(buckets)-2].upper
	}
	return last.upper
}

func labeledSum(family *dto.MetricFamily, label, want string) float64 {
	var total float64
	for _, metric := range family.GetMetric() {
		if labelValue(metric, label) == want {
			total += metricValue(metric)
		}
	}
	return total
}

func gaugeValue(family *dto.MetricFamily) float64 {
	if len(family.GetMetric()) == 0 {
		return 0
	}
	return metricValue(family.GetMetric()[0])
}

func metricValue(metric *dto.Metric) float64 {
	if metric.GetCounter() != nil {
		return metric.GetCounter().GetValue()
	}
	if metric.GetGauge() != nil {
		return metric.GetGauge().GetValue()
	}
	if metric.GetHistogram() != nil {
		return float64(metric.GetHistogram().GetSampleCount())
	}
	if metric.GetUntyped() != nil {
		return metric.GetUntyped().GetValue()
	}
	return 0
}

func labelValue(metric *dto.Metric, name string) string {
	for _, label := range metric.GetLabel() {
		if strings.EqualFold(label.GetName(), name) {
			return label.GetValue()
		}
	}
	return ""
}
