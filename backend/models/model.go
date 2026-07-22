package models

import (
	"time"

	"gorm.io/gorm"
)

type Base struct {
	ID         int64          `gorm:"primaryKey;column:id" json:"id,string"`
	UpdateTime time.Time      `gorm:"autoUpdateTime;column:update_time"`
	CreateTime time.Time      `gorm:"autoCreateTime;column:create_time"`
	DeleteTime gorm.DeletedAt `gorm:"index;column:delete_time"`
}
type Dormitory struct {
	Base
	BuildingName string `gorm:"size:64;column:building_name;not null"`
	RoomNumber   string `gorm:"size:32;column:room_number;not null"`
}

// TODO: float64 money fields (Balance, Price, TotalPrice etc.) should be changed
// to int64 (cents) or use shopspring/decimal to avoid IEEE 754 rounding errors.
type User struct {
	Base
	Username string  `gorm:"unique;column:username;not null" json:"username"`
	Password string  `gorm:"column:password;not null" json:"-"`
	Balance  float64 `gorm:"type:decimal(10,2)" json:"balance"`
	// BalanceCents 是票务领域的余额来源。Balance 仅供遗留零食模块回滚使用。
	BalanceCents int64      `gorm:"not null;default:100000" json:"balance_cents"`
	Phone        *string    `gorm:"size:20" json:"phone"`
	AvatarURL    string     `gorm:"size:512" json:"avatar_url"`
	DormID       int64      `json:"dorm_id"`
	Dorm         Dormitory  `gorm:"foreignKey:DormID" json:"dorm,omitempty"`
	Role         string     `gorm:"size:16;default:'user'" json:"role"`
	LastLoginAt  *time.Time `json:"last_login_at"`
}
type ProductStatus int

const (
	ProductStatusOffSale ProductStatus = 0
	ProductStatusOnSale  ProductStatus = 1
)

type Product struct {
	Base
	Name        string        `gorm:"size:128;not null;index" json:"name"`
	Description string        `gorm:"type:text" json:"description"`
	Price       float64       `gorm:"type:decimal(10,2);not null" json:"price"`
	Stock       int           `gorm:"not null;default:0" json:"stock"`
	ImageURL    string        `gorm:"size:512" json:"image_url"`
	CategoryID  *int64        `gorm:"index" json:"category_id"`
	Category    Category      `gorm:"foreignKey:CategoryID" json:"category,omitempty"`
	Status      ProductStatus `gorm:"default:1;index" json:"status"`
	SalesCount  int64         `gorm:"default:0" json:"sales_count"`
}
type OrderStatus int

const (
	OrderStatusPending   OrderStatus = 1
	OrderStatusPaid      OrderStatus = 2
	OrderStatusCompleted OrderStatus = 3
	OrderStatusCancelled OrderStatus = 5
)

var orderTransitionMap = map[OrderStatus][]OrderStatus{
	OrderStatusPending:   {OrderStatusPaid, OrderStatusCancelled},
	OrderStatusPaid:      {OrderStatusCompleted, OrderStatusCancelled},
	OrderStatusCompleted: {OrderStatusCancelled},
}

// HasBeenPaid 表示订单是否已经扣过款。
// 在"支付时扣款"模型下,余额只在 PayOrder(Pending→Paid)时扣减,
// 因此 Paid 和 Completed 意味着已扣款,取消时需退款;
// Pending 和 Cancelled 从未扣款,取消时只退库存不退钱。
func (o OrderStatus) HasBeenPaid() bool {
	return o == OrderStatusPaid || o == OrderStatusCompleted
}

func (o OrderStatus) CanTransitionTo(target OrderStatus) bool {
	allowed, ok := orderTransitionMap[o]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == target {
			return true
		}
	}
	return false
}

func (o OrderStatus) String() string {
	switch o {
	case OrderStatusPending:
		return "pending"
	case OrderStatusPaid:
		return "paid"
	case OrderStatusCompleted:
		return "completed"
	case OrderStatusCancelled:
		return "cancelled"
	default:
		return "unknown"
	}
}

type Order struct {
	Base
	UserID       int64       `gorm:"index;not null" json:"user_id"`
	User         User        `json:"user,omitempty"`
	AddressID    *int64      `json:"address_id"`
	TotalPrice   float64     `gorm:"type:decimal(10,2)" json:"total_price"`
	Status       OrderStatus `gorm:"default:1;index" json:"status"`
	CancelReason string      `gorm:"size:256" json:"cancel_reason"`
	OrderItem    []OrderItem `gorm:"foreignKey:OrderID" json:"order_items,omitempty"`
}
type OrderItem struct {
	Base
	OrderID       int64
	ProductID     int64
	Product       Product
	Quantity      int
	SnapshotPrice float64 `gorm:"type:decimal(10,2)"`
}
