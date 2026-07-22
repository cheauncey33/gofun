package models

import "time"

type TicketOrderSource string

const (
	TicketOrderSourceNormal   TicketOrderSource = "normal"
	TicketOrderSourceRushSale TicketOrderSource = "rush_sale"
)

type TicketOrderStatus string

const (
	TicketOrderStatusQueued         TicketOrderStatus = "queued"
	TicketOrderStatusPendingPayment TicketOrderStatus = "pending_payment"
	TicketOrderStatusPaid           TicketOrderStatus = "paid"
	TicketOrderStatusCancelled      TicketOrderStatus = "cancelled"
	TicketOrderStatusFailed         TicketOrderStatus = "failed"
)

type PaymentStatus string

const (
	PaymentStatusUnpaid    PaymentStatus = "unpaid"
	PaymentStatusPaid      PaymentStatus = "paid"
	PaymentStatusRefunding PaymentStatus = "refunding"
	PaymentStatusRefunded  PaymentStatus = "refunded"
)

var ticketOrderTransitions = map[TicketOrderStatus][]TicketOrderStatus{
	TicketOrderStatusQueued:         {TicketOrderStatusPendingPayment, TicketOrderStatusFailed},
	TicketOrderStatusPendingPayment: {TicketOrderStatusPaid, TicketOrderStatusCancelled},
	TicketOrderStatusPaid:           {TicketOrderStatusCancelled},
}

func (s TicketOrderStatus) CanTransitionTo(target TicketOrderStatus) bool {
	for _, allowed := range ticketOrderTransitions[s] {
		if target == allowed {
			return true
		}
	}
	return false
}

func (s TicketOrderStatus) HasBeenPaid() bool {
	return s == TicketOrderStatusPaid
}

type TicketOrder struct {
	Base
	OrderNo               string                `gorm:"size:32;uniqueIndex;not null" json:"order_no"`
	UserID                int64                 `gorm:"not null;index;uniqueIndex:uk_ticket_order_idempotency,priority:1" json:"user_id,string"`
	OrganizerID           int64                 `gorm:"not null;index" json:"organizer_id,string"`
	EventID               int64                 `gorm:"not null;index" json:"event_id,string"`
	SessionID             int64                 `gorm:"not null;index" json:"session_id,string"`
	RushSaleCampaignID    *int64                `gorm:"index" json:"rush_sale_campaign_id,omitempty,string"`
	OrderSource           TicketOrderSource     `gorm:"size:16;not null;index" json:"order_source"`
	Status                TicketOrderStatus     `gorm:"size:24;not null;index" json:"status"`
	PaymentStatus         PaymentStatus         `gorm:"size:16;not null;index" json:"payment_status"`
	TotalAmountCents      int64                 `gorm:"not null" json:"total_amount_cents"`
	ContactName           string                `gorm:"size:64;not null" json:"contact_name"`
	ContactPhone          string                `gorm:"size:20;not null" json:"contact_phone"`
	RealNameRequired      bool                  `gorm:"not null;default:false" json:"real_name_required"`
	PurchaseNoticeVersion string                `gorm:"size:32;not null" json:"purchase_notice_version"`
	TermsAcceptedAt       *time.Time            `json:"terms_accepted_at,omitempty"`
	IdempotencyKey        string                `gorm:"size:64;uniqueIndex:uk_ticket_order_idempotency,priority:2" json:"-"`
	RequestID             string                `gorm:"size:64;index" json:"request_id"`
	ExpiresAt             time.Time             `gorm:"not null;index" json:"expires_at"`
	PaidAt                *time.Time            `json:"paid_at,omitempty"`
	CancelledAt           *time.Time            `json:"cancelled_at,omitempty"`
	CancelReason          string                `gorm:"size:256" json:"cancel_reason"`
	Items                 []TicketOrderItem     `gorm:"foreignKey:OrderID" json:"items,omitempty"`
	Attendees             []TicketOrderAttendee `gorm:"foreignKey:OrderID" json:"attendees,omitempty"`
	Tickets               []AdmissionTicket     `gorm:"foreignKey:OrderID" json:"tickets,omitempty"`
}

// IdempotencyKey 只在同一用户内唯一，允许不同用户使用相同的客户端请求键。
func (TicketOrder) TableName() string {
	return "ticket_order"
}

type TicketOrderItem struct {
	Base
	OrderID                 int64     `gorm:"not null;index" json:"order_id,string"`
	TicketTierID            int64     `gorm:"not null;index" json:"ticket_tier_id,string"`
	Quantity                int       `gorm:"not null" json:"quantity"`
	UnitPriceCents          int64     `gorm:"not null" json:"unit_price_cents"`
	EventTitleSnapshot      string    `gorm:"size:160;not null" json:"event_title_snapshot"`
	SessionStartsAtSnapshot time.Time `gorm:"not null" json:"session_starts_at_snapshot"`
	VenueNameSnapshot       string    `gorm:"size:128;not null" json:"venue_name_snapshot"`
	VenueAddressSnapshot    string    `gorm:"size:256;not null" json:"venue_address_snapshot"`
	TierNameSnapshot        string    `gorm:"size:64;not null" json:"tier_name_snapshot"`
}

func (TicketOrderItem) TableName() string {
	return "ticket_order_item"
}

// TicketOrderAttendee 是下单时的实名观演人快照。
// 证件号只保留展示掩码和带密钥摘要，订单查询接口不会返回可逆明文。
type TicketOrderAttendee struct {
	Base
	OrderID        int64  `gorm:"not null;index;uniqueIndex:uk_ticket_order_attendee,priority:1" json:"order_id,string"`
	SequenceNo     int    `gorm:"not null;uniqueIndex:uk_ticket_order_attendee,priority:2" json:"sequence_no"`
	Name           string `gorm:"size:64;not null" json:"name"`
	IDType         string `gorm:"size:16;not null" json:"id_type"`
	IDNumberMasked string `gorm:"size:32;not null" json:"id_number_masked"`
	IDNumberHash   string `gorm:"size:64;not null;index" json:"-"`
}

func (TicketOrderAttendee) TableName() string {
	return "ticket_order_attendee"
}

type TicketOrderOutboxStatus string

const (
	TicketOrderOutboxPending   TicketOrderOutboxStatus = "pending"
	TicketOrderOutboxPublished TicketOrderOutboxStatus = "published"
	TicketOrderOutboxFailed    TicketOrderOutboxStatus = "failed"
)

// TicketOrderOutbox 将订单消息先落库，再由后台转发到 MQ，避免创建成功却投递失败。
type TicketOrderOutbox struct {
	ID          int64                   `gorm:"primaryKey" json:"id,string"`
	OrderID     int64                   `gorm:"not null;index:idx_outbox_order" json:"order_id,string"`
	Payload     string                  `gorm:"type:json;not null" json:"payload"`
	Status      TicketOrderOutboxStatus `gorm:"size:16;not null;default:pending;index:idx_outbox_status_create,priority:1" json:"status"`
	Attempts    int                     `gorm:"not null;default:0" json:"attempts"`
	LastError   string                  `gorm:"size:512;not null;default:''" json:"last_error"`
	CreateTime  time.Time               `gorm:"not null;autoCreateTime;index:idx_outbox_status_create,priority:2" json:"create_time"`
	PublishedAt *time.Time              `json:"published_at,omitempty"`
}

func (TicketOrderOutbox) TableName() string {
	return "ticket_order_outbox"
}

type RushSaleStatus string

const (
	RushSaleStatusDraft     RushSaleStatus = "draft"
	RushSaleStatusScheduled RushSaleStatus = "scheduled"
	RushSaleStatusActive    RushSaleStatus = "active"
	RushSaleStatusEnded     RushSaleStatus = "ended"
	RushSaleStatusCancelled RushSaleStatus = "cancelled"
)

type RushSaleCampaign struct {
	Base
	OrganizerID    int64          `gorm:"not null;index" json:"organizer_id,string"`
	TicketTierID   int64          `gorm:"not null;index" json:"ticket_tier_id,string"`
	Name           string         `gorm:"size:128;not null" json:"name"`
	RushPriceCents int64          `gorm:"not null" json:"rush_price_cents"`
	TotalQuota     int            `gorm:"not null" json:"total_quota"`
	RemainingQuota int            `gorm:"not null" json:"remaining_quota"`
	PerUserLimit   int            `gorm:"not null;default:1" json:"per_user_limit"`
	StartsAt       time.Time      `gorm:"not null;index" json:"starts_at"`
	EndsAt         time.Time      `gorm:"not null;index" json:"ends_at"`
	Status         RushSaleStatus `gorm:"size:16;not null;default:'draft';index" json:"status"`
}
