package metrics

import (
	"math"
	"sync"
	"time"
)

const httpRateWindowSeconds = 10
const httpHealthWindowSeconds = 5 * 60

var httpHealthDurationBounds = [...]float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, math.Inf(1)}

type rollingWindow struct {
	mu      sync.Mutex
	buckets [httpRateWindowSeconds]struct {
		sec int64
		n   int64
	}
}

func (w *rollingWindow) add(n int64) {
	sec := time.Now().Unix()
	idx := int(sec % httpRateWindowSeconds)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buckets[idx].sec != sec {
		w.buckets[idx].sec = sec
		w.buckets[idx].n = 0
	}
	w.buckets[idx].n += n
}

func (w *rollingWindow) rate() float64 {
	sec := time.Now().Unix()
	cutoff := sec - httpRateWindowSeconds + 1
	w.mu.Lock()
	defer w.mu.Unlock()
	var sum int64
	for _, bucket := range w.buckets {
		if bucket.sec >= cutoff {
			sum += bucket.n
		}
	}
	return float64(sum) / float64(httpRateWindowSeconds)
}

var httpRequestWindow = &rollingWindow{}

type httpHealthBucket struct {
	sec       int64
	requests  int64
	code4xx   int64
	code5xx   int64
	durations [len(httpHealthDurationBounds)]int64
}

type httpHealthSnapshot struct {
	Requests int64
	Code4xx  int64
	Code5xx  int64
	P50      float64
	P95      float64
	P99      float64
}

type httpHealthWindow struct {
	mu      sync.Mutex
	buckets [httpHealthWindowSeconds]httpHealthBucket
}

func (w *httpHealthWindow) addAt(now time.Time, status int, duration float64) {
	sec := now.Unix()
	idx := int(sec % httpHealthWindowSeconds)
	w.mu.Lock()
	defer w.mu.Unlock()
	bucket := &w.buckets[idx]
	if bucket.sec != sec {
		*bucket = httpHealthBucket{sec: sec}
	}
	bucket.requests++
	if status >= 500 {
		bucket.code5xx++
	} else if status >= 400 {
		bucket.code4xx++
	}
	for i, upper := range httpHealthDurationBounds {
		if duration <= upper {
			bucket.durations[i]++
			break
		}
	}
}

func (w *httpHealthWindow) snapshotAt(now time.Time) httpHealthSnapshot {
	cutoff := now.Unix() - httpHealthWindowSeconds + 1
	w.mu.Lock()
	defer w.mu.Unlock()
	var out httpHealthSnapshot
	var counts [len(httpHealthDurationBounds)]int64
	for _, bucket := range w.buckets {
		if bucket.sec < cutoff {
			continue
		}
		out.Requests += bucket.requests
		out.Code4xx += bucket.code4xx
		out.Code5xx += bucket.code5xx
		for i := range counts {
			counts[i] += bucket.durations[i]
		}
	}
	out.P50 = windowQuantile(0.50, counts, out.Requests)
	out.P95 = windowQuantile(0.95, counts, out.Requests)
	out.P99 = windowQuantile(0.99, counts, out.Requests)
	return out
}

func windowQuantile(q float64, counts [len(httpHealthDurationBounds)]int64, total int64) float64 {
	if total <= 0 || q <= 0 || q > 1 {
		return 0
	}
	rank := int64(math.Ceil(q * float64(total)))
	var cumulative int64
	for i, count := range counts {
		cumulative += count
		if cumulative < rank {
			continue
		}
		upper := httpHealthDurationBounds[i]
		if math.IsInf(upper, 1) && i > 0 {
			return httpHealthDurationBounds[i-1]
		}
		return upper
	}
	return 0
}

var httpHealth = &httpHealthWindow{}

func observeHTTPTraffic() {
	httpRequestWindow.add(1)
}

func observeHTTPHealth(status int, duration float64) {
	httpHealth.addAt(time.Now(), status, duration)
}
