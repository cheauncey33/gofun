package service

import (
	"context"
	"fmt"
	"gofun/container"
	"gofun/metrics"
	"gofun/models"
	"log"
	"sort"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// InventoryReservationBatchService 将 ticket_order 上的库存预约状态按库存桶合并处理。
// 订单确认事务只更新订单状态和预约目标状态，库存桶由这里批量更新。
type InventoryReservationBatchService struct {
	db          *gorm.DB
	inventoryDB *gorm.DB
	settings    InventoryBucketSettings
	sharded     bool

	mu          sync.Mutex
	lastGaugeAt time.Time
}

func NewInventoryReservationBatchService(c *container.Container, cfg InventoryBucketSettings) *InventoryReservationBatchService {
	inventoryDB := c.InventoryDB
	if inventoryDB == nil {
		inventoryDB = c.DB
	}
	return &InventoryReservationBatchService{
		db:          c.DB,
		inventoryDB: inventoryDB,
		settings:    cfg,
		sharded:     cfg.ShardEnabled,
	}
}

func (s *InventoryReservationBatchService) Start(ctx context.Context) {
	if !s.settings.Enabled || !s.settings.BatchEnabled {
		return
	}
	interval := s.settings.BatchInterval
	if interval <= 0 {
		interval = 50 * time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := s.ProcessBatch(ctx); err != nil && ctx.Err() == nil {
			log.Printf("inventory reservation batch failed: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type inventoryRushBucketKey struct {
	campaignID int64
	tierID     int64
	bucketNo   int
}

type inventoryTierBucketKey struct {
	tierID   int64
	bucketNo int
}

type inventoryReservationBatchStats struct {
	processed                   int
	reserved                    int
	released                    int
	releasedWithoutBucketUpdate int
}

// ProcessBatch 处理一批订单预约。库存更新和订单 applied_state 在同一个事务内推进，
// 事务失败时全部回滚，下一轮继续重试。
func (s *InventoryReservationBatchService) ProcessBatch(ctx context.Context) (int, error) {
	if !s.settings.Enabled || !s.settings.BatchEnabled {
		return 0, nil
	}
	if s.sharded {
		return s.processBatchSharded(ctx)
	}
	started := time.Now()
	stats := inventoryReservationBatchStats{}
	var selectedIDs []int64
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var candidateIDs []int64
		now := time.Now()
		candidateQuery := tx.Model(&models.TicketOrder{}).
			Select("id").
			Where("inventory_next_attempt_at <= ?", now).
			Where(
				"(inventory_desired_state = ? AND inventory_applied_state = ?) OR (inventory_desired_state = ? AND inventory_applied_state IN ?)",
				models.InventoryReservationReserved,
				models.InventoryReservationNone,
				models.InventoryReservationReleased,
				[]models.InventoryReservationState{
					models.InventoryReservationNone,
					models.InventoryReservationReserved,
				},
			).
			Order("id ASC").Limit(s.settings.BatchSize).Find(&candidateIDs)
		if candidateQuery.Error != nil {
			return candidateQuery.Error
		}
		if len(candidateIDs) == 0 {
			return nil
		}

		var orders []models.TicketOrder
		query := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Preload("Items").
			Where("id IN ?", candidateIDs).
			Where("inventory_next_attempt_at <= ?", now).
			Where(
				"(inventory_desired_state = ? AND inventory_applied_state = ?) OR (inventory_desired_state = ? AND inventory_applied_state IN ?)",
				models.InventoryReservationReserved,
				models.InventoryReservationNone,
				models.InventoryReservationReleased,
				[]models.InventoryReservationState{
					models.InventoryReservationNone,
					models.InventoryReservationReserved,
				},
			).
			Order("id ASC").Limit(s.settings.BatchSize).Find(&orders)
		if query.Error != nil {
			return query.Error
		}
		if len(orders) == 0 {
			return nil
		}

		selectedIDs = make([]int64, 0, len(orders))
		reserveRush := make(map[inventoryRushBucketKey]int)
		reserveTier := make(map[inventoryTierBucketKey]int)
		releaseRush := make(map[inventoryRushBucketKey]int)
		releaseTier := make(map[inventoryTierBucketKey]int)
		reservedIDs := make([]int64, 0, len(orders))
		releasedIDs := make([]int64, 0, len(orders))
		releasedWithoutBucketIDs := make([]int64, 0, len(orders))

		for _, order := range orders {
			if len(order.Items) != 1 {
				return fmt.Errorf("订单 %d 库存批处理明细异常", order.ID)
			}
			item := order.Items[0]
			stockBucket := queuedOrderStockBucket(&order, s.settings)
			quantity := item.Quantity
			selectedIDs = append(selectedIDs, order.ID)
			switch {
			case order.InventoryDesiredState == models.InventoryReservationReserved &&
				order.InventoryAppliedState == models.InventoryReservationNone:
				reservedIDs = append(reservedIDs, order.ID)
				reserveTier[inventoryTierBucketKey{tierID: item.TicketTierID, bucketNo: stockBucket}] += quantity
				if order.RushSaleCampaignID != nil {
					rushBucket := queuedOrderRushBucket(&order, s.settings)
					reserveRush[inventoryRushBucketKey{
						campaignID: *order.RushSaleCampaignID,
						tierID:     item.TicketTierID,
						bucketNo:   rushBucket,
					}] += quantity
				}
			case order.InventoryDesiredState == models.InventoryReservationReleased &&
				order.InventoryAppliedState == models.InventoryReservationReserved:
				releasedIDs = append(releasedIDs, order.ID)
				releaseTier[inventoryTierBucketKey{tierID: item.TicketTierID, bucketNo: stockBucket}] += quantity
				if order.RushSaleCampaignID != nil {
					rushBucket := queuedOrderRushBucket(&order, s.settings)
					releaseRush[inventoryRushBucketKey{
						campaignID: *order.RushSaleCampaignID,
						bucketNo:   rushBucket,
					}] += quantity
				}
			case order.InventoryDesiredState == models.InventoryReservationReleased &&
				order.InventoryAppliedState == models.InventoryReservationNone:
				releasedWithoutBucketIDs = append(releasedWithoutBucketIDs, order.ID)
			default:
				return fmt.Errorf("订单 %d 库存状态非法: desired=%s applied=%s", order.ID, order.InventoryDesiredState, order.InventoryAppliedState)
			}
		}

		if err := applyRushBucketGroups(tx, reserveRush, false); err != nil {
			return err
		}
		if err := applyRushBucketGroups(tx, releaseRush, true); err != nil {
			return err
		}
		if err := applyTierBucketGroups(tx, reserveTier, false); err != nil {
			return err
		}
		if err := applyTierBucketGroups(tx, releaseTier, true); err != nil {
			return err
		}

		if err := markInventoryOrders(tx, reservedIDs, models.InventoryReservationReserved, now); err != nil {
			return err
		}
		if err := markInventoryOrders(tx, releasedIDs, models.InventoryReservationReleased, now); err != nil {
			return err
		}
		if err := markInventoryOrders(tx, releasedWithoutBucketIDs, models.InventoryReservationReleased, now); err != nil {
			return err
		}
		stats = inventoryReservationBatchStats{
			processed:                   len(orders),
			reserved:                    len(reservedIDs),
			released:                    len(releasedIDs),
			releasedWithoutBucketUpdate: len(releasedWithoutBucketIDs),
		}
		return nil
	})

	result := "success"
	if err != nil {
		result = "error"
		metrics.InventoryReservationBatches.WithLabelValues(result).Inc()
		metrics.InventoryReservationBatchDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
		if len(selectedIDs) > 0 {
			s.recordBatchFailure(ctx, selectedIDs, err)
		}
		s.refreshPendingGauge(ctx)
		return 0, err
	}
	if stats.processed == 0 {
		metrics.InventoryReservationBatches.WithLabelValues("empty").Inc()
	} else {
		metrics.InventoryReservationBatches.WithLabelValues(result).Inc()
		metrics.InventoryReservationRows.WithLabelValues("reserved").Add(float64(stats.reserved))
		metrics.InventoryReservationRows.WithLabelValues("released").Add(float64(stats.released))
		metrics.InventoryReservationRows.WithLabelValues("released_without_bucket_update").Add(float64(stats.releasedWithoutBucketUpdate))
	}
	metrics.InventoryReservationBatchDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
	s.refreshPendingGauge(ctx)
	return stats.processed, nil
}

// processBatchSharded 将库存桶更新放到独立库存库，主库只持有订单行锁。
// 两边不是 XA：库存库的 operation_key 账本提供幂等屏障，库存库提交后主库
// 更新 applied_state；若主库提交失败，下一轮会命中账本而不会重复扣减。
func (s *InventoryReservationBatchService) processBatchSharded(ctx context.Context) (int, error) {
	started := time.Now()
	stats := inventoryReservationBatchStats{}
	var selectedIDs []int64
	err := s.db.WithContext(ctx).Transaction(func(mainTx *gorm.DB) error {
		now := time.Now()
		var candidateIDs []int64
		if err := mainTx.Model(&models.TicketOrder{}).
			Select("id").
			Where("inventory_next_attempt_at <= ?", now).
			Where(
				"(inventory_desired_state = ? AND inventory_applied_state = ?) OR (inventory_desired_state = ? AND inventory_applied_state IN ?)",
				models.InventoryReservationReserved,
				models.InventoryReservationNone,
				models.InventoryReservationReleased,
				[]models.InventoryReservationState{
					models.InventoryReservationNone,
					models.InventoryReservationReserved,
				},
			).
			Order("id ASC").Limit(s.settings.BatchSize).Find(&candidateIDs).Error; err != nil {
			return err
		}
		if len(candidateIDs) == 0 {
			return nil
		}

		var orders []models.TicketOrder
		if err := mainTx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Preload("Items").
			Where("id IN ?", candidateIDs).
			Where("inventory_next_attempt_at <= ?", now).
			Where(
				"(inventory_desired_state = ? AND inventory_applied_state = ?) OR (inventory_desired_state = ? AND inventory_applied_state IN ?)",
				models.InventoryReservationReserved,
				models.InventoryReservationNone,
				models.InventoryReservationReleased,
				[]models.InventoryReservationState{
					models.InventoryReservationNone,
					models.InventoryReservationReserved,
				},
			).
			Order("id ASC").Limit(s.settings.BatchSize).Find(&orders).Error; err != nil {
			return err
		}
		if len(orders) == 0 {
			return nil
		}

		selectedIDs = make([]int64, 0, len(orders))
		operations := make([]models.InventoryOperation, 0, len(orders)*2)
		reservedIDs := make([]int64, 0, len(orders))
		releasedIDs := make([]int64, 0, len(orders))
		releasedWithoutBucketIDs := make([]int64, 0, len(orders))

		for _, order := range orders {
			if len(order.Items) != 1 {
				return fmt.Errorf("订单 %d 库存批处理明细异常", order.ID)
			}
			item := order.Items[0]
			stockBucket := queuedOrderStockBucket(&order, s.settings)
			selectedIDs = append(selectedIDs, order.ID)
			switch {
			case order.InventoryDesiredState == models.InventoryReservationReserved &&
				order.InventoryAppliedState == models.InventoryReservationNone:
				reservedIDs = append(reservedIDs, order.ID)
				operations = append(operations, models.InventoryOperation{
					OperationKey: fmt.Sprintf("%d:reserve:tier", order.ID), OrderID: order.ID,
					Action: "reserve", TierID: item.TicketTierID, BucketNo: stockBucket, Quantity: item.Quantity,
				})
				if order.RushSaleCampaignID != nil {
					campaignID := *order.RushSaleCampaignID
					rushBucket := queuedOrderRushBucket(&order, s.settings)
					operations = append(operations, models.InventoryOperation{
						OperationKey: fmt.Sprintf("%d:reserve:rush", order.ID), OrderID: order.ID,
						Action: "reserve", TierID: item.TicketTierID, CampaignID: &campaignID,
						BucketNo: rushBucket, Quantity: item.Quantity,
					})
				}
			case order.InventoryDesiredState == models.InventoryReservationReleased &&
				order.InventoryAppliedState == models.InventoryReservationReserved:
				releasedIDs = append(releasedIDs, order.ID)
				operations = append(operations, models.InventoryOperation{
					OperationKey: fmt.Sprintf("%d:release:tier", order.ID), OrderID: order.ID,
					Action: "release", TierID: item.TicketTierID, BucketNo: stockBucket, Quantity: item.Quantity,
				})
				if order.RushSaleCampaignID != nil {
					campaignID := *order.RushSaleCampaignID
					rushBucket := queuedOrderRushBucket(&order, s.settings)
					operations = append(operations, models.InventoryOperation{
						OperationKey: fmt.Sprintf("%d:release:rush", order.ID), OrderID: order.ID,
						Action: "release", TierID: item.TicketTierID, CampaignID: &campaignID,
						BucketNo: rushBucket, Quantity: item.Quantity,
					})
				}
			case order.InventoryDesiredState == models.InventoryReservationReleased &&
				order.InventoryAppliedState == models.InventoryReservationNone:
				releasedWithoutBucketIDs = append(releasedWithoutBucketIDs, order.ID)
			default:
				return fmt.Errorf("订单 %d 库存状态非法: desired=%s applied=%s", order.ID, order.InventoryDesiredState, order.InventoryAppliedState)
			}
		}

		if len(operations) > 0 {
			if err := s.inventoryDB.WithContext(ctx).Transaction(func(inventoryTx *gorm.DB) error {
				newReserveRush := make(map[inventoryRushBucketKey]int)
				newReserveTier := make(map[inventoryTierBucketKey]int)
				newReleaseRush := make(map[inventoryRushBucketKey]int)
				newReleaseTier := make(map[inventoryTierBucketKey]int)
				for _, operation := range operations {
					result := inventoryTx.Clauses(clause.OnConflict{DoNothing: true}).Create(&operation)
					if result.Error != nil {
						return result.Error
					}
					if result.RowsAffected == 0 {
						continue
					}
					if operation.CampaignID == nil {
						key := inventoryTierBucketKey{tierID: operation.TierID, bucketNo: operation.BucketNo}
						if operation.Action == "reserve" {
							newReserveTier[key] += operation.Quantity
						} else {
							newReleaseTier[key] += operation.Quantity
						}
						continue
					}
					key := inventoryRushBucketKey{campaignID: *operation.CampaignID, tierID: operation.TierID, bucketNo: operation.BucketNo}
					if operation.Action == "reserve" {
						newReserveRush[key] += operation.Quantity
					} else {
						newReleaseRush[key] += operation.Quantity
					}
				}
				if err := applyRushBucketGroupsWithoutParent(inventoryTx, newReserveRush, false); err != nil {
					return err
				}
				if err := applyRushBucketGroupsWithoutParent(inventoryTx, newReleaseRush, true); err != nil {
					return err
				}
				if err := applyTierBucketGroupsWithoutParent(inventoryTx, newReserveTier, false); err != nil {
					return err
				}
				return applyTierBucketGroupsWithoutParent(inventoryTx, newReleaseTier, true)
			}); err != nil {
				return err
			}
		}

		if err := markInventoryOrders(mainTx, reservedIDs, models.InventoryReservationReserved, now); err != nil {
			return err
		}
		if err := markInventoryOrders(mainTx, releasedIDs, models.InventoryReservationReleased, now); err != nil {
			return err
		}
		if err := markInventoryOrders(mainTx, releasedWithoutBucketIDs, models.InventoryReservationReleased, now); err != nil {
			return err
		}
		stats = inventoryReservationBatchStats{
			processed: len(orders), reserved: len(reservedIDs), released: len(releasedIDs),
			releasedWithoutBucketUpdate: len(releasedWithoutBucketIDs),
		}
		return nil
	})

	result := "success"
	if err != nil {
		result = "error"
		metrics.InventoryReservationBatches.WithLabelValues(result).Inc()
		metrics.InventoryReservationBatchDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
		if len(selectedIDs) > 0 {
			s.recordBatchFailure(ctx, selectedIDs, err)
		}
		s.refreshPendingGauge(ctx)
		return 0, err
	}
	if stats.processed == 0 {
		metrics.InventoryReservationBatches.WithLabelValues("empty").Inc()
	} else {
		metrics.InventoryReservationBatches.WithLabelValues(result).Inc()
		metrics.InventoryReservationRows.WithLabelValues("reserved").Add(float64(stats.reserved))
		metrics.InventoryReservationRows.WithLabelValues("released").Add(float64(stats.released))
		metrics.InventoryReservationRows.WithLabelValues("released_without_bucket_update").Add(float64(stats.releasedWithoutBucketUpdate))
	}
	metrics.InventoryReservationBatchDuration.WithLabelValues(result).Observe(time.Since(started).Seconds())
	s.refreshPendingGauge(ctx)
	return stats.processed, nil
}

func applyRushBucketGroupsWithoutParent(tx *gorm.DB, groups map[inventoryRushBucketKey]int, restore bool) error {
	keys := make([]inventoryRushBucketKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].campaignID != keys[j].campaignID {
			return keys[i].campaignID < keys[j].campaignID
		}
		return keys[i].bucketNo < keys[j].bucketNo
	})
	for _, key := range keys {
		quantity := groups[key]
		var err error
		if restore {
			err = restoreRushBucket(tx, key.campaignID, key.bucketNo, quantity)
		} else {
			err = deductRushBucketWithoutParent(tx, key.campaignID, key.bucketNo, quantity)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func applyTierBucketGroupsWithoutParent(tx *gorm.DB, groups map[inventoryTierBucketKey]int, restore bool) error {
	keys := make([]inventoryTierBucketKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tierID != keys[j].tierID {
			return keys[i].tierID < keys[j].tierID
		}
		return keys[i].bucketNo < keys[j].bucketNo
	})
	for _, key := range keys {
		quantity := groups[key]
		var err error
		if restore {
			err = restoreTierBucketWithoutParent(tx, key.tierID, key.bucketNo, quantity)
		} else {
			err = deductTierBucketWithoutParent(tx, key.tierID, key.bucketNo, quantity)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func applyRushBucketGroups(tx *gorm.DB, groups map[inventoryRushBucketKey]int, restore bool) error {
	keys := make([]inventoryRushBucketKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].campaignID != keys[j].campaignID {
			return keys[i].campaignID < keys[j].campaignID
		}
		return keys[i].bucketNo < keys[j].bucketNo
	})
	for _, key := range keys {
		quantity := groups[key]
		var err error
		if restore {
			err = restoreRushBucket(tx, key.campaignID, key.bucketNo, quantity)
		} else {
			err = deductRushBucket(tx, key.campaignID, key.tierID, key.bucketNo, quantity)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func applyTierBucketGroups(tx *gorm.DB, groups map[inventoryTierBucketKey]int, restore bool) error {
	keys := make([]inventoryTierBucketKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].tierID != keys[j].tierID {
			return keys[i].tierID < keys[j].tierID
		}
		return keys[i].bucketNo < keys[j].bucketNo
	})
	for _, key := range keys {
		quantity := groups[key]
		var err error
		if restore {
			err = restoreTierBucket(tx, key.tierID, key.bucketNo, quantity)
		} else {
			err = deductTierBucket(tx, key.tierID, key.bucketNo, quantity)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func markInventoryOrders(tx *gorm.DB, ids []int64, state models.InventoryReservationState, now time.Time) error {
	if len(ids) == 0 {
		return nil
	}
	return tx.Model(&models.TicketOrder{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"inventory_applied_state":   state,
			"inventory_applied_at":      now,
			"inventory_last_error":      "",
			"inventory_retry_count":     0,
			"inventory_next_attempt_at": now,
		}).Error
}

func (s *InventoryReservationBatchService) recordBatchFailure(ctx context.Context, ids []int64, batchErr error) {
	if len(ids) == 0 || ctx.Err() != nil {
		return
	}
	next := time.Now().Add(100 * time.Millisecond)
	_ = s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("id IN ?", ids).
		Updates(map[string]interface{}{
			"inventory_last_error":      batchErr.Error(),
			"inventory_retry_count":     gorm.Expr("inventory_retry_count + 1"),
			"inventory_next_attempt_at": next,
		}).Error
}

func (s *InventoryReservationBatchService) refreshPendingGauge(ctx context.Context) {
	s.mu.Lock()
	if time.Since(s.lastGaugeAt) < time.Second {
		s.mu.Unlock()
		return
	}
	s.lastGaugeAt = time.Now()
	s.mu.Unlock()
	var count int64
	if err := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("inventory_desired_state <> inventory_applied_state").Count(&count).Error; err != nil {
		return
	}
	metrics.InventoryReservationPending.Set(float64(count))
}
