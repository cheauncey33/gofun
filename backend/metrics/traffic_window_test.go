package metrics

import (
	"testing"
	"time"
)

func TestHTTPHealthWindowUsesRollingFiveMinuteScope(t *testing.T) {
	var window httpHealthWindow
	now := time.Unix(1_700_000_000, 0)
	window.addAt(now.Add(-6*time.Minute), 500, 2)
	window.addAt(now.Add(-10*time.Second), 200, 0.02)
	window.addAt(now.Add(-5*time.Second), 503, 0.7)

	snapshot := window.snapshotAt(now)
	if snapshot.Requests != 2 || snapshot.Code5xx != 1 {
		t.Fatalf("unexpected rolling counts: %+v", snapshot)
	}
	if snapshot.P50 != 0.025 || snapshot.P95 != 1 {
		t.Fatalf("unexpected rolling latency buckets: %+v", snapshot)
	}
}

func TestWindowQuantileEmpty(t *testing.T) {
	var counts [len(httpHealthDurationBounds)]int64
	if got := windowQuantile(0.95, counts, 0); got != 0 {
		t.Fatalf("empty quantile=%v want 0", got)
	}
}
