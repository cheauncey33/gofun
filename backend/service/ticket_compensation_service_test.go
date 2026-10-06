package service

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"testing"
	"time"
)

func TestRecoveryWorkerStopsOnCancellation(t *testing.T) {
	worker := &TicketCompensationService{order: &TicketOrderService{scannerInterval: 10 * time.Second}}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		worker.Start(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("recovery worker did not stop")
	}
}

func TestReconciliationReportsDrift(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	worker := &TicketCompensationService{rdb: rdb}
	for _, tc := range []struct {
		name, value string
		anomaly     bool
	}{
		{"missing", "", true}, {"high", "8", true}, {"low", "3", true}, {"equal", "5", false}, {"invalid", "bad", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := "stock:" + tc.name
			if tc.value != "" {
				mr.Set(key, tc.value)
			}
			anomaly, err := worker.reconcileRedisStock(t.Context(), key, 5, tc.name, true, nil)
			if err != nil || anomaly != tc.anomaly {
				t.Fatalf("anomaly=%v err=%v", anomaly, err)
			}
			if tc.value == "" {
				if mr.Exists(key) {
					t.Fatal("check created stock key")
				}
				return
			}
			if got, _ := mr.Get(key); got != tc.value {
				t.Fatalf("stock=%q want=%q", got, tc.value)
			}
		})
	}
}
