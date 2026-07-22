package service

import "testing"

func TestRushStockKeyUsesFuchangNamespace(t *testing.T) {
	if got, want := rushStockKey(7), "fuchang:rush:stock:7"; got != want {
		t.Fatalf("rush stock key = %q, want %q", got, want)
	}
	if got, want := rushTokenKey(7, 9), "fuchang:rush:token:7:9"; got != want {
		t.Fatalf("rush token key = %q, want %q", got, want)
	}
	if got, want := rushUserCountKey(7, 9), "fuchang:rush:user-count:7:9"; got != want {
		t.Fatalf("rush user count key = %q, want %q", got, want)
	}
}

func TestCreateRushOrderRejectsMissingPurchaseInfo(t *testing.T) {
	// 与普通单共用校验：缺联系人/须知时必须在建单前失败（调用方应回滚 Redis）。
	err := validatePurchaseInfo(PurchaseInfoInput{}, 1, false)
	if err == nil {
		t.Fatal("expected rush purchase info validation to fail")
	}
}
