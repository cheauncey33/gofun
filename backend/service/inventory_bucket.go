package service

import (
	"fmt"
	"gofun/config"
)

// InventoryBucketSettings 票档/抢票分桶运行时参数（来自 inventory.* 配置）。
type InventoryBucketSettings struct {
	Enabled          bool
	BucketCount      int
	MinQuotaToBucket int
	BucketRetry      int
}

func NewInventoryBucketSettings(cfg config.InventoryConfig) InventoryBucketSettings {
	s := InventoryBucketSettings{
		Enabled:          cfg.BucketsEnabled,
		BucketCount:      cfg.BucketCount,
		MinQuotaToBucket: cfg.MinQuotaToBucket,
		BucketRetry:      cfg.BucketRetry,
	}
	if s.BucketCount <= 0 {
		s.BucketCount = 32
	}
	if s.MinQuotaToBucket <= 0 {
		s.MinQuotaToBucket = 64
	}
	if s.BucketRetry < 0 {
		s.BucketRetry = 0
	}
	return s
}

// EffectiveBucketCount 小库存强制单桶，避免空桶误伤。
func (s InventoryBucketSettings) EffectiveBucketCount(totalQuota int) int {
	if !s.Enabled {
		return 1
	}
	if totalQuota < s.MinQuotaToBucket {
		return 1
	}
	if s.BucketCount <= 1 {
		return 1
	}
	return s.BucketCount
}

// SplitQuotaEvenly 将 total 均分到 n 个桶；余数从前几个桶各 +1。
func SplitQuotaEvenly(total, n int) []int {
	if n <= 0 {
		n = 1
	}
	if total < 0 {
		total = 0
	}
	out := make([]int, n)
	base := total / n
	rem := total % n
	for i := 0; i < n; i++ {
		out[i] = base
		if i < rem {
			out[i]++
		}
	}
	return out
}

// SelectBucketNo 主桶 = userID % n，失败时可按 offset 环形换桶。
func SelectBucketNo(userID int64, n, attemptOffset int) int {
	if n <= 0 {
		n = 1
	}
	if attemptOffset < 0 {
		attemptOffset = 0
	}
	base := int(userID % int64(n))
	if base < 0 {
		base = -base
	}
	return (base + attemptOffset) % n
}

func TicketStockBucketKey(tierID int64, bucketNo int) string {
	return fmt.Sprintf("fuchang:ticket:stock:%d:%d", tierID, bucketNo)
}

func RushStockBucketKey(campaignID int64, bucketNo int) string {
	return fmt.Sprintf("fuchang:rush:stock:%d:%d", campaignID, bucketNo)
}

// ResolveOrderBucketNo 订单无桶号时的回补兜底（旧单）：userID % n，再不行用 0。
func ResolveOrderBucketNo(bucketNo *int, userID int64, n int) (int, bool) {
	if bucketNo != nil {
		return *bucketNo, false
	}
	if n <= 0 {
		n = 1
	}
	return SelectBucketNo(userID, n, 0), true
}
