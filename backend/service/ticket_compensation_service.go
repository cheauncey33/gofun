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

// TicketCompensationService 对齐 Gofun 票档 Redis 库存与 MySQL 可用量，只下调或补缺。
type TicketCompensationService struct {
	db          *gorm.DB
	inventoryDB *gorm.DB
	rdb         *redis.Client
	inventory   InventoryBucketSettings
}

func NewTicketCompensationService(c *container.Container) *TicketCompensationService {
	return &TicketCompensationService{db: c.DB, inventoryDB: c.InventoryDB, rdb: c.RDB}
}

func (s *TicketCompensationService) inventoryDatabase() *gorm.DB {
	if s.inventory.ShardEnabled && s.inventoryDB != nil {
		return s.inventoryDB
	}
	return s.db
}

func (s *TicketCompensationService) ConfigureInventory(cfg config.InventoryConfig) {
	s.inventory = NewInventoryBucketSettings(cfg)
}

// RunCompensation 对 on_sale / sold_out 票档做一次对账，返回修复的异常数。
func (s *TicketCompensationService) RunCompensation(ctx context.Context) (int, error) {
	if s.inventory.Enabled {
		return s.runBucketCompensation(ctx)
	}
	return s.runStandardCompensation(ctx)
}

func (s *TicketCompensationService) runStandardCompensation(ctx context.Context) (int, error) {
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
		redisStockStr, err := s.rdb.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			if tier.Status == models.TicketTierStatusOnSale {
				if setErr := s.rdb.Set(ctx, key, available, 0).Err(); setErr != nil {
					return anomalies, setErr
				}
				log.Printf("票额缓存缺失:票档[%d], 已按可用量%d重建", tier.ID, available)
				anomalies++
			}
			continue
		}
		if err != nil {
			return anomalies, err
		}
		redisStock, err := strconv.ParseInt(redisStockStr, 10, 64)
		if err != nil {
			continue
		}
		if redisStock > int64(available) {
			if setErr := s.rdb.Set(ctx, key, available, 0).Err(); setErr != nil {
				return anomalies, setErr
			}
			log.Printf(
				"票额超卖风险:票档[%d],可用:%d,Redis:%d",
				tier.ID, available, redisStock,
			)
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

func (s *TicketCompensationService) runBucketCompensation(ctx context.Context) (int, error) {
	var buckets []models.TicketTierBucket
	if err := s.inventoryDatabase().WithContext(ctx).Find(&buckets).Error; err != nil {
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
		redisStockStr, err := s.rdb.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			if setErr := s.rdb.Set(ctx, key, available, 0).Err(); setErr != nil {
				return anomalies, setErr
			}
			log.Printf("分桶票额缓存缺失:票档[%d]桶[%d], 已按可用量%d重建", bucket.TierID, bucket.BucketNo, available)
			anomalies++
			continue
		}
		if err != nil {
			return anomalies, err
		}
		redisStock, err := strconv.ParseInt(redisStockStr, 10, 64)
		if err != nil {
			continue
		}
		if redisStock > int64(available) {
			if setErr := s.rdb.Set(ctx, key, available, 0).Err(); setErr != nil {
				return anomalies, setErr
			}
			log.Printf(
				"分桶票额超卖风险:票档[%d]桶[%d],可用:%d,Redis:%d",
				bucket.TierID, bucket.BucketNo, available, redisStock,
			)
			anomalies++
		}
	}

	// 刷新父表 remaining/sold 展示字段（非热路径）
	for tierID, remaining := range parentTierSum {
		var sold int64
		if err := s.inventoryDatabase().WithContext(ctx).Model(&models.TicketTierBucket{}).
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
	if err := s.inventoryDatabase().WithContext(ctx).Find(&rushBuckets).Error; err != nil {
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
		redisStockStr, err := s.rdb.Get(ctx, key).Result()
		if errors.Is(err, redis.Nil) {
			if setErr := s.rdb.Set(ctx, key, available, 0).Err(); setErr != nil {
				return anomalies, setErr
			}
			anomalies++
			continue
		}
		if err != nil {
			return anomalies, err
		}
		redisStock, err := strconv.ParseInt(redisStockStr, 10, 64)
		if err != nil {
			continue
		}
		if redisStock > int64(available) {
			if setErr := s.rdb.Set(ctx, key, available, 0).Err(); setErr != nil {
				return anomalies, setErr
			}
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

// Start 每 5 分钟执行一次票额对账。
func (s *TicketCompensationService) Start(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.RunCompensation(ctx); err != nil {
				log.Printf("票额对账失败: %v", err)
			}
		}
	}
}
