package models

import "time"

type SeckillStatus int

const (
	SeckillStatusPending SeckillStatus = 0
	SeckillStatusActive  SeckillStatus = 1
	SeckillStatusEnded   SeckillStatus = 2
)

type SeckillActivity struct {
	Base
	Name           string        `gorm:"size:128;not null" json:"name"`
	ProductID      int64         `gorm:"index;not null" json:"product_id"`
	Product        Product       `gorm:"foreignKey:ProductID" json:"product"`
	SeckillPrice   float64       `gorm:"type:decimal(10,2);not null" json:"seckill_price"`
	Stock          int           `gorm:"not null" json:"stock"`
	RemainingStock int           `gorm:"default:0" json:"remaining_stock"`
	StartTime      time.Time     `gorm:"not null;index" json:"start_time"`
	EndTime        time.Time     `gorm:"not null;index" json:"end_time"`
	LimitPerUser   int           `gorm:"default:1" json:"limit_per_user"`
	Status         SeckillStatus `gorm:"default:0;index" json:"status"`
}
