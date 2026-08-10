package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"gofun/container"
	"gofun/metrics"
	"gofun/models"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/snowflake"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrTicketCredentialInvalid = errors.New("票码无效")
	ErrTicketAccessDenied      = errors.New("无权核销该主办方的票")
)

type TicketCredentialSigner struct {
	// secrets[0] 为当前签发密钥；其余为轮换后仍可验签的旧密钥。
	secrets [][]byte
}

func NewTicketCredentialSigner(secrets ...[]byte) *TicketCredentialSigner {
	copied := make([][]byte, 0, len(secrets))
	for _, secret := range secrets {
		if len(secret) == 0 {
			continue
		}
		copied = append(copied, append([]byte(nil), secret...))
	}
	return &TicketCredentialSigner{secrets: copied}
}

func (s *TicketCredentialSigner) Sign(ticketID int64) string {
	if len(s.secrets) == 0 {
		return ""
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(ticketID, 10)))
	message := "FC1." + payload
	mac := hmac.New(sha256.New, s.secrets[0])
	_, _ = mac.Write([]byte(message))
	return message + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *TicketCredentialSigner) Parse(credential string) (int64, error) {
	parts := strings.Split(strings.TrimSpace(credential), ".")
	if len(parts) != 3 || parts[0] != "FC1" {
		return 0, ErrTicketCredentialInvalid
	}
	message := parts[0] + "." + parts[1]
	provided, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return 0, ErrTicketCredentialInvalid
	}
	matched := false
	for _, secret := range s.secrets {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write([]byte(message))
		if hmac.Equal(provided, mac.Sum(nil)) {
			matched = true
			break
		}
	}
	if !matched {
		return 0, ErrTicketCredentialInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, ErrTicketCredentialInvalid
	}
	ticketID, err := strconv.ParseInt(string(payload), 10, 64)
	if err != nil || ticketID <= 0 {
		return 0, ErrTicketCredentialInvalid
	}
	return ticketID, nil
}

type TicketVerificationResultView struct {
	Result     models.TicketVerificationResult `json:"result"`
	Message    string                          `json:"message"`
	VerifiedAt time.Time                       `json:"verified_at"`
	Ticket     *models.AdmissionTicket         `json:"ticket,omitempty"`
}

type TicketVerificationService struct {
	db     *gorm.DB
	node   *snowflake.Node
	signer *TicketCredentialSigner
}

func NewTicketVerificationService(c *container.Container) *TicketVerificationService {
	return &TicketVerificationService{
		db:     c.DB,
		node:   c.SnowflakeNode,
		signer: NewTicketCredentialSigner(c.TicketQRSecrets...),
	}
}

func (s *TicketVerificationService) Verify(
	ctx context.Context,
	organizerID, operatorUserID int64,
	credential string,
	sessionID int64,
) (*TicketVerificationResultView, error) {
	verificationResult := "error"
	defer func() {
		metrics.TicketVerificationsTotal.WithLabelValues(verificationResult).Inc()
	}()
	if err := s.requireOrganizerAccess(ctx, organizerID, operatorUserID); err != nil {
		if errors.Is(err, ErrTicketAccessDenied) {
			verificationResult = "access_denied"
		}
		return nil, err
	}
	fingerprint := credentialFingerprint(credential)
	ticketID, err := s.signer.Parse(credential)
	if err != nil {
		now := time.Now()
		if recordErr := s.createRecord(s.db.WithContext(ctx), nil, nil, organizerID, operatorUserID,
			fingerprint, models.TicketVerificationInvalidCredential, "票码格式或签名无效", now); recordErr != nil {
			return nil, recordErr
		}
		return &TicketVerificationResultView{
			Result: models.TicketVerificationInvalidCredential, Message: "票码无效", VerifiedAt: now,
		}, nil
	}

	var result *TicketVerificationResultView
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var ticket models.AdmissionTicket
		queryErr := tx.First(&ticket, ticketID).Error
		now := time.Now()
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			if err := s.createRecord(tx, nil, nil, organizerID, operatorUserID, fingerprint,
				models.TicketVerificationNotFound, "电子票不存在", now); err != nil {
				return err
			}
			result = &TicketVerificationResultView{
				Result: models.TicketVerificationNotFound, Message: "电子票不存在", VerifiedAt: now,
			}
			return nil
		}
		if queryErr != nil {
			return queryErr
		}
		var order models.TicketOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			First(&order, ticket.OrderID).Error; err != nil {
			return err
		}
		if order.Status != models.TicketOrderStatusPaid || order.PaymentStatus != models.PaymentStatusPaid {
			if err := s.createRecord(tx, &ticket.ID, nil, organizerID, operatorUserID, fingerprint,
				models.TicketVerificationRevoked, "ticket order is not available for verification", now); err != nil {
				return err
			}
			result = &TicketVerificationResultView{
				Result:     models.TicketVerificationRevoked,
				Message:    "ticket is not available for verification",
				VerifiedAt: now,
				Ticket:     &ticket,
			}
			return nil
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("OrderItem").First(&ticket, ticketID).Error; err != nil {
			return err
		}
		if ticket.OrganizerID != organizerID {
			if err := s.createRecord(tx, &ticket.ID, nil, organizerID, operatorUserID, fingerprint,
				models.TicketVerificationWrongOrganizer, "电子票不属于当前主办方", now); err != nil {
				return err
			}
			result = &TicketVerificationResultView{
				Result:     models.TicketVerificationWrongOrganizer,
				Message:    "该票不属于当前主办方",
				VerifiedAt: now,
			}
			return nil
		}
		if sessionID > 0 && ticket.SessionID != sessionID {
			if err := s.createRecord(tx, &ticket.ID, nil, organizerID, operatorUserID, fingerprint,
				models.TicketVerificationWrongOrganizer, "电子票不属于当前场次", now); err != nil {
				return err
			}
			result = &TicketVerificationResultView{
				Result:     models.TicketVerificationWrongOrganizer,
				Message:    "该票不属于当前核销场次",
				VerifiedAt: now,
				Ticket:     &ticket,
			}
			return nil
		}

		switch ticket.Status {
		case models.AdmissionTicketStatusUsed:
			if err := s.createRecord(tx, &ticket.ID, nil, organizerID, operatorUserID, fingerprint,
				models.TicketVerificationAlreadyUsed, "电子票已经核销", now); err != nil {
				return err
			}
			result = &TicketVerificationResultView{
				Result:     models.TicketVerificationAlreadyUsed,
				Message:    "该票已核销，不能重复入场",
				VerifiedAt: now,
				Ticket:     &ticket,
			}
			return nil
		case models.AdmissionTicketStatusRevoked:
			if err := s.createRecord(tx, &ticket.ID, nil, organizerID, operatorUserID, fingerprint,
				models.TicketVerificationRevoked, "电子票已经作废", now); err != nil {
				return err
			}
			result = &TicketVerificationResultView{
				Result:     models.TicketVerificationRevoked,
				Message:    "该票已退款或作废",
				VerifiedAt: now,
				Ticket:     &ticket,
			}
			return nil
		case models.AdmissionTicketStatusValid:
			update := tx.Model(&models.AdmissionTicket{}).
				Where("id = ? AND status = ?", ticket.ID, models.AdmissionTicketStatusValid).
				Updates(map[string]interface{}{
					"status":  models.AdmissionTicketStatusUsed,
					"used_at": &now,
				})
			if update.Error != nil {
				return update.Error
			}
			if update.RowsAffected != 1 {
				return fmt.Errorf("电子票状态并发更新失败")
			}
			ticket.Status = models.AdmissionTicketStatusUsed
			ticket.UsedAt = &now
			if err := s.createRecord(tx, &ticket.ID, &ticket.ID, organizerID, operatorUserID, fingerprint,
				models.TicketVerificationSuccess, "核销成功", now); err != nil {
				return err
			}
			result = &TicketVerificationResultView{
				Result:     models.TicketVerificationSuccess,
				Message:    "核销成功，可以入场",
				VerifiedAt: now,
				Ticket:     &ticket,
			}
			return nil
		default:
			return fmt.Errorf("未知电子票状态: %s", ticket.Status)
		}
	})
	if err == nil && result != nil {
		verificationResult = string(result.Result)
	}
	return result, err
}

func (s *TicketVerificationService) ListRecords(
	ctx context.Context,
	organizerID, operatorUserID int64,
	page, pageSize int,
) ([]models.TicketVerificationRecord, int64, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, operatorUserID); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	query := s.db.WithContext(ctx).Model(&models.TicketVerificationRecord{}).
		Where("organizer_id = ?", organizerID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var records []models.TicketVerificationRecord
	err := query.Preload("Ticket.OrderItem").
		Order("verified_at DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&records).Error
	return records, total, err
}

func (s *TicketVerificationService) requireOrganizerAccess(
	ctx context.Context,
	organizerID, userID int64,
) error {
	var count int64
	err := s.db.WithContext(ctx).Model(&models.OrganizerMember{}).
		Where(
			"organizer_id = ? AND user_id = ? AND status = ?",
			organizerID,
			userID,
			models.OrganizerStatusActive,
		).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count == 0 {
		return ErrTicketAccessDenied
	}
	var organizer models.Organizer
	if err := s.db.WithContext(ctx).First(&organizer, organizerID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrTicketAccessDenied
		}
		return err
	}
	if organizer.Status != models.OrganizerStatusActive {
		return ErrTicketAccessDenied
	}
	return nil
}

func (s *TicketVerificationService) createRecord(
	db *gorm.DB,
	ticketID, successTicketID *int64,
	organizerID, operatorUserID int64,
	fingerprint string,
	result models.TicketVerificationResult,
	detail string,
	verifiedAt time.Time,
) error {
	return db.Create(&models.TicketVerificationRecord{
		Base:                  models.Base{ID: s.node.Generate().Int64()},
		TicketID:              ticketID,
		SuccessTicketID:       successTicketID,
		OrganizerID:           organizerID,
		OperatorUserID:        operatorUserID,
		CredentialFingerprint: fingerprint,
		Result:                result,
		Detail:                detail,
		VerifiedAt:            verifiedAt,
	}).Error
}

func credentialFingerprint(credential string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(credential)))
	return hex.EncodeToString(sum[:])
}
