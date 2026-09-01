package models

import "time"

type TicketOrderSource string

const (
	TicketOrderSourceNormal   TicketOrderSource = "normal"
	TicketOrderSourceRushSale TicketOrderSource = "rush_sale"
	TicketOrderSourceWaitlist TicketOrderSource = "waitlist"
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
	PaymentStatusFailed    PaymentStatus = "failed"
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
	RequestHash           string                `gorm:"size:64;not null;default:''" json:"-"`
	RequestID             string                `gorm:"size:64;index" json:"request_id"`
	FunnelVisitorKey      string                `gorm:"size:64;not null;default:''" json:"-"`
	StockBucketNo         *int                  `json:"stock_bucket_no,omitempty"`
	RushBucketNo          *int                  `json:"rush_bucket_no,omitempty"`
	ExpiresAt             time.Time             `gorm:"not null;index" json:"expires_at"`
	ExpiresAtUnix         int64                 `gorm:"-" json:"expires_at_unix,omitempty"`
	PaidAt                *time.Time            `json:"paid_at,omitempty"`
	CancelledAt           *time.Time            `json:"cancelled_at,omitempty"`
	CancelReason          string                `gorm:"size:256" json:"cancel_reason"`
	Items                 []TicketOrderItem     `gorm:"foreignKey:OrderID" json:"items,omitempty"`
	Attendees             []TicketOrderAttendee `gorm:"foreignKey:OrderID" json:"attendees,omitempty"`
	Tickets               []AdmissionTicket     `gorm:"foreignKey:OrderID" json:"tickets,omitempty"`
	Payments              []PaymentTransaction  `gorm:"foreignKey:OrderID" json:"payments,omitempty"`
	SessionSeats          []SessionSeat         `gorm:"foreignKey:OrderID" json:"session_seats,omitempty"`
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

type PaymentTransactionStatus string

const (
	PaymentTransactionPending  PaymentTransactionStatus = "pending"
	PaymentTransactionSuccess  PaymentTransactionStatus = "success"
	PaymentTransactionFailed   PaymentTransactionStatus = "failed"
	PaymentTransactionClosed   PaymentTransactionStatus = "closed"
	PaymentTransactionRefunded PaymentTransactionStatus = "refunded"
)

// PaymentTransaction stores the platform-side payment order. It never stores
// a user's bank or wallet balance; the provider owns that money.
type PaymentTransaction struct {
	Base
	PaymentNo         string                   `gorm:"size:64;uniqueIndex;not null" json:"payment_no"`
	OrderID           int64                    `gorm:"not null;index;default:0" json:"order_id,string"`
	WaitlistID        int64                    `gorm:"not null;index;default:0" json:"waitlist_id,string"`
	UserID            int64                    `gorm:"not null;index" json:"user_id,string"`
	Provider          string                   `gorm:"size:32;not null" json:"provider"`
	ProviderPaymentID string                   `gorm:"size:128;not null" json:"provider_payment_id"`
	AmountCents       int64                    `gorm:"not null" json:"amount_cents"`
	Status            PaymentTransactionStatus `gorm:"size:16;not null;index" json:"status"`
	Scenario          string                   `gorm:"size:16;not null;default:'success'" json:"scenario"`
	FailureReason     string                   `gorm:"size:256;not null;default:''" json:"failure_reason"`
	ExpiresAt         time.Time                `gorm:"not null;index" json:"expires_at"`
	PaidAt            *time.Time               `json:"paid_at,omitempty"`
	RefundedAt        *time.Time               `json:"refunded_at,omitempty"`
}

func (PaymentTransaction) TableName() string {
	return "payment_transaction"
}

// PaymentCallback makes provider webhooks auditable and idempotent.
type PaymentCallback struct {
	Base
	ProviderEventID string     `gorm:"size:128;uniqueIndex;not null" json:"provider_event_id"`
	PaymentNo       string     `gorm:"size:64;index;not null" json:"payment_no"`
	Provider        string     `gorm:"size:32;not null" json:"provider"`
	Status          string     `gorm:"size:16;not null" json:"status"`
	AmountCents     int64      `gorm:"not null" json:"amount_cents"`
	Payload         string     `gorm:"type:json;not null" json:"-"`
	ProcessedAt     *time.Time `json:"processed_at,omitempty"`
}

func (PaymentCallback) TableName() string {
	return "payment_callback"
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
	IdentityKey    string `gorm:"size:64;not null;index;default:''" json:"-"`
}

func (TicketOrderAttendee) TableName() string {
	return "ticket_order_attendee"
}

type TicketOrderOutboxStatus string

const (
	TicketOrderOutboxPending    TicketOrderOutboxStatus = "pending"
	TicketOrderOutboxPublishing TicketOrderOutboxStatus = "publishing"
	TicketOrderOutboxPublished  TicketOrderOutboxStatus = "published"
	TicketOrderOutboxFailed     TicketOrderOutboxStatus = "failed"
)

// TicketOrderOutbox 将订单消息先落库，再由后台转发到 MQ，避免创建成功却投递失败。
type TicketOrderOutbox struct {
	ID          int64                   `gorm:"primaryKey" json:"id,string"`
	OrderID     int64                   `gorm:"not null;index:idx_outbox_order" json:"order_id,string"`
	EventType   string                  `gorm:"size:64;not null" json:"event_type"`
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

// TicketOrderConsumerInbox makes broker redeliveries idempotent by event ID.
// The composite primary key permits independent consumers of the same event.
type TicketOrderConsumerInbox struct {
	ConsumerName string    `gorm:"primaryKey;size:64" json:"consumer_name"`
	EventID      int64     `gorm:"primaryKey" json:"event_id,string"`
	OrderID      int64     `gorm:"not null;index" json:"order_id,string"`
	CreateTime   time.Time `gorm:"not null;autoCreateTime" json:"create_time"`
}

func (TicketOrderConsumerInbox) TableName() string {
	return "ticket_order_consumer_inbox"
}

type TicketStockRecoveryFenceOwner string

const (
	TicketStockRecoveryFenceOrder    TicketStockRecoveryFenceOwner = "order"
	TicketStockRecoveryFenceRecovery TicketStockRecoveryFenceOwner = "recovery"
)

// TicketStockRecoveryFence 让订单事务与库存恢复任务竞争同一个 order_id。
// order 表示订单和 Outbox 已在同一事务提交；recovery 表示恢复任务已取得 Redis 回滚权。
type TicketStockRecoveryFence struct {
	OrderID    int64                         `gorm:"primaryKey" json:"order_id,string"`
	Owner      TicketStockRecoveryFenceOwner `gorm:"size:16;not null" json:"owner"`
	CreateTime time.Time                     `gorm:"not null;autoCreateTime" json:"create_time"`
}

func (TicketStockRecoveryFence) TableName() string {
	return "ticket_stock_recovery_fence"
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
