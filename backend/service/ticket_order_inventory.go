package service

import (
	"fmt"
	"gofun/models"

	"gorm.io/gorm"
)

// releaseInventoryForOrder 在订单状态事务中释放已 reserve 的库存。
// 订单状态和分桶库存位于同一主库，因此回补与取消/退款状态变更可以原子提交。
func (s *TicketOrderService) releaseInventoryForOrder(
	tx *gorm.DB,
	order *models.TicketOrder,
) (stockBucketNo, rushBucketNo *int, err error) {
	if order == nil || len(order.Items) != 1 {
		return nil, nil, fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
	}
	stockBucket, _ := resolveStockBucketForOrder(order, nil, s.inventory, 0)
	stockBucketNo = &stockBucket
	if order.RushSaleCampaignID != nil {
		rushBucket, _ := resolveRushBucketForOrder(order, nil, s.inventory, 0)
		rushBucketNo = &rushBucket
		if err := restoreRushBucket(tx, *order.RushSaleCampaignID, rushBucket, order.Items[0].Quantity); err != nil {
			return stockBucketNo, rushBucketNo, err
		}
	}
	if err := restoreTierBucket(tx, order.Items[0].TicketTierID, stockBucket, order.Items[0].Quantity); err != nil {
		return stockBucketNo, rushBucketNo, err
	}
	return stockBucketNo, rushBucketNo, nil
}
