package service

import (
	"context"
	"errors"
	"fmt"
	"gofun/config"
	"gofun/container"
	"gofun/metrics"
	"gofun/models"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const stockFullReconciliationInterval = 5 * time.Minute

var lowerRedisStockIfUnchangedScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call('SET', KEYS[1], ARGV[2])
return 1
`)

// TicketCompensationService 是统一的库存恢复 Worker：高频处理单笔 Redis 预扣，
// 低频按 MySQL 可用量做全量库存对账。
type TicketCompensationService struct {
	db        *gorm.DB
	rdb       *redis.Client
	order     *TicketOrderService
	inventory InventoryBucketSettings
}

func NewTicketCompensationService(
	c *container.Container,
	order *TicketOrderService,
) *TicketCompensationService {
	db := c.WorkerDB
	if db == nil {
		db = c.DB
	}
	return &TicketCompensationService{db: db, rdb: c.RDB, order: order}
}

func (s *TicketCompensationService) ConfigureInventory(cfg config.InventoryConfig) {
	s.inventory = NewInventoryBucketSettings(cfg)
}

// RunCompensation 对 on_sale / sold_out 票档做一次对账，返回修复的异常数。
func (s *TicketCompensationService) RunCompensation(ctx context.Context) (int, error) {
	pendingKeys := make(map[string]struct{})
	if s.order != nil {
		var err error
		pendingKeys, err = s.order.pendingStockReservationKeys(ctx)
		if err != nil {
			return 0, fmt.Errorf("读取在途库存预扣: %w", err)
		}
	}
	if s.inventory.Enabled {
		return s.runBucketCompensation(ctx, pendingKeys)
	}
	return s.runStandardCompensation(ctx, pendingKeys)
}

func (s *TicketCompensationService) reconcileRedisStock(
	ctx context.Context,
	key string,
	expected int,
	description string,
	rebuildMissing bool,
	pendingKeys map[string]struct{},
) (bool, error) {
	_, hasPendingReservation := pendingKeys[key]
	redisStockStr, err := s.rdb.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		if !rebuildMissing {
			return false, nil
		}
		if hasPendingReservation {
			log.Printf("%s Redis 库存缺失但仍有在途预扣，暂不重建", description)
			metrics.StockReconciliationMismatchTotal.WithLabelValues("pending_missing").Inc()
			return true, nil
		}
		created, setErr := s.rdb.SetNX(ctx, key, expected, 0).Result()
		if setErr != nil {
			return false, setErr
		}
		if created {
			log.Printf("%s Redis 库存缺失，已按安全可用量%d重建", description, expected)
			metrics.StockReconciliationMismatchTotal.WithLabelValues("missing").Inc()
			return true, nil
		}
		return false, nil
	}
	if err != nil {
		return false, err
	}
	redisStock, err := strconv.ParseInt(redisStockStr, 10, 64)
	if err != nil {
		log.Printf("%s Redis 库存值非法: %q", description, redisStockStr)
		metrics.StockReconciliationMismatchTotal.WithLabelValues("invalid").Inc()
		return true, nil
	}
	if redisStock > int64(expected) {
		changed, err := lowerRedisStockIfUnchangedScript.Run(
			ctx, s.rdb, []string{key}, redisStockStr, expected,
		).Int64()
		if err != nil {
			return false, err
		}
		if changed == 1 {
			log.Printf("%s Redis 库存偏多，已从%d下调为%d", description, redisStock, expected)
		} else {
			log.Printf("%s 对账期间库存发生并发变化，本轮不覆盖", description)
		}
		metrics.StockReconciliationMismatchTotal.WithLabelValues("too_high").Inc()
		return true, nil
	}
	if redisStock < int64(expected) && !hasPendingReservation {
		// 偏少可能来自历史泄漏；没有单笔凭证能够解释时只告警，不猜测性加库存。
		log.Printf("%s Redis 库存偏少:安全可用量=%d,Redis=%d，本轮不自动上调", description, expected, redisStock)
		metrics.StockReconciliationMismatchTotal.WithLabelValues("too_low").Inc()
		return true, nil
	}
	return false, nil
}

func (s *TicketCompensationService) runStandardCompensation(
	ctx context.Context,
	pendingKeys map[string]struct{},
) (int, error) {
	var tiers []models.TicketTier
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []models.TicketTierStatus{
			models.TicketTierStatusOnSale,
			models.TicketTierStatusSoldOut,
		}).Find(&tiers).Error; err != nil {
		return 0, err
	}
	if len(tiers) == 0 {
		metrics.CompensationRuns.WithLabelValues("success").Inc()
		return 0, nil
	}

	var queuedOrders []models.TicketOrder
	if err := s.db.WithContext(ctx).Preload("Items").
		Where("status = ?", models.TicketOrderStatusQueued).
		Find(&queuedOrders).Error; err != nil {
		return 0, err
	}
	queuedByTier := make(map[int64]int)
	for _, order := range queuedOrders {
		for _, item := range order.Items {
			queuedByTier[item.TicketTierID] += item.Quantity
		}
	}

	anomalies := 0
	for _, tier := range tiers {
		available := tier.RemainingQuota - queuedByTier[tier.ID]
		if available < 0 {
			available = 0
		}
		key := ticketStockKey(tier.ID)
		anomaly, err := s.reconcileRedisStock(
			ctx, key, available, fmt.Sprintf("票档[%d]", tier.ID),
			tier.Status == models.TicketTierStatusOnSale, pendingKeys,
		)
		if err != nil {
			return anomalies, err
		}
		if anomaly {
			anomalies++
		}
	}

	if anomalies > 0 {
		metrics.CompensationRuns.WithLabelValues("anomalies_found").Inc()
	} else {
		metrics.CompensationRuns.WithLabelValues("success").Inc()
	}
	return anomalies, nil
}

func (s *TicketCompensationService) runBucketCompensation(
	ctx context.Context,
	pendingKeys map[string]struct{},
) (int, error) {
	var buckets []models.TicketTierBucket
	if err := s.db.WithContext(ctx).Find(&buckets).Error; err != nil {
		return 0, err
	}
	var queuedOrders []models.TicketOrder
	if err := s.db.WithContext(ctx).Preload("Items").
		Where("status = ?", models.TicketOrderStatusQueued).
		Find(&queuedOrders).Error; err != nil {
		return 0, err
	}
	queuedByBucket := make(map[string]int)
	for _, order := range queuedOrders {
		for _, item := range order.Items {
			b := queuedOrderStockBucket(&order, s.inventory)
			queuedByBucket[fmt.Sprintf("%d:%d", item.TicketTierID, b)] += item.Quantity
		}
	}

	anomalies := 0
	parentTierSum := make(map[int64]int)
	for _, bucket := range buckets {
		available := bucket.RemainingQuota - queuedByBucket[fmt.Sprintf("%d:%d", bucket.TierID, bucket.BucketNo)]
		if available < 0 {
			available = 0
		}
		parentTierSum[bucket.TierID] += bucket.RemainingQuota
		key := TicketStockBucketKey(bucket.TierID, bucket.BucketNo)
		anomaly, err := s.reconcileRedisStock(
			ctx, key, available,
			fmt.Sprintf("票档[%d]桶[%d]", bucket.TierID, bucket.BucketNo),
			true, pendingKeys,
		)
		if err != nil {
			return anomalies, err
		}
		if anomaly {
			anomalies++
		}
	}

	// 刷新父表 remaining/sold 展示字段（非热路径）
	for tierID, remaining := range parentTierSum {
		var sold int64
		if err := s.db.WithContext(ctx).Model(&models.TicketTierBucket{}).
			Select("COALESCE(SUM(sold_count),0)").
			Where("tier_id = ?", tierID).Scan(&sold).Error; err != nil {
			return anomalies, err
		}
		if err := s.db.WithContext(ctx).Model(&models.TicketTier{}).
			Where("id = ?", tierID).
			Updates(map[string]interface{}{
				"remaining_quota": remaining,
				"sold_count":      sold,
				// 父级库存数量和状态只在补偿任务中汇总修正，
				// 不让普通分桶扣减持续更新 ticket_tier 热行。
				"status": gorm.Expr(
					"CASE "+
						"WHEN ? = 0 AND status = ? THEN ? "+
						"WHEN ? > 0 AND status = ? THEN ? "+
						"ELSE status END",
					remaining,
					models.TicketTierStatusOnSale,
					models.TicketTierStatusSoldOut,
					remaining,
					models.TicketTierStatusSoldOut,
					models.TicketTierStatusOnSale,
				),
			}).Error; err != nil {
			return anomalies, err
		}
	}

	var rushBuckets []models.RushCampaignBucket
	if err := s.db.WithContext(ctx).Find(&rushBuckets).Error; err != nil {
		return anomalies, err
	}
	queuedByRushBucket := make(map[string]int)
	for _, order := range queuedOrders {
		if order.RushSaleCampaignID == nil {
			continue
		}
		for _, item := range order.Items {
			b := queuedOrderRushBucket(&order, s.inventory)
			queuedByRushBucket[fmt.Sprintf("%d:%d", *order.RushSaleCampaignID, b)] += item.Quantity
			_ = item
		}
	}
	parentCampaignSum := make(map[int64]int)
	for _, bucket := range rushBuckets {
		available := bucket.RemainingQuota - queuedByRushBucket[fmt.Sprintf("%d:%d", bucket.CampaignID, bucket.BucketNo)]
		if available < 0 {
			available = 0
		}
		parentCampaignSum[bucket.CampaignID] += bucket.RemainingQuota
		key := RushStockBucketKey(bucket.CampaignID, bucket.BucketNo)
		anomaly, err := s.reconcileRedisStock(
			ctx, key, available,
			fmt.Sprintf("限时开售[%d]桶[%d]", bucket.CampaignID, bucket.BucketNo),
			true, pendingKeys,
		)
		if err != nil {
			return anomalies, err
		}
		if anomaly {
			anomalies++
		}
	}
	for campaignID, remaining := range parentCampaignSum {
		if err := s.db.WithContext(ctx).Model(&models.RushSaleCampaign{}).
			Where("id = ?", campaignID).
			Update("remaining_quota", remaining).Error; err != nil {
			return anomalies, err
		}
	}

	if anomalies > 0 {
		metrics.CompensationRuns.WithLabelValues("anomalies_found").Inc()
	} else {
		metrics.CompensationRuns.WithLabelValues("success").Inc()
	}
	return anomalies, nil
}

// Start 每 30 秒执行单笔恢复，并在同一个 Worker 内每 5 分钟执行一次全量对账。
func (s *TicketCompensationService) Start(ctx context.Context) {
	ticker := time.NewTicker(stockReservationRecoveryInterval)
	defer ticker.Stop()
	nextFullReconciliation := time.Now().Add(stockFullReconciliationInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if s.order != nil {
				recoveryOK := true
				if _, err := s.order.RecoverStaleStockReservations(ctx, now); err != nil {
					recoveryOK = false
					log.Printf("库存单笔恢复失败: %v", err)
				}
				if err := s.order.observePendingStockReservationMetrics(ctx, now); err != nil {
					recoveryOK = false
					metrics.StockReservationRecoveryTotal.WithLabelValues("error").Inc()
					log.Printf("采集库存预扣状态失败: %v", err)
				}
				if recoveryOK {
					metrics.StockRecoveryLastSuccessTimestamp.SetToCurrentTime()
				}
			}
			if !now.Before(nextFullReconciliation) {
				if _, err := s.RunCompensation(ctx); err != nil {
					metrics.CompensationRuns.WithLabelValues("error").Inc()
					log.Printf("票额对账失败: %v", err)
				}
				nextFullReconciliation = now.Add(stockFullReconciliationInterval)
			}
		}
	}
}
