package service

import (
	"fmt"
	"gofun/models"

	"gorm.io/gorm"
)

// restoreOrderInventory 归还本单占用的库存。选座走 session_seat，计数走票档/分桶。
// skipRedis 为 true 时调用方不得再回滚 Redis 票额。
func (s *TicketOrderService) restoreOrderInventory(
	tx *gorm.DB,
	order *models.TicketOrder,
) (stockBucketNo, rushBucketNo *int, skipRedis bool, err error) {
	if order == nil || len(order.Items) == 0 {
		return nil, nil, false, fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
	}
	released, err := releaseSessionSeats(tx, order.ID)
	if err != nil {
		return nil, nil, false, err
	}
	if released > 0 {
		return nil, nil, true, nil
	}
	if s.inventory.Enabled {
		stockBucketNo, rushBucketNo, err = s.releaseInventoryForOrder(tx, order)
		return stockBucketNo, rushBucketNo, false, err
	}
	for _, item := range order.Items {
		if err := restoreTierQuota(tx, item.TicketTierID, item.Quantity); err != nil {
			return nil, nil, false, err
		}
		if order.RushSaleCampaignID != nil {
			if err := tx.Model(&models.RushSaleCampaign{}).
				Where("id = ?", *order.RushSaleCampaignID).
				Update("remaining_quota", gorm.Expr("remaining_quota + ?", item.Quantity)).Error; err != nil {
				return nil, nil, false, err
			}
		}
	}
	return nil, nil, false, nil
}

// releaseInventoryForOrder 在订单状态事务中释放已 reserve 的库存。
// 订单状态和分桶库存位于同一主库，因此回补与取消/退款状态变更可以原子提交。
func (s *TicketOrderService) releaseInventoryForOrder(
	tx *gorm.DB,
	order *models.TicketOrder,
) (stockBucketNo, rushBucketNo *int, err error) {
	if order == nil || len(order.Items) == 0 {
		return nil, nil, fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
	}
	stockBucket, _ := resolveStockBucketForOrder(order, nil, s.inventory, 0)
	stockBucketNo = &stockBucket
	if order.RushSaleCampaignID != nil {
		rushBucket, _ := resolveRushBucketForOrder(order, nil, s.inventory, 0)
		rushBucketNo = &rushBucket
	}
	for _, item := range order.Items {
		if order.RushSaleCampaignID != nil && rushBucketNo != nil {
			if err := restoreRushBucket(tx, *order.RushSaleCampaignID, *rushBucketNo, item.Quantity); err != nil {
				return stockBucketNo, rushBucketNo, err
			}
		}
		if err := restoreTierBucket(tx, item.TicketTierID, stockBucket, item.Quantity); err != nil {
			return stockBucketNo, rushBucketNo, err
		}
	}
	return stockBucketNo, rushBucketNo, nil
}
