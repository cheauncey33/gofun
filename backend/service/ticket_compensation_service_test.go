package service

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestReconcileRedisStockIsConservative(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	worker := &TicketCompensationService{rdb: rdb}
	ctx := t.Context()

	missingPendingKey := "stock:missing:pending"
	anomaly, err := worker.reconcileRedisStock(
		ctx, missingPendingKey, 5, "pending", true,
		map[string]struct{}{missingPendingKey: {}},
	)
	if err != nil || !anomaly {
		t.Fatalf("pending missing key anomaly=%v err=%v", anomaly, err)
	}
	if mr.Exists(missingPendingKey) {
		t.Fatal("missing key with pending reservation must not be rebuilt")
	}

	missingSafeKey := "stock:missing:safe"
	anomaly, err = worker.reconcileRedisStock(ctx, missingSafeKey, 5, "safe", true, nil)
	if err != nil || !anomaly {
		t.Fatalf("safe missing key anomaly=%v err=%v", anomaly, err)
	}
	if got, _ := mr.Get(missingSafeKey); got != "5" {
		t.Fatalf("rebuilt stock=%q, want 5", got)
	}

	highKey := "stock:high"
	mr.Set(highKey, "8")
	anomaly, err = worker.reconcileRedisStock(ctx, highKey, 5, "high", true, nil)
	if err != nil || !anomaly {
		t.Fatalf("high stock anomaly=%v err=%v", anomaly, err)
	}
	if got, _ := mr.Get(highKey); got != "5" {
		t.Fatalf("lowered stock=%q, want 5", got)
	}

	lowKey := "stock:low"
	mr.Set(lowKey, "3")
	anomaly, err = worker.reconcileRedisStock(ctx, lowKey, 5, "low", true, nil)
	if err != nil || !anomaly {
		t.Fatalf("low stock anomaly=%v err=%v", anomaly, err)
	}
	if got, _ := mr.Get(lowKey); got != "3" {
		t.Fatalf("low stock was guessed upward to %q", got)
	}
}

func TestLowerRedisStockIfUnchangedSkipsConcurrentChange(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx := t.Context()
	key := "stock:cas"
	mr.Set(key, "4")

	changed, err := lowerRedisStockIfUnchangedScript.Run(ctx, rdb, []string{key}, "8", 5).Int64()
	if err != nil || changed != 0 {
		t.Fatalf("cas changed=%d err=%v, want 0", changed, err)
	}
	if got, _ := mr.Get(key); got != "4" {
		t.Fatalf("concurrent stock overwritten to %q", got)
	}
}
