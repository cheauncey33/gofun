package models

type SeckillOrder struct {
	Base
	UserID     int64   `gorm:"index;not null" json:"user_id"`
	ActivityID int64   `gorm:"index;not null" json:"activity_id"`
	OrderID    int64   `gorm:"uniqueIndex" json:"order_id"`
	Amount     float64 `gorm:"type:decimal(10,2)" json:"amount"`
}
