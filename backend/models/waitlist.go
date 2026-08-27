package models

import "time"

type WaitlistStatus string

const (
	WaitlistStatusPendingPayment WaitlistStatus = "pending_payment"
	WaitlistStatusQueued         WaitlistStatus = "queued"
	WaitlistStatusFulfilled      WaitlistStatus = "fulfilled"
	WaitlistStatusCancelled      WaitlistStatus = "cancelled"
	WaitlistStatusExpired        WaitlistStatus = "expired"
)

// WaitlistEntry 是已付款候补单，不占公开库存，也不签发电子票。
// 退票释放的待派发库存按 id 升序派给 queued 队头。
type WaitlistEntry struct {
	Base
	WaitlistNo              string             `gorm:"size:32;uniqueIndex;not null" json:"waitlist_no"`
	UserID                  int64              `gorm:"not null;index;uniqueIndex:uk_waitlist_idempotency,priority:1" json:"user_id,string"`
	OrganizerID             int64              `gorm:"not null;index" json:"organizer_id,string"`
	EventID                 int64              `gorm:"not null;index" json:"event_id,string"`
	SessionID               int64              `gorm:"not null;index" json:"session_id,string"`
	TicketTierID            int64              `gorm:"not null;index" json:"ticket_tier_id,string"`
	Quantity                int                `gorm:"not null" json:"quantity"`
	AmountCents             int64              `gorm:"not null" json:"amount_cents"`
	Status                  WaitlistStatus     `gorm:"size:24;not null;index:idx_waitlist_tier_fifo,priority:2" json:"status"`
	ContactName             string             `gorm:"size:64;not null" json:"contact_name"`
	ContactPhone            string             `gorm:"size:20;not null" json:"contact_phone"`
	RealNameRequired        bool               `gorm:"not null;default:false" json:"real_name_required"`
	PurchaseNoticeVersion   string             `gorm:"size:32;not null" json:"purchase_notice_version"`
	EventTitleSnapshot      string             `gorm:"size:160;not null" json:"event_title_snapshot"`
	SessionStartsAtSnapshot time.Time          `gorm:"not null" json:"session_starts_at_snapshot"`
	VenueNameSnapshot       string             `gorm:"size:128;not null" json:"venue_name_snapshot"`
	VenueAddressSnapshot    string             `gorm:"size:256;not null" json:"venue_address_snapshot"`
	TierNameSnapshot        string             `gorm:"size:64;not null" json:"tier_name_snapshot"`
	IdempotencyKey          string             `gorm:"size:64;uniqueIndex:uk_waitlist_idempotency,priority:2" json:"-"`
	RequestID               string             `gorm:"size:64;index" json:"request_id"`
	FunnelVisitorKey        string             `gorm:"size:64;not null;default:''" json:"-"`
	ExpiresAt               time.Time          `gorm:"not null;index" json:"expires_at"`
	ExpiresAtUnix           int64              `gorm:"-" json:"expires_at_unix,omitempty"`
	PaidAt                  *time.Time         `json:"paid_at,omitempty"`
	FulfilledOrderID        *int64             `gorm:"index" json:"fulfilled_order_id,omitempty,string"`
	CancelledAt             *time.Time         `json:"cancelled_at,omitempty"`
	CancelReason            string             `gorm:"size:256" json:"cancel_reason"`
	QueuePosition           int                `gorm:"-" json:"queue_position,omitempty"`
	Attendees               []WaitlistAttendee `gorm:"foreignKey:WaitlistID" json:"attendees,omitempty"`
}

func (WaitlistEntry) TableName() string {
	return "waitlist_entry"
}

type WaitlistAttendee struct {
	Base
	WaitlistID     int64  `gorm:"not null;index;uniqueIndex:uk_waitlist_attendee,priority:1" json:"waitlist_id,string"`
	SequenceNo     int    `gorm:"not null;uniqueIndex:uk_waitlist_attendee,priority:2" json:"sequence_no"`
	Name           string `gorm:"size:64;not null" json:"name"`
	IDType         string `gorm:"size:16;not null" json:"id_type"`
	IDNumberMasked string `gorm:"size:32;not null" json:"id_number_masked"`
	IDNumberHash   string `gorm:"size:64;not null;index" json:"-"`
	IdentityKey    string `gorm:"size:64;not null;index;default:''" json:"-"`
}

func (WaitlistAttendee) TableName() string {
	return "waitlist_attendee"
}
