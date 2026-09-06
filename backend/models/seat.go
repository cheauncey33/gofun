package models

import "time"

const (
	MaxSeatLayoutRows = 16
	MaxSeatLayoutCols = 24
)

type SeatLayoutStatus string

const (
	SeatLayoutDraft     SeatLayoutStatus = "draft"
	SeatLayoutPublished SeatLayoutStatus = "published"
)

type SessionSeatStatus string

const (
	SessionSeatAvailable SessionSeatStatus = "available"
	SessionSeatHeld      SessionSeatStatus = "held"
	SessionSeatSold      SessionSeatStatus = "sold"
	// SessionSeatOffSale 仅用于座位列表展示：票档已停售，库存行仍是 available。
	SessionSeatOffSale SessionSeatStatus = "off_sale"
)

func (s SessionSeatStatus) IsOccupied() bool {
	return s == SessionSeatHeld || s == SessionSeatSold
}

func SeatLayoutBoundsOK(rowCount, colCount int) bool {
	return rowCount >= 1 && rowCount <= MaxSeatLayoutRows &&
		colCount >= 1 && colCount <= MaxSeatLayoutCols
}

// SeatLayout 是物理厅的版本化座位图。EventID 仅用于兼容改造前的数据。
type SeatLayout struct {
	Base
	EventID     *int64           `gorm:"uniqueIndex" json:"event_id,omitempty,string"`
	HallID      *int64           `gorm:"index;uniqueIndex:uk_hall_layout_version,priority:1" json:"hall_id,omitempty,string"`
	Version     int              `gorm:"not null;default:1;uniqueIndex:uk_hall_layout_version,priority:2" json:"version"`
	Status      SeatLayoutStatus `gorm:"size:16;not null;default:'draft';index" json:"status"`
	Name        string           `gorm:"size:64;not null;default:''" json:"name"`
	RowCount    int              `gorm:"not null" json:"row_count"`
	ColCount    int              `gorm:"not null" json:"col_count"`
	PublishedAt *time.Time       `json:"published_at,omitempty"`
	Seats       []Seat           `gorm:"foreignKey:LayoutID" json:"seats,omitempty"`
}

func (SeatLayout) TableName() string {
	return "seat_layout"
}

// Seat 是厅图上的可售格。未画出的格子不建行。
type Seat struct {
	Base
	LayoutID int64 `gorm:"not null;uniqueIndex:uk_seat_layout_cell,priority:1" json:"layout_id,string"`
	// TicketTierID 仅兼容旧活动厅图；新厅图在 SessionSeat 上配置本场票档。
	TicketTierID *int64 `gorm:"index" json:"ticket_tier_id,omitempty,string"`
	ZoneKey      string `gorm:"size:64;not null;default:'general';index" json:"zone_key"`
	RowNo        int    `gorm:"not null;uniqueIndex:uk_seat_layout_cell,priority:2" json:"row_no"`
	ColNo        int    `gorm:"not null;uniqueIndex:uk_seat_layout_cell,priority:3" json:"col_no"`
	Label        string `gorm:"size:32;not null" json:"label"`
}

func (Seat) TableName() string {
	return "seat"
}

// SessionSeat 是场次库存真相：一座一行。剩余张数只是展示字段。
type SessionSeat struct {
	Base
	SessionID    int64             `gorm:"not null;uniqueIndex:uk_session_seat,priority:1" json:"session_id,string"`
	SeatID       int64             `gorm:"not null;uniqueIndex:uk_session_seat,priority:2" json:"seat_id,string"`
	TicketTierID int64             `gorm:"not null;index" json:"ticket_tier_id,string"`
	OrderID      *int64            `gorm:"index" json:"order_id,omitempty,string"`
	Status       SessionSeatStatus `gorm:"size:16;not null;default:'available';index" json:"status"`
	Seat         Seat              `gorm:"foreignKey:SeatID" json:"seat,omitempty"`
}

func (SessionSeat) TableName() string {
	return "session_seat"
}
