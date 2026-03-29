package models

import (
	"time"

	"gorm.io/gorm"
)

type Base struct {
	ID         int64          `gorm:"primaryKey;column:id"`
	UpdateTime time.Time      `gorm:"autoUpdateTime;column:update_time"`
	CreateTime time.Time      `gorm:"autoCreateTime;column:create_time"`
	DeleteTime gorm.DeletedAt `gorm:"index;column:delete_time"`
}
type Dormitory struct {
	Base
	BuildingName string `gorm:"size:64;column:building_name;not null"`
	RoomNumber   string `gorm:"size:32;column:room_number;not null"`
}
type User struct {
	Base
	Username string  `gorm:"unique;column:username;not null"`
	Password string  `gorm:"column:password;not null"`
	Balance  float64 `gorm:"type:decimal(10,2)"`
	DormID   int64
	Dorm     Dormitory `gorm:"foreignKey:DormID"`
}
type Product struct {
	Base
	Name  string  `gorm:"size:128;not null"`
	Price float64 `gorm:"type:decimal(10,2)"`
	Stock int     `gorm:"not null"`
}
type Order struct {
	Base
	UserID     int64
	User       User
	TotalPrice float64 `gorm:"type:decimal(10,2)"`
	Status     int     `gorm:"default:1"` //1-to be deliver 2-delivering 3-delivered
	//和User-Dormitory一样Order-OrderItem同样是多对一 为啥gorm的foreignKey tag一个定义在User（子）中，一个定义在Order（父）
	//这和实际应用相关：写在谁中说明需要谁preload得到另一方
	//同样的gormTag 不同的含义
	OrderItem []OrderItem `gorm:"foreignKey:OrderID"`
}
type OrderItem struct {
	Base
	OrderID       int64
	ProductID     int64
	Product       Product
	Quantity      int
	SnapshotPrice float64 `gorm:"type:decimal(10,2)"`
}
