package service

import (
	"testing"

	"WHU_Snack_GO/config"
)

func TestSplitQuotaEvenly(t *testing.T) {
	got := SplitQuotaEvenly(10, 8)
	sum := 0
	for _, v := range got {
		sum += v
	}
	if sum != 10 || len(got) != 8 {
		t.Fatalf("split 10/8 = %v sum=%d", got, sum)
	}
	if got[0] != 2 || got[1] != 2 || got[2] != 1 {
		t.Fatalf("expected remainder on first buckets, got %v", got)
	}
}

func TestEffectiveBucketCountSmallQuota(t *testing.T) {
	s := NewInventoryBucketSettings(config.InventoryConfig{
		BucketsEnabled:   true,
		BucketCount:      8,
		MinQuotaToBucket: 64,
		BucketRetry:      4,
	})
	if s.EffectiveBucketCount(63) != 1 {
		t.Fatalf("small quota should use 1 bucket")
	}
	if s.EffectiveBucketCount(64) != 8 {
		t.Fatalf("large quota should use configured buckets")
	}
	s.Enabled = false
	if s.EffectiveBucketCount(1000) != 1 {
		t.Fatalf("disabled should force 1")
	}
}

func TestSelectBucketNo(t *testing.T) {
	if SelectBucketNo(10, 8, 0) != 2 {
		t.Fatalf("10%%8 = 2")
	}
	if SelectBucketNo(10, 8, 1) != 3 {
		t.Fatalf("retry +1")
	}
	if SelectBucketNo(7, 8, 4) != 3 { // (7+4)%8
		t.Fatalf("wrap retry")
	}
}

func TestResolveOrderBucketNo(t *testing.T) {
	n := 3
	got, fallback := ResolveOrderBucketNo(&n, 99, 8)
	if got != 3 || fallback {
		t.Fatalf("explicit bucket")
	}
	got, fallback = ResolveOrderBucketNo(nil, 10, 8)
	if got != 2 || !fallback {
		t.Fatalf("fallback user%%n expected 2, got %d fallback=%v", got, fallback)
	}
}
