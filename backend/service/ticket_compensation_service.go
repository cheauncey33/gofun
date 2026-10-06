package service

import (
	"context"
	"errors"
	"fmt"
	"gofun/config"
	"gofun/container"
	"gofun/metrics"
	"gofun/models"
	"gofun/pkg/logger"
	"log"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const stockFullReconciliationInterval = 5 * time.Minute

// TicketCompensationService 统一管理订单超时、预扣恢复、退款恢复、库存归还与对账。
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

// RunCompensation 检查在售票档库存，返回发现的差异数。
func (s *TicketCompensationService) RunCompensation(ctx context.Context) (int, error) {
	pendingKeys := make(map[string]struct{})
	if s.order != nil {
		var err error
		pendingKeys, err = s.order.pendingInventoryKeys(ctx)
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
		log.Printf("%s Redis stock missing: expected=%d", description, expected)
		metrics.StockReconciliationMismatchTotal.WithLabelValues("missing").Inc()
		return true, nil
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
		log.Printf("%s Redis stock drift: expected=%d actual=%d", description, expected, redisStock)
		metrics.StockReconciliationMismatchTotal.WithLabelValues("too_high").Inc()
		return true, nil
	}
	if redisStock < int64(expected) && !hasPendingReservation {
		log.Printf("%s Redis stock drift: expected=%d actual=%d", description, expected, redisStock)
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
			models.TicketTierStatusWaitlist,
			models.TicketTierStatusSoldOut,
		}).Find(&tiers).Error; err != nil {
		return 0, err
	}
	if len(tiers) == 0 {
		metrics.CompensationRuns.WithLabelValues("success").Inc()
		return 0, nil
	}

	occ, err := loadQueuedStockOccupancy(ctx, s.db, s.inventory)
	if err != nil {
		return 0, err
	}
	queuedByTier := occ.byTier
	seatedTiers, err := seatedTicketTierIDs(s.db.WithContext(ctx))
	if err != nil {
		return 0, err
	}

	anomalies := 0
	for _, tier := range tiers {
		if _, skip := seatedTiers[tier.ID]; skip {
			continue
		}
		available := publicRedisExpected(tier.RemainingQuota, tier.WaitlistPending, queuedByTier[tier.ID], false)
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
	occ, err := loadQueuedStockOccupancy(ctx, s.db, s.inventory)
	if err != nil {
		return 0, err
	}
	queuedByBucket := occ.byTierBucket
	seatedTiers, err := seatedTicketTierIDs(s.db.WithContext(ctx))
	if err != nil {
		return 0, err
	}
	pendingByTier := make(map[int64]int)
	if len(buckets) > 0 {
		tierIDs := make([]int64, 0, len(buckets))
		seen := map[int64]struct{}{}
		for _, bucket := range buckets {
			if _, ok := seen[bucket.TierID]; ok {
				continue
			}
			seen[bucket.TierID] = struct{}{}
			tierIDs = append(tierIDs, bucket.TierID)
		}
		var parents []models.TicketTier
		if err := s.db.WithContext(ctx).Select("id", "waitlist_pending").
			Where("id IN ?", tierIDs).Find(&parents).Error; err != nil {
			return 0, err
		}
		for _, tier := range parents {
			pendingByTier[tier.ID] = tier.WaitlistPending
		}
	}

	anomalies := 0
	parentTierSum := make(map[int64]int)
	for _, bucket := range buckets {
		if _, skip := seatedTiers[bucket.TierID]; skip {
			continue
		}
		available := publicRedisExpected(
			bucket.RemainingQuota,
			pendingByTier[bucket.TierID],
			queuedByBucket[fmt.Sprintf("%d:%d", bucket.TierID, bucket.BucketNo)],
			true,
		)
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
					models.TicketTierStatusWaitlist,
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
	queuedByRushBucket := occ.byCampaignBucket
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

// Start 管理恢复任务的生命周期；超时扫描独立执行，避免被退款查询阻塞。
func (s *TicketCompensationService) Start(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		s.scanTimeouts(ctx)
	}()
	go func() {
		defer wg.Done()
		s.runRecovery(ctx)
	}()
	wg.Wait()
}

func (s *TicketCompensationService) scanTimeouts(ctx context.Context) {
	interval := s.order.scannerInterval
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			scanCtx, cancel := context.WithTimeout(ctx, interval)
			if err := s.order.CancelExpiredOrders(scanCtx); err != nil {
				logger.FromContext(ctx).Error("Payment timeout scan failed", zap.Error(err))
			}
			cancel()
		}
	}
}

// runRecovery 每 30 秒核查预扣、退款并执行归还，每 5 分钟对账。
func (s *TicketCompensationService) runRecovery(ctx context.Context) {
	ticker := time.NewTicker(stockReservationRecoveryInterval)
	defer ticker.Stop()
	nextFullReconciliation := time.Now().Add(stockFullReconciliationInterval)
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:

			if s.order != nil {

				if _, err := s.order.RecoverStaleStockReservations(ctx, now); err != nil {
					log.Printf("库存单笔恢复失败: %v", err)
				} else {
					metrics.StockRecoveryLastSuccessTimestamp.SetToCurrentTime()
				}
				if err := s.order.observePendingStockReservationMetrics(ctx, now); err != nil {
					log.Printf("采集库存预扣状态失败: %v", err)
				}
				// 重驱动卡在 refunding 的订单（CancelOrder 事务外调网关的崩溃残留）。
				if recovered, err := s.order.RecoverStuckRefunds(ctx); err != nil {
					log.Printf("退款悬挂恢复失败: %v", err)
				} else if recovered > 0 {
					log.Printf("退款悬挂恢复完成 %d 笔", recovered)
				}
				if _, err := s.order.RecoverStockReturns(ctx); err != nil {
					log.Printf("stock recovery: %v", err)
				}
				s.order.observeStockReturns(ctx)

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
