package models

type Address struct {
	Base
	UserID       int64  `gorm:"index;not null" json:"user_id"`
	ReceiverName string `gorm:"size:64;not null" json:"receiver_name" binding:"required"`
	Phone        string `gorm:"size:20;not null" json:"phone" binding:"required"`
	Province     string `gorm:"size:32" json:"province"`
	City         string `gorm:"size:32" json:"city"`
	District     string `gorm:"size:32" json:"district"`
	Detail       string `gorm:"size:256;not null" json:"detail"`
	IsDefault    bool   `gorm:"default:false" json:"is_default"`
}
