package metrics

import (
	"math"
	"testing"

	dto "github.com/prometheus/client_model/go"
)

func TestHistogramQuantile(t *testing.T) {
	buckets := []histBucket{
		{upper: 0.1, count: 10},
		{upper: 0.5, count: 50},
		{upper: 1, count: 90},
		{upper: math.Inf(1), count: 100},
	}
	p50 := histogramQuantile(0.50, buckets, 100)
	if p50 < 0.49 || p50 > 0.51 {
		t.Fatalf("p50=%v want ~0.5", p50)
	}
	p90 := histogramQuantile(0.90, buckets, 100)
	if p90 < 0.99 || p90 > 1.01 {
		t.Fatalf("p90=%v want ~1.0", p90)
	}
	if histogramQuantile(0.99, buckets, 100) != 1 {
		t.Fatalf("p99 should clamp to last finite bucket, got %v", histogramQuantile(0.99, buckets, 100))
	}
	if histogramQuantile(0.5, nil, 0) != 0 {
		t.Fatal("empty histogram should be 0")
	}
}

func TestPercent(t *testing.T) {
	if percent(5, 200) != 2.5 {
		t.Fatalf("percent=%v", percent(5, 200))
	}
	if percent(1, 0) != 0 {
		t.Fatal("zero total should be 0")
	}
}

func mqCounter(result string, value float64) *dto.Metric {
	name := "result"
	r := result
	v := value
	return &dto.Metric{
		Label:   []*dto.LabelPair{{Name: &name, Value: &r}},
		Counter: &dto.Counter{Value: &v},
	}
}

func TestApplyMQConsumedMapsConsumerLabels(t *testing.T) {
	family := &dto.MetricFamily{
		Metric: []*dto.Metric{
			mqCounter("success", 10),
			mqCounter("retry", 4),
			mqCounter("malformed", 1),
			mqCounter("permanent_error", 2),
			mqCounter("dead_letter", 3),
			mqCounter("error", 99),
		},
	}
	var out PlatformSnapshot
	applyMQConsumed(family, &out)
	if out.MQConsumedOK != 10 {
		t.Fatalf("ok=%v want 10", out.MQConsumedOK)
	}
	if out.MQConsumedRetry != 4 {
		t.Fatalf("retry=%v want 4", out.MQConsumedRetry)
	}
	if out.MQConsumedMalformed != 1 || out.MQConsumedPermanent != 2 || out.MQConsumedDeadLetter != 3 {
		t.Fatalf("split labels malformed=%v permanent=%v dead_letter=%v",
			out.MQConsumedMalformed, out.MQConsumedPermanent, out.MQConsumedDeadLetter)
	}
	if out.MQConsumedErr != 6 {
		t.Fatalf("err=%v want 6 (malformed+permanent+dead_letter; retry and leftover error ignored)", out.MQConsumedErr)
	}
}

func TestSnapshotWiresMQConsumerLabels(t *testing.T) {
	before := Snapshot()
	MQMessagesConsumed.WithLabelValues("success").Add(5)
	MQMessagesConsumed.WithLabelValues("retry").Add(2)
	MQMessagesConsumed.WithLabelValues("malformed").Add(1)
	MQMessagesConsumed.WithLabelValues("permanent_error").Add(1)
	MQMessagesConsumed.WithLabelValues("dead_letter").Add(1)
	MQMessagesConsumed.WithLabelValues("error").Add(8)
	after := Snapshot()
	if after.MQConsumedOK-before.MQConsumedOK != 5 {
		t.Fatalf("ok delta=%v want 5", after.MQConsumedOK-before.MQConsumedOK)
	}
	if after.MQConsumedRetry-before.MQConsumedRetry != 2 {
		t.Fatalf("retry delta=%v want 2", after.MQConsumedRetry-before.MQConsumedRetry)
	}
	if after.MQConsumedMalformed-before.MQConsumedMalformed != 1 {
		t.Fatalf("malformed delta=%v", after.MQConsumedMalformed-before.MQConsumedMalformed)
	}
	if after.MQConsumedPermanent-before.MQConsumedPermanent != 1 {
		t.Fatalf("permanent delta=%v", after.MQConsumedPermanent-before.MQConsumedPermanent)
	}
	if after.MQConsumedDeadLetter-before.MQConsumedDeadLetter != 1 {
		t.Fatalf("dead_letter delta=%v", after.MQConsumedDeadLetter-before.MQConsumedDeadLetter)
	}
	if after.MQConsumedErr-before.MQConsumedErr != 3 {
		t.Fatalf("err delta=%v want 3; leftover error label must not count", after.MQConsumedErr-before.MQConsumedErr)
	}
}
