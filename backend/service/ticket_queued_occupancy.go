package service

import (
	"context"
	"fmt"
	"gofun/models"

	"gorm.io/gorm"
)

const queuedItemChunk = 500

type queuedStockOccupancy struct {
	byTier           map[int64]int
	byCampaign       map[int64]int
	byTierBucket     map[string]int
	byCampaignBucket map[string]int
}

// loadQueuedStockOccupancy 按块聚合 queued 订单项占用。
// 不能 Preload(Items)：queued 过万时 GORM 会生成过长 IN 列表，触发 MySQL Error 1390。
func loadQueuedStockOccupancy(
	ctx context.Context,
	db *gorm.DB,
	inventory InventoryBucketSettings,
) (queuedStockOccupancy, error) {
	occ := queuedStockOccupancy{
		byTier:           make(map[int64]int),
		byCampaign:       make(map[int64]int),
		byTierBucket:     make(map[string]int),
		byCampaignBucket: make(map[string]int),
	}
	var queuedOrders []models.TicketOrder
	if err := db.WithContext(ctx).
		Where("status = ?", models.TicketOrderStatusQueued).
		Find(&queuedOrders).Error; err != nil {
		return occ, err
	}
	if len(queuedOrders) == 0 {
		return occ, nil
	}

	type queuedItemRow struct {
		OrderID      int64 `gorm:"column:order_id"`
		TicketTierID int64 `gorm:"column:ticket_tier_id"`
		Quantity     int   `gorm:"column:quantity"`
	}
	orderIDs := make([]int64, 0, len(queuedOrders))
	orderByID := make(map[int64]models.TicketOrder, len(queuedOrders))
	for _, order := range queuedOrders {
		orderIDs = append(orderIDs, order.ID)
		orderByID[order.ID] = order
	}
	for start := 0; start < len(orderIDs); start += queuedItemChunk {
		end := start + queuedItemChunk
		if end > len(orderIDs) {
			end = len(orderIDs)
		}
		var itemRows []queuedItemRow
		if err := db.WithContext(ctx).Raw(`
			SELECT order_id, ticket_tier_id, quantity
			FROM ticket_order_item
			WHERE delete_time IS NULL AND order_id IN ?
		`, orderIDs[start:end]).Scan(&itemRows).Error; err != nil {
			return occ, err
		}
		for _, item := range itemRows {
			order, ok := orderByID[item.OrderID]
			if !ok {
				continue
			}
			occ.byTier[item.TicketTierID] += item.Quantity
			if order.RushSaleCampaignID != nil {
				occ.byCampaign[*order.RushSaleCampaignID] += item.Quantity
			}
			if inventory.Enabled {
				stockBucket := queuedOrderStockBucket(&order, inventory)
				occ.byTierBucket[fmt.Sprintf("%d:%d", item.TicketTierID, stockBucket)] += item.Quantity
				if order.RushSaleCampaignID != nil {
					rushBucket := queuedOrderRushBucket(&order, inventory)
					occ.byCampaignBucket[fmt.Sprintf("%d:%d", *order.RushSaleCampaignID, rushBucket)] += item.Quantity
				}
			}
		}
	}
	return occ, nil
}
