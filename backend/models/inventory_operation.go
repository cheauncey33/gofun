package models

import "time"

// InventoryOperation 是库存库中的幂等账本。订单状态仍以主库为准；库存库
// 先用 operation_key 去重，再修改 bucket，避免跨库重试重复扣减库存。
type InventoryOperation struct {
	OperationKey string    `gorm:"primaryKey;size:96" json:"operation_key"`
	OrderID      int64     `gorm:"not null;index" json:"order_id,string"`
	Action       string    `gorm:"size:16;not null" json:"action"`
	TierID       int64     `gorm:"not null;index" json:"tier_id,string"`
	CampaignID   *int64    `gorm:"index" json:"campaign_id,string,omitempty"`
	BucketNo     int       `gorm:"not null" json:"bucket_no"`
	Quantity     int       `gorm:"not null" json:"quantity"`
	AppliedAt    time.Time `gorm:"not null;autoCreateTime" json:"applied_at"`
}

func (InventoryOperation) TableName() string {
	return "inventory_operation"
}
