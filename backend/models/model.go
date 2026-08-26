package models

import (
	"time"

	"gorm.io/gorm"
)

type Base struct {
	ID         int64          `gorm:"primaryKey;column:id" json:"id,string"`
	UpdateTime time.Time      `gorm:"autoUpdateTime;column:update_time" json:"update_time"`
	CreateTime time.Time      `gorm:"autoCreateTime;column:create_time" json:"create_time"`
	DeleteTime gorm.DeletedAt `gorm:"index;column:delete_time" json:"-"`
}

// TODO: float64 money fields should migrate to int64 cents or shopspring/decimal.
type User struct {
	Base
	Username     string     `gorm:"unique;column:username;not null" json:"username"`
	Password     string     `gorm:"column:password;not null" json:"-"`
	Balance      float64    `gorm:"type:decimal(10,2)" json:"-"`
	BalanceCents int64      `gorm:"not null;default:100000" json:"-"`
	Phone        *string    `gorm:"size:20" json:"phone"`
	AvatarURL    string     `gorm:"size:512" json:"avatar_url"`
	Role         string     `gorm:"size:16;default:'user'" json:"role"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}
