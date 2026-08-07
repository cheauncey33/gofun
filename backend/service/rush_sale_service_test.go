package service

import (
	"testing"
	"time"

	"gofun/models"

	gocache "github.com/patrickmn/go-cache"
)

func TestRushStockKeyUsesFuchangNamespace(t *testing.T) {
	if got, want := TicketStockBucketKey(7, 3), "fuchang:ticket:stock:7:3"; got != want {
		t.Fatalf("ticket bucket key = %q, want %q", got, want)
	}
	if got, want := RushStockBucketKey(7, 3), "fuchang:rush:stock:7:3"; got != want {
		t.Fatalf("rush bucket key = %q, want %q", got, want)
	}
	if got, want := rushStockKey(7), "fuchang:rush:stock:7"; got != want {
		t.Fatalf("rush stock key = %q, want %q", got, want)
	}
	if got, want := rushUserCountKey(7, 9), "fuchang:rush:user-count:7:9"; got != want {
		t.Fatalf("rush user count key = %q, want %q", got, want)
	}
	if got, want := rushStockLocalKey(7), "local:rush:stock:7"; got != want {
		t.Fatalf("rush local key = %q, want %q", got, want)
	}
	if got, want := rushCampaignLocalKey(7), "local:rush:campaign:7"; got != want {
		t.Fatalf("rush campaign local key = %q, want %q", got, want)
	}
}

func TestCreateRushOrderRejectsMissingPurchaseInfo(t *testing.T) {
	// 与普通单共用校验：缺联系人/须知时必须在建单前失败（调用方应回滚 Redis）。
	err := validatePurchaseInfo(PurchaseInfoInput{}, 1, false)
	if err == nil {
		t.Fatal("expected rush purchase info validation to fail")
	}
}

func TestRushStockLocalCacheHitAndInvalidate(t *testing.T) {
	svc := &RushSaleService{
		localCache: gocache.New(time.Minute, time.Minute),
	}
	svc.setRushStockLocal(42, 88)
	stock, ok, err := svc.loadRushStock(t.Context(), 42, 100)
	if err != nil || !ok || stock != 88 {
		t.Fatalf("expected local hit 88, got stock=%d ok=%v err=%v", stock, ok, err)
	}
	svc.invalidateRushStockLocal(42)
	if _, found := svc.localCache.Get(rushStockLocalKey(42)); found {
		t.Fatal("expected local cache entry deleted")
	}
}

func TestRushStockLocalTTLIsShort(t *testing.T) {
	if rushStockLocalTTL <= 0 || rushStockLocalTTL > time.Second {
		t.Fatalf("rush local TTL should stay sub-second for hotkey reads, got %s", rushStockLocalTTL)
	}
}

func TestGetAvailableCampaignUsesLocalProjection(t *testing.T) {
	cache := gocache.New(time.Minute, time.Minute)
	cache.Set(rushCampaignLocalKey(42), models.RushSaleCampaign{
		Base:   models.Base{ID: 42},
		Status: models.RushSaleStatusActive,
	}, time.Minute)
	svc := &RushSaleService{localCache: cache, campaignCacheTTL: time.Minute}

	campaign, err := svc.getAvailableCampaign(t.Context(), 42)
	if err != nil {
		t.Fatalf("getAvailableCampaign() error = %v", err)
	}
	if campaign.ID != 42 {
		t.Fatalf("campaign ID = %d, want 42", campaign.ID)
	}
}
