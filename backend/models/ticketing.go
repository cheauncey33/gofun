package models

import (
	"fmt"
	"strings"
	"time"
)

// TicketingStatus 使用字符串而不是无语义的数字，便于管理端、日志和数据库排查。
// 这些状态只覆盖第一阶段，不提前加入核销、结算等尚未实现的流程。
type OrganizerStatus string

const (
	OrganizerStatusDisabled OrganizerStatus = "disabled"
	OrganizerStatusActive   OrganizerStatus = "active"
)

type AuditStatus string

const (
	AuditStatusPending  AuditStatus = "pending"
	AuditStatusApproved AuditStatus = "approved"
	AuditStatusRejected AuditStatus = "rejected"
)

type OrganizerRole string

const (
	OrganizerRoleOwner    OrganizerRole = "owner"
	OrganizerRoleOperator OrganizerRole = "operator"
)

type EventSaleMode string

const (
	EventSaleModeCounter EventSaleMode = "counter"
	EventSaleModeSeated  EventSaleMode = "seated"
)

func ParseEventSaleMode(raw string) (EventSaleMode, error) {
	switch strings.TrimSpace(raw) {
	case "", string(EventSaleModeCounter):
		return EventSaleModeCounter, nil
	case string(EventSaleModeSeated):
		return EventSaleModeSeated, nil
	default:
		return "", fmt.Errorf("无效卖法")
	}
}

func (m EventSaleMode) IsSeated() bool {
	return m == EventSaleModeSeated
}

type EventStatus string

const (
	EventStatusDraft     EventStatus = "draft"
	EventStatusPublished EventStatus = "published"
	EventStatusCancelled EventStatus = "cancelled"
	EventStatusFinished  EventStatus = "finished"
)

type SessionStatus string

const (
	SessionStatusDraft     SessionStatus = "draft"
	SessionStatusOnSale    SessionStatus = "on_sale"
	SessionStatusSoldOut   SessionStatus = "sold_out"
	SessionStatusCancelled SessionStatus = "cancelled"
	SessionStatusFinished  SessionStatus = "finished"
)

type TicketTierStatus string

const (
	TicketTierStatusDisabled TicketTierStatus = "disabled"
	TicketTierStatusOnSale   TicketTierStatus = "on_sale"
	TicketTierStatusSoldOut  TicketTierStatus = "sold_out"
)

type Organizer struct {
	Base
	Name         string          `gorm:"size:128;not null" json:"name"`
	Slug         string          `gorm:"size:64;uniqueIndex;not null" json:"slug"`
	LogoURL      string          `gorm:"size:512" json:"logo_url"`
	Description  string          `gorm:"type:text" json:"description"`
	ContactName  string          `gorm:"size:64" json:"contact_name"`
	ContactPhone string          `gorm:"size:32" json:"contact_phone"`
	Status       OrganizerStatus `gorm:"size:16;not null;default:'active';index" json:"status"`
	AuditStatus  AuditStatus     `gorm:"size:16;not null;default:'pending';index" json:"audit_status"`
}

type OrganizerMember struct {
	Base
	OrganizerID int64           `gorm:"not null;uniqueIndex:uk_organizer_user;index" json:"organizer_id,string"`
	UserID      int64           `gorm:"not null;uniqueIndex:uk_organizer_user;index" json:"user_id,string"`
	Role        OrganizerRole   `gorm:"size:16;not null" json:"role"`
	Status      OrganizerStatus `gorm:"size:16;not null;default:'active';index" json:"status"`
	Organizer   Organizer       `gorm:"foreignKey:OrganizerID" json:"organizer,omitempty"`
	User        User            `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

type Venue struct {
	Base
	OrganizerID int64           `gorm:"not null;index" json:"organizer_id,string"`
	Name        string          `gorm:"size:128;not null;index" json:"name"`
	City        string          `gorm:"size:64;not null;index" json:"city"`
	District    string          `gorm:"size:64" json:"district"`
	Address     string          `gorm:"size:256;not null" json:"address"`
	Latitude    *float64        `gorm:"type:decimal(10,7)" json:"latitude,omitempty"`
	Longitude   *float64        `gorm:"type:decimal(10,7)" json:"longitude,omitempty"`
	Timezone    string          `gorm:"size:64;not null;default:'Asia/Shanghai'" json:"timezone"`
	Status      OrganizerStatus `gorm:"size:16;not null;default:'active';index" json:"status"`
}

type Event struct {
	Base
	OrganizerID        int64          `gorm:"not null;index" json:"organizer_id,string"`
	Organizer          Organizer      `gorm:"foreignKey:OrganizerID" json:"organizer,omitempty"`
	Title              string         `gorm:"size:160;not null;index" json:"title"`
	Subtitle           string         `gorm:"size:256" json:"subtitle"`
	Category           string         `gorm:"size:64;not null;index" json:"category"`
	CoverURL           string         `gorm:"size:512" json:"cover_url"`
	Description        string         `gorm:"type:text" json:"description"`
	Status             EventStatus    `gorm:"size:24;not null;default:'draft';index" json:"status"`
	RealNameRequired   bool           `gorm:"not null;default:false" json:"real_name_required"`
	MaxTicketsPerOrder int            `gorm:"not null;default:6" json:"max_tickets_per_order"`
	SaleMode           EventSaleMode  `gorm:"size:16;not null;default:'counter';index" json:"sale_mode"`
	PublishedAt        *time.Time     `gorm:"index" json:"published_at,omitempty"`
	Sessions           []EventSession `gorm:"foreignKey:EventID" json:"sessions,omitempty"`
}

type EventSession struct {
	Base
	EventID      int64         `gorm:"not null;index" json:"event_id,string"`
	VenueID      int64         `gorm:"not null;index" json:"venue_id,string"`
	Venue        Venue         `gorm:"foreignKey:VenueID" json:"venue,omitempty"`
	StartsAt     time.Time     `gorm:"not null;index" json:"starts_at"`
	EndsAt       time.Time     `gorm:"not null" json:"ends_at"`
	SaleStartsAt time.Time     `gorm:"not null;index" json:"sale_starts_at"`
	SaleEndsAt   time.Time     `gorm:"not null;index" json:"sale_ends_at"`
	Status       SessionStatus `gorm:"size:24;not null;default:'draft';index" json:"status"`
	Version      int64         `gorm:"not null;default:0" json:"version"`
	TicketTiers  []TicketTier  `gorm:"foreignKey:SessionID" json:"ticket_tiers,omitempty"`
}

type TicketTier struct {
	Base
	SessionID          int64            `gorm:"not null;index" json:"session_id,string"`
	Name               string           `gorm:"size:64;not null" json:"name"`
	Description        string           `gorm:"size:256" json:"description"`
	PriceCents         int64            `gorm:"not null" json:"price_cents"`
	OriginalPriceCents *int64           `json:"original_price_cents,omitempty"`
	TotalQuota         int              `gorm:"not null" json:"total_quota"`
	RemainingQuota     int              `gorm:"not null;index" json:"remaining_quota"`
	SoldCount          int64            `gorm:"not null;default:0" json:"sold_count"`
	PurchaseLimit      int              `gorm:"not null;default:6" json:"purchase_limit"`
	AssignPlaceNo      bool             `gorm:"not null;default:true" json:"assign_place_no"`
	PlaceSeq           int              `gorm:"not null;default:0" json:"-"`
	Status             TicketTierStatus `gorm:"size:16;not null;default:'disabled';index" json:"status"`
	Version            int64            `gorm:"not null;default:0" json:"version"`
}

// TicketTierBucket 将票档库存拆成多行，打散消费热路径行锁。
type TicketTierBucket struct {
	TierID         int64     `gorm:"primaryKey;not null" json:"tier_id,string"`
	BucketNo       int       `gorm:"primaryKey;not null" json:"bucket_no"`
	RemainingQuota int       `gorm:"not null" json:"remaining_quota"`
	SoldCount      int64     `gorm:"not null;default:0" json:"sold_count"`
	Version        int64     `gorm:"not null;default:0" json:"version"`
	UpdateTime     time.Time `gorm:"autoUpdateTime" json:"update_time"`
	CreateTime     time.Time `gorm:"autoCreateTime" json:"create_time"`
}

func (TicketTierBucket) TableName() string {
	return "ticket_tier_bucket"
}

// RushCampaignBucket 限时开售活动库存分桶。
type RushCampaignBucket struct {
	CampaignID     int64     `gorm:"primaryKey;not null" json:"campaign_id,string"`
	BucketNo       int       `gorm:"primaryKey;not null" json:"bucket_no"`
	RemainingQuota int       `gorm:"not null" json:"remaining_quota"`
	Version        int64     `gorm:"not null;default:0" json:"version"`
	UpdateTime     time.Time `gorm:"autoUpdateTime" json:"update_time"`
	CreateTime     time.Time `gorm:"autoCreateTime" json:"create_time"`
}

func (RushCampaignBucket) TableName() string {
	return "rush_campaign_bucket"
}
