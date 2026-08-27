package metrics

import (
	"math"
	"testing"
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
