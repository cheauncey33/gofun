package models

import "time"

type AdmissionTicketStatus string

const (
	AdmissionTicketStatusValid   AdmissionTicketStatus = "valid"
	AdmissionTicketStatusUsed    AdmissionTicketStatus = "used"
	AdmissionTicketStatusRevoked AdmissionTicketStatus = "revoked"
)

type AdmissionTicket struct {
	Base
	TicketNo     string                `gorm:"size:32;uniqueIndex;not null" json:"ticket_no"`
	OrderID      int64                 `gorm:"not null;index" json:"order_id,string"`
	OrderItemID  int64                 `gorm:"not null;index;uniqueIndex:uk_ticket_unit,priority:1" json:"order_item_id,string"`
	SequenceNo   int                   `gorm:"not null;uniqueIndex:uk_ticket_unit,priority:2" json:"sequence_no"`
	UserID       int64                 `gorm:"not null;index" json:"user_id,string"`
	OrganizerID  int64                 `gorm:"not null;index" json:"organizer_id,string"`
	EventID      int64                 `gorm:"not null;index" json:"event_id,string"`
	SessionID    int64                 `gorm:"not null;index" json:"session_id,string"`
	TicketTierID int64                 `gorm:"not null;index" json:"ticket_tier_id,string"`
	PlaceLabel   string                `gorm:"size:64;not null;default:''" json:"place_label"`
	Status       AdmissionTicketStatus `gorm:"size:16;not null;index" json:"status"`
	IssuedAt     time.Time             `gorm:"not null;index" json:"issued_at"`
	UsedAt       *time.Time            `json:"used_at,omitempty"`
	RevokedAt    *time.Time            `json:"revoked_at,omitempty"`
	RevokeReason string                `gorm:"size:256" json:"revoke_reason"`
	Credential   string                `gorm:"-" json:"credential,omitempty"`
	OrderItem    TicketOrderItem       `gorm:"foreignKey:OrderItemID" json:"order_item,omitempty"`
}

func (AdmissionTicket) TableName() string {
	return "admission_ticket"
}

type TicketVerificationResult string

const (
	TicketVerificationSuccess           TicketVerificationResult = "success"
	TicketVerificationAlreadyUsed       TicketVerificationResult = "already_used"
	TicketVerificationRevoked           TicketVerificationResult = "revoked"
	TicketVerificationInvalidCredential TicketVerificationResult = "invalid_credential"
	TicketVerificationWrongOrganizer    TicketVerificationResult = "wrong_organizer"
	TicketVerificationNotFound          TicketVerificationResult = "not_found"
)

// TicketVerificationRecord 记录每一次核销尝试，而不只记录成功结果。
// SuccessTicketID 只在成功时写入；唯一索引是“同一张票最多成功一次”的数据库兜底。
type TicketVerificationRecord struct {
	Base
	TicketID              *int64                   `gorm:"index" json:"ticket_id,omitempty,string"`
	SuccessTicketID       *int64                   `gorm:"uniqueIndex" json:"success_ticket_id,omitempty,string"`
	OrganizerID           int64                    `gorm:"not null;index" json:"organizer_id,string"`
	OperatorUserID        int64                    `gorm:"not null;index" json:"operator_user_id,string"`
	CredentialFingerprint string                   `gorm:"size:64;not null;index" json:"-"`
	Result                TicketVerificationResult `gorm:"size:32;not null;index" json:"result"`
	Detail                string                   `gorm:"size:256" json:"detail"`
	VerifiedAt            time.Time                `gorm:"not null;index" json:"verified_at"`
	Ticket                *AdmissionTicket         `gorm:"foreignKey:TicketID" json:"ticket,omitempty"`
}

func (TicketVerificationRecord) TableName() string {
	return "ticket_verification_record"
}
