package service

import (
	"testing"

	"gofun/config"
	"gofun/models"
)

func TestResolveStockBucketPrefersOrderField(t *testing.T) {
	settings := NewInventoryBucketSettings(config.InventoryConfig{
		BucketsEnabled: true, BucketCount: 8, MinQuotaToBucket: 64, BucketRetry: 4,
	})
	n := 5
	order := &models.TicketOrder{Base: models.Base{ID: 1}, UserID: 10, StockBucketNo: &n}
	got, fallback := resolveStockBucketForOrder(order, nil, settings, 1000)
	if got != 5 || fallback {
		t.Fatalf("got=%d fallback=%v", got, fallback)
	}
}

func TestResolveStockBucketUsesMessageThenFallback(t *testing.T) {
	settings := NewInventoryBucketSettings(config.InventoryConfig{
		BucketsEnabled: true, BucketCount: 8, MinQuotaToBucket: 64, BucketRetry: 4,
	})
	msgBucket := 1
	order := &models.TicketOrder{Base: models.Base{ID: 2}, UserID: 10}
	got, fallback := resolveStockBucketForOrder(order, &msgBucket, settings, 1000)
	if got != 1 || fallback {
		t.Fatalf("message bucket preferred: got=%d fallback=%v", got, fallback)
	}
	got, fallback = resolveStockBucketForOrder(order, nil, settings, 1000)
	if !fallback || got != SelectBucketNo(10, 8, 0) {
		t.Fatalf("fallback expected user%%8=2, got=%d fallback=%v", got, fallback)
	}
}
