//go:build integration

package service

import (
	"testing"
	"time"

	"gofun/config"
	"gofun/models"
	"gorm.io/gorm"
)

func TestIntegrationBucketParentStatusOnlyTransitionsAtBoundaries(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	tier := env.seedPurchasableTier(t, 2, 2)
	settings := NewInventoryBucketSettings(config.InventoryConfig{
		BucketsEnabled:   true,
		BucketCount:      2,
		MinQuotaToBucket: 1,
	})
	if err := EnsureTierBuckets(env.db, tier, settings); err != nil {
		t.Fatalf("ensure tier buckets: %v", err)
	}

	var current models.TicketTier
	if err := env.db.First(&current, tier.ID).Error; err != nil {
		t.Fatalf("load initial tier: %v", err)
	}
	initialUpdateTime := current.UpdateTime

	if err := env.db.Transaction(func(tx *gorm.DB) error {
		return deductTierBucket(tx, tier.ID, 0, 1)
	}); err != nil {
		t.Fatalf("deduct first bucket: %v", err)
	}
	if err := env.db.First(&current, tier.ID).Error; err != nil {
		t.Fatalf("load after first deduction: %v", err)
	}
	if current.Status != models.TicketTierStatusOnSale {
		t.Fatalf("tier status after partial deduction = %s, want on_sale", current.Status)
	}
	if !current.UpdateTime.Equal(initialUpdateTime) {
		t.Fatalf("parent tier updated during ordinary bucket deduction")
	}

	if err := env.db.Transaction(func(tx *gorm.DB) error {
		return deductTierBucket(tx, tier.ID, 1, 1)
	}); err != nil {
		t.Fatalf("deduct final bucket: %v", err)
	}
	if err := env.db.First(&current, tier.ID).Error; err != nil {
		t.Fatalf("load after sold-out transition: %v", err)
	}
	if current.Status != models.TicketTierStatusWaitlist {
		t.Fatalf("tier status after final deduction = %s, want waitlist", current.Status)
	}
	soldOutUpdateTime := current.UpdateTime

	time.Sleep(10 * time.Millisecond)
	if err := env.db.Transaction(func(tx *gorm.DB) error {
		return restoreTierBucket(tx, tier.ID, 0, 1)
	}); err != nil {
		t.Fatalf("restore first bucket: %v", err)
	}
	if err := env.db.First(&current, tier.ID).Error; err != nil {
		t.Fatalf("load after on-sale transition: %v", err)
	}
	if current.Status != models.TicketTierStatusOnSale {
		t.Fatalf("tier status after restore = %s, want on_sale", current.Status)
	}
	if !current.UpdateTime.After(soldOutUpdateTime) {
		t.Fatalf("expected waitlist -> on_sale parent transition")
	}
	onSaleUpdateTime := current.UpdateTime

	if err := env.db.Transaction(func(tx *gorm.DB) error {
		return restoreTierBucket(tx, tier.ID, 1, 1)
	}); err != nil {
		t.Fatalf("restore second bucket: %v", err)
	}
	if err := env.db.First(&current, tier.ID).Error; err != nil {
		t.Fatalf("load after ordinary restore: %v", err)
	}
	if current.Status != models.TicketTierStatusOnSale {
		t.Fatalf("tier status after ordinary restore = %s, want on_sale", current.Status)
	}
	if !current.UpdateTime.Equal(onSaleUpdateTime) {
		t.Fatalf("parent tier updated during ordinary restore")
	}
}
