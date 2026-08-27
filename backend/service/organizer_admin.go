package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"gofun/metrics"
	"gofun/models"

	"gorm.io/gorm"
)

type ApplyOrganizerInput struct {
	Name         string `json:"name"`
	Slug         string `json:"slug"`
	Description  string `json:"description"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
}

type ReviewDecisionInput struct {
	Note string `json:"note"`
}

type AdminOrganizerRow struct {
	Organizer     models.Organizer `json:"organizer"`
	OwnerUsername string           `json:"owner_username"`
}

type AdminEventReviewRow struct {
	Event         models.Event `json:"event"`
	OrganizerName string       `json:"organizer_name"`
	OrganizerID   int64        `json:"organizer_id,string"`
}

type AdminPlatformOverview struct {
	PendingOrganizers int64                    `json:"pending_organizers"`
	PendingEvents     int64                    `json:"pending_events"`
	ActiveOrganizers  int64                    `json:"active_organizers"`
	PublishedEvents   int64                    `json:"published_events"`
	Runtime           metrics.PlatformSnapshot `json:"runtime"`
}

func (s *TicketCatalogService) ApplyOrganizer(
	ctx context.Context,
	userID int64,
	input ApplyOrganizerInput,
) (*models.Organizer, error) {
	if userID <= 0 {
		return nil, ErrOrganizerForbidden
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Description = strings.TrimSpace(input.Description)
	input.ContactName = strings.TrimSpace(input.ContactName)
	input.ContactPhone = strings.TrimSpace(input.ContactPhone)
	if input.Name == "" || !organizerSlugPattern.MatchString(input.Slug) {
		return nil, fmt.Errorf("%w: 请填写名称，标识仅小写字母、数字和连字符", ErrInvalidTicketCatalog)
	}

	memberships, err := s.repo.ListActiveMembershipsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	var rejected *models.Organizer
	for _, membership := range memberships {
		org := membership.Organizer
		switch org.AuditStatus {
		case models.AuditStatusApproved:
			if org.Status == models.OrganizerStatusActive {
				return nil, fmt.Errorf("%w: 当前账号已经是主办方", ErrInvalidTicketCatalog)
			}
		case models.AuditStatusPending:
			return nil, fmt.Errorf("%w: 已有待审核的主办方申请", ErrInvalidTicketCatalog)
		case models.AuditStatusRejected:
			if membership.Role == models.OrganizerRoleOwner {
				copy := org
				rejected = &copy
			}
		}
	}

	if rejected != nil {
		rejected.Name = input.Name
		rejected.Slug = input.Slug
		rejected.Description = input.Description
		rejected.ContactName = input.ContactName
		rejected.ContactPhone = input.ContactPhone
		rejected.Status = models.OrganizerStatusDisabled
		rejected.AuditStatus = models.AuditStatusPending
		rejected.AuditNote = ""
		if err := s.db.WithContext(ctx).Save(rejected).Error; err != nil {
			return nil, wrapOrganizerWriteError(err)
		}
		return rejected, nil
	}

	organizer := &models.Organizer{
		Name:         input.Name,
		Slug:         input.Slug,
		Description:  input.Description,
		ContactName:  input.ContactName,
		ContactPhone: input.ContactPhone,
		Status:       models.OrganizerStatusDisabled,
		AuditStatus:  models.AuditStatusPending,
	}
	if err := s.repo.CreateOrganizerWithOwner(ctx, organizer, userID); err != nil {
		return nil, wrapOrganizerWriteError(err)
	}
	return organizer, nil
}

func (s *TicketCatalogService) ApproveOrganizer(ctx context.Context, organizerID int64) (*models.Organizer, error) {
	organizer, err := s.repo.FindOrganizerByID(ctx, organizerID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if organizer.AuditStatus != models.AuditStatusPending {
		return nil, fmt.Errorf("%w: 只有待审核的主办方可以批准", ErrInvalidTicketCatalog)
	}
	organizer.Status = models.OrganizerStatusActive
	organizer.AuditStatus = models.AuditStatusApproved
	organizer.AuditNote = ""
	if err := s.db.WithContext(ctx).Save(organizer).Error; err != nil {
		return nil, err
	}
	return organizer, nil
}

func (s *TicketCatalogService) RejectOrganizer(ctx context.Context, organizerID int64, note string) (*models.Organizer, error) {
	organizer, err := s.repo.FindOrganizerByID(ctx, organizerID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if organizer.AuditStatus != models.AuditStatusPending {
		return nil, fmt.Errorf("%w: 只有待审核的主办方可以驳回", ErrInvalidTicketCatalog)
	}
	organizer.Status = models.OrganizerStatusDisabled
	organizer.AuditStatus = models.AuditStatusRejected
	organizer.AuditNote = strings.TrimSpace(note)
	if err := s.db.WithContext(ctx).Save(organizer).Error; err != nil {
		return nil, err
	}
	return organizer, nil
}

func (s *TicketCatalogService) ListAdminOrganizers(
	ctx context.Context,
	auditStatus string,
	page, pageSize int,
) ([]AdminOrganizerRow, int64, error) {
	query := TicketCatalogListQuery{Page: page, PageSize: pageSize}
	query.Normalize()
	db := s.db.WithContext(ctx).Model(&models.Organizer{})
	switch strings.TrimSpace(auditStatus) {
	case string(models.AuditStatusPending), string(models.AuditStatusApproved), string(models.AuditStatusRejected):
		db = db.Where("audit_status = ?", auditStatus)
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var organizers []models.Organizer
	if err := db.Order("create_time DESC").
		Limit(query.PageSize).Offset((query.Page - 1) * query.PageSize).
		Find(&organizers).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AdminOrganizerRow, 0, len(organizers))
	for _, organizer := range organizers {
		row := AdminOrganizerRow{Organizer: organizer}
		var member models.OrganizerMember
		if err := s.db.WithContext(ctx).
			Where("organizer_id = ? AND role = ?", organizer.ID, models.OrganizerRoleOwner).
			First(&member).Error; err == nil {
			var user models.User
			if err := s.db.WithContext(ctx).Select("username").First(&user, member.UserID).Error; err == nil {
				row.OwnerUsername = user.Username
			}
		}
		rows = append(rows, row)
	}
	return rows, total, nil
}

func (s *TicketCatalogService) ListPendingEventReviews(
	ctx context.Context,
	page, pageSize int,
) ([]AdminEventReviewRow, int64, error) {
	query := TicketCatalogListQuery{Page: page, PageSize: pageSize}
	query.Normalize()
	db := s.db.WithContext(ctx).Model(&models.Event{}).
		Where("status = ?", models.EventStatusPendingReview)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var events []models.Event
	if err := db.Preload("Organizer").
		Order("update_time DESC").
		Limit(query.PageSize).Offset((query.Page - 1) * query.PageSize).
		Find(&events).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]AdminEventReviewRow, 0, len(events))
	for _, event := range events {
		rows = append(rows, AdminEventReviewRow{
			Event:         event,
			OrganizerName: event.Organizer.Name,
			OrganizerID:   event.OrganizerID,
		})
	}
	return rows, total, nil
}

func (s *TicketCatalogService) GetAdminPlatformOverview(ctx context.Context) (*AdminPlatformOverview, error) {
	out := &AdminPlatformOverview{Runtime: metrics.Snapshot()}
	if err := s.db.WithContext(ctx).Model(&models.Organizer{}).
		Where("audit_status = ?", models.AuditStatusPending).
		Count(&out.PendingOrganizers).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Model(&models.Event{}).
		Where("status = ?", models.EventStatusPendingReview).
		Count(&out.PendingEvents).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Model(&models.Organizer{}).
		Where("status = ? AND audit_status = ?", models.OrganizerStatusActive, models.AuditStatusApproved).
		Count(&out.ActiveOrganizers).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Model(&models.Event{}).
		Where("status = ?", models.EventStatusPublished).
		Count(&out.PublishedEvents).Error; err != nil {
		return nil, err
	}
	return out, nil
}

func wrapOrganizerWriteError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrDuplicatedKey) || strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		return fmt.Errorf("%w: 标识已被占用", ErrInvalidTicketCatalog)
	}
	return err
}
