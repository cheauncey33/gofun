package service

import (
	"context"
	"errors"
	"fmt"
	"gofun/metrics"
	"gofun/models"
	"log"
	"time"

	"gorm.io/gorm"
)

// EnsureTierBuckets 幂等：票档尚无分桶行时按 remaining 均分插入。
func EnsureTierBuckets(tx *gorm.DB, tier *models.TicketTier, settings InventoryBucketSettings) error {
	if tier == nil || !settings.Enabled {
		return nil
	}
	var count int64
	if err := tx.Model(&models.TicketTierBucket{}).
		Where("tier_id = ?", tier.ID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	n := settings.EffectiveBucketCount(tier.TotalQuota)
	parts := SplitQuotaEvenly(tier.RemainingQuota, n)
	rows := make([]models.TicketTierBucket, 0, n)
	for i, q := range parts {
		rows = append(rows, models.TicketTierBucket{
			TierID:         tier.ID,
			BucketNo:       i,
			RemainingQuota: q,
		})
	}
	return tx.Create(&rows).Error
}

// EnsureRushBuckets 幂等：存在活动分桶行时跳过。
func EnsureRushBuckets(tx *gorm.DB, campaign *models.RushSaleCampaign, settings InventoryBucketSettings) error {
	if campaign == nil || !settings.Enabled {
		return nil
	}
	var count int64
	if err := tx.Model(&models.RushCampaignBucket{}).
		Where("campaign_id = ?", campaign.ID).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	n := settings.EffectiveBucketCount(campaign.TotalQuota)
	parts := SplitQuotaEvenly(campaign.RemainingQuota, n)
	rows := make([]models.RushCampaignBucket, 0, n)
	for i, q := range parts {
		rows = append(rows, models.RushCampaignBucket{
			CampaignID:     campaign.ID,
			BucketNo:       i,
			RemainingQuota: q,
		})
	}
	return tx.Create(&rows).Error
}

func deductTierBucket(tx *gorm.DB, tierID int64, bucketNo, quantity int) error {
	stageStarted := time.Now()
	result := tx.Model(&models.TicketTierBucket{}).
		Where("tier_id = ? AND bucket_no = ? AND remaining_quota >= ?", tierID, bucketNo, quantity).
		Updates(map[string]interface{}{
			"remaining_quota": gorm.Expr("remaining_quota - ?", quantity),
			"sold_count":      gorm.Expr("sold_count + ?", quantity),
			"version":         gorm.Expr("version + 1"),
		})
	metrics.TicketOrderConsumerStageDuration.WithLabelValues("tier_bucket_update").Observe(time.Since(stageStarted).Seconds())
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return classifyTierBucketUpdateMiss(tx, tierID, bucketNo, quantity)
	}
	// 仅当前桶扣到 0 时再 SUM 判断售罄，避免每单扫全部桶。
	var bucketRem int
	stageStarted = time.Now()
	err := tx.Model(&models.TicketTierBucket{}).
		Select("remaining_quota").
		Where("tier_id = ? AND bucket_no = ?", tierID, bucketNo).
		Scan(&bucketRem).Error
	metrics.TicketOrderConsumerStageDuration.WithLabelValues("tier_bucket_remaining_read").Observe(time.Since(stageStarted).Seconds())
	if err != nil {
		return err
	}
	if bucketRem > 0 {
		return nil
	}
	stageStarted = time.Now()
	err = refreshTierSoldOutFromBuckets(tx, tierID, quantity)
	metrics.TicketOrderConsumerStageDuration.WithLabelValues("tier_sold_out_refresh").Observe(time.Since(stageStarted).Seconds())
	return err
}

func deductRushBucket(tx *gorm.DB, campaignID, tierID int64, bucketNo, quantity int) error {
	result := tx.Model(&models.RushCampaignBucket{}).
		Where("campaign_id = ? AND bucket_no = ? AND remaining_quota >= ?", campaignID, bucketNo, quantity).
		Update("remaining_quota", gorm.Expr("remaining_quota - ?", quantity))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return classifyRushBucketUpdateMiss(tx, campaignID, tierID, bucketNo, quantity)
	}
	return nil
}

func restoreTierBucket(tx *gorm.DB, tierID int64, bucketNo, quantity int) error {
	result := tx.Model(&models.TicketTierBucket{}).
		Where("tier_id = ? AND bucket_no = ?", tierID, bucketNo).
		Updates(map[string]interface{}{
			"remaining_quota": gorm.Expr("remaining_quota + ?", quantity),
			"sold_count":      gorm.Expr("GREATEST(sold_count - ?, 0)", quantity),
			"version":         gorm.Expr("version + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if err := tx.Create(&models.TicketTierBucket{
			TierID:         tierID,
			BucketNo:       bucketNo,
			RemainingQuota: quantity,
		}).Error; err != nil {
			return err
		}
		// 缺失桶按 0 -> quantity 处理，需要尝试 sold_out -> on_sale。
		return refreshTierOnSaleFromBuckets(tx, tierID, quantity)
	}

	// UPDATE 已经锁住了桶行；只有更新后的桶余量等于本次恢复量时，
	// 才能确定它是从 0 恢复到正数。普通正数恢复不再访问父级热行。
	var bucketRemaining int
	if err := tx.Model(&models.TicketTierBucket{}).
		Select("remaining_quota").
		Where("tier_id = ? AND bucket_no = ?", tierID, bucketNo).
		Scan(&bucketRemaining).Error; err != nil {
		return err
	}
	if bucketRemaining != quantity {
		return nil
	}
	return refreshTierOnSaleFromBuckets(tx, tierID, quantity)
}

func restoreRushBucket(tx *gorm.DB, campaignID int64, bucketNo, quantity int) error {
	result := tx.Model(&models.RushCampaignBucket{}).
		Where("campaign_id = ? AND bucket_no = ?", campaignID, bucketNo).
		Updates(map[string]interface{}{
			"remaining_quota": gorm.Expr("remaining_quota + ?", quantity),
			"version":         gorm.Expr("version + 1"),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return tx.Create(&models.RushCampaignBucket{
			CampaignID:     campaignID,
			BucketNo:       bucketNo,
			RemainingQuota: quantity,
		}).Error
	}
	return nil
}

// refreshTierSoldOutFromBuckets 父表退出热路径：仅当全部分桶余量归零时标记 sold_out。
// sold_count / remaining_quota 由补偿任务按桶 SUM 回写，避免消费期打到同一父行。
func refreshTierSoldOutFromBuckets(tx *gorm.DB, tierID int64, _ int) error {
	var sum int64
	if err := tx.Model(&models.TicketTierBucket{}).
		Select("COALESCE(SUM(remaining_quota),0)").
		Where("tier_id = ?", tierID).Scan(&sum).Error; err != nil {
		return err
	}
	if sum > 0 {
		return nil
	}
	// 只有真正发生 on_sale -> sold_out 边界转换时才更新父级行。
	// 普通分桶扣减不会触碰 ticket_tier，状态漂移由补偿任务修正。
	return tx.Model(&models.TicketTier{}).
		Where("id = ? AND status = ?", tierID, models.TicketTierStatusOnSale).
		Update("status", models.TicketTierStatusSoldOut).Error
}

func refreshTierOnSaleFromBuckets(tx *gorm.DB, tierID int64, _ int) error {
	// 恢复库存时只有 sold_out -> on_sale 才需要更新父级行；
	// 已经 on_sale 的普通恢复不再执行无效 UPDATE。
	return tx.Model(&models.TicketTier{}).
		Where("id = ? AND status = ?", tierID, models.TicketTierStatusSoldOut).
		Update("status", models.TicketTierStatusOnSale).Error
}

func classifyTierBucketUpdateMiss(tx *gorm.DB, tierID int64, bucketNo, quantity int) error {
	var bucket models.TicketTierBucket
	err := tx.Where("tier_id = ? AND bucket_no = ?", tierID, bucketNo).First(&bucket).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: 票档分桶不存在", ErrTicketOrderNonRetryable)
	}
	if err != nil {
		return err
	}
	_ = quantity
	return fmt.Errorf("%w: MySQL 分桶票额不足", ErrTicketOrderNonRetryable)
}

func classifyRushBucketUpdateMiss(tx *gorm.DB, campaignID, tierID int64, bucketNo, quantity int) error {
	var campaign models.RushSaleCampaign
	if err := tx.Select("id", "ticket_tier_id").First(&campaign, campaignID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: 限时开售活动不存在", ErrTicketOrderNonRetryable)
		}
		return err
	}
	if campaign.TicketTierID != tierID {
		return fmt.Errorf("%w: 限时开售票额不足", ErrTicketOrderNonRetryable)
	}
	_ = bucketNo
	_ = quantity
	return fmt.Errorf("%w: 限时开售分桶票额不足", ErrTicketOrderNonRetryable)
}

func sumTierBucketRemaining(ctx context.Context, db *gorm.DB, tierID int64) (int, error) {
	var sum int64
	err := db.WithContext(ctx).Model(&models.TicketTierBucket{}).
		Select("COALESCE(SUM(remaining_quota),0)").
		Where("tier_id = ?", tierID).Scan(&sum).Error
	return int(sum), err
}

func sumRushBucketRemaining(ctx context.Context, db *gorm.DB, campaignID int64) (int, error) {
	var sum int64
	err := db.WithContext(ctx).Model(&models.RushCampaignBucket{}).
		Select("COALESCE(SUM(remaining_quota),0)").
		Where("campaign_id = ?", campaignID).Scan(&sum).Error
	return int(sum), err
}

func resolveStockBucketForOrder(
	order *models.TicketOrder,
	messageBucket *int,
	settings InventoryBucketSettings,
	totalQuotaHint int,
) (bucketNo int, usedFallback bool) {
	if messageBucket != nil {
		return *messageBucket, false
	}
	if order != nil && order.StockBucketNo != nil {
		return *order.StockBucketNo, false
	}
	n := settings.BucketCount
	if n <= 0 {
		n = 8
	}
	if totalQuotaHint > 0 {
		n = settings.EffectiveBucketCount(totalQuotaHint)
	}
	userID := int64(0)
	if order != nil {
		userID = order.UserID
	}
	bucketNo, usedFallback = ResolveOrderBucketNo(nil, userID, n)
	if usedFallback {
		metrics.InventoryBucketFallback.Inc()
		log.Printf("inventory bucket fallback order=%d user=%d bucket=%d",
			orderIDOrZero(order), userID, bucketNo)
	}
	return bucketNo, usedFallback
}

func resolveRushBucketForOrder(
	order *models.TicketOrder,
	messageBucket *int,
	settings InventoryBucketSettings,
	totalQuotaHint int,
) (bucketNo int, usedFallback bool) {
	if messageBucket != nil {
		return *messageBucket, false
	}
	if order != nil && order.RushBucketNo != nil {
		return *order.RushBucketNo, false
	}
	// 抢票与票档同桶；无 rush 字段时回退 stock_bucket_no。
	if order != nil && order.StockBucketNo != nil {
		return *order.StockBucketNo, false
	}
	return resolveStockBucketForOrder(order, nil, settings, totalQuotaHint)
}

func orderIDOrZero(order *models.TicketOrder) int64 {
	if order == nil {
		return 0
	}
	return order.ID
}

func intPtr(v int) *int {
	return &v
}
