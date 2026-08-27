package metrics

import (
	"math"
	"sort"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// PlatformSnapshot 是管理员看的进程级健康摘要：流量、延迟、错误率、限流。
// 不是某个主办方的转化漏斗。
type PlatformSnapshot struct {
	HTTPQPS                 float64 `json:"http_qps"`
	HTTPInFlight            float64 `json:"http_in_flight"`
	HTTPRequests            float64 `json:"http_requests"`
	HTTP5xx                 float64 `json:"http_5xx"`
	HTTP4xx                 float64 `json:"http_4xx"`
	HTTPErrorRate           float64 `json:"http_error_rate"`
	HTTPP50Ms               float64 `json:"http_p50_ms"`
	HTTPP95Ms               float64 `json:"http_p95_ms"`
	HTTPP99Ms               float64 `json:"http_p99_ms"`
	RateLimitAllowed        float64 `json:"rate_limit_allowed"`
	RateLimitRejected       float64 `json:"rate_limit_rejected"`
	RateLimitRejectRate     float64 `json:"rate_limit_reject_rate"`
	MQConsumedOK            float64 `json:"mq_consumed_ok"`
	MQConsumedErr           float64 `json:"mq_consumed_err"`
	MQErrorRate             float64 `json:"mq_error_rate"`
	ConsumerTxOK            float64 `json:"consumer_tx_ok"`
	ConsumerTxErr           float64 `json:"consumer_tx_err"`
	ConsumerErrorRate       float64 `json:"consumer_error_rate"`
	OrderAcceptP95Ms        float64 `json:"order_accept_p95_ms"`
	ConsumerTxP95Ms         float64 `json:"consumer_tx_p95_ms"`
	OutboxPending           float64 `json:"outbox_pending"`
	StockReservationPending float64 `json:"stock_reservation_pending"`
}

func Snapshot() PlatformSnapshot {
	out := PlatformSnapshot{
		HTTPQPS: httpRequestWindow.rate(),
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
			out.MQConsumedOK = labeledSum(family, "result", "success")
			out.MQConsumedErr = labeledSum(family, "result", "error")
		case "ticket_order_consumer_transactions_total":
			out.ConsumerTxOK = labeledSum(family, "result", "success")
			out.ConsumerTxErr = labeledSum(family, "result", "error") + labeledSum(family, "result", "retryable_error")
		case "ticket_order_accepted_to_pending_payment_duration_seconds":
			_, out.OrderAcceptP95Ms, _ = histogramPercentilesMs(family)
		case "ticket_order_consumer_transaction_duration_seconds":
			_, out.ConsumerTxP95Ms, _ = histogramPercentilesMs(family)
		case "ticket_order_outbox_pending_rows":
			out.OutboxPending = gaugeValue(family)
		case "ticket_stock_reservation_pending":
			out.StockReservationPending = gaugeValue(family)
		}
	}
	out.HTTPErrorRate = percent(out.HTTP5xx, out.HTTPRequests)
	out.RateLimitRejectRate = percent(out.RateLimitRejected, out.RateLimitAllowed+out.RateLimitRejected)
	out.MQErrorRate = percent(out.MQConsumedErr, out.MQConsumedOK+out.MQConsumedErr)
	out.ConsumerErrorRate = percent(out.ConsumerTxErr, out.ConsumerTxOK+out.ConsumerTxErr)
	return out
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
