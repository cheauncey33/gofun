package metrics

import (
	"sync"
	"time"
)

const httpRateWindowSeconds = 10

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

func observeHTTPTraffic() {
	httpRequestWindow.add(1)
}
