package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/repository"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrTicketResourceNotFound = errors.New("票务资源不存在")
	ErrOrganizerForbidden     = errors.New("无权管理该主办方")
	ErrInvalidTicketCatalog   = errors.New("票务目录参数不合法")
	ErrOrganizerUnavailable   = errors.New("主办方尚未通过审核或已停用")
)

var organizerSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

type TicketCatalogListQuery struct {
	Page     int    `form:"page,default=1"`
	PageSize int    `form:"page_size,default=12"`
	City     string `form:"city"`
	Category string `form:"category"`
}

func (q *TicketCatalogListQuery) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 12
	}
	q.City = strings.TrimSpace(q.City)
	q.Category = strings.TrimSpace(q.Category)
}

type CreateOrganizerInput struct {
	Name         string `json:"name" binding:"required"`
	Slug         string `json:"slug" binding:"required"`
	LogoURL      string `json:"logo_url"`
	Description  string `json:"description"`
	ContactName  string `json:"contact_name"`
	ContactPhone string `json:"contact_phone"`
	OwnerUserID  int64  `json:"owner_user_id,string" binding:"required"`
}

type CreateVenueInput struct {
	Name      string   `json:"name" binding:"required"`
	City      string   `json:"city" binding:"required"`
	District  string   `json:"district"`
	Address   string   `json:"address" binding:"required"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Timezone  string   `json:"timezone"`
}

type CreateEventInput struct {
	Title              string `json:"title" binding:"required"`
	Subtitle           string `json:"subtitle"`
	Category           string `json:"category" binding:"required"`
	CoverURL           string `json:"cover_url"`
	Description        string `json:"description"`
	RealNameRequired   bool   `json:"real_name_required"`
	MaxTicketsPerOrder int    `json:"max_tickets_per_order"`
}

type CreateEventSessionInput struct {
	VenueID      int64     `json:"venue_id,string" binding:"required"`
	StartsAt     time.Time `json:"starts_at" binding:"required"`
	EndsAt       time.Time `json:"ends_at" binding:"required"`
	SaleStartsAt time.Time `json:"sale_starts_at" binding:"required"`
	SaleEndsAt   time.Time `json:"sale_ends_at" binding:"required"`
}

type CreateTicketTierInput struct {
	Name               string `json:"name" binding:"required"`
	Description        string `json:"description"`
	PriceCents         int64  `json:"price_cents" binding:"required"`
	OriginalPriceCents *int64 `json:"original_price_cents"`
	TotalQuota         int    `json:"total_quota" binding:"required"`
	PurchaseLimit      int    `json:"purchase_limit"`
}

type MyOrganizerView struct {
	Organizer models.Organizer     `json:"organizer"`
	Role      models.OrganizerRole `json:"role"`
}

type OrganizerOverview struct {
	OnSaleEvents         int64 `json:"on_sale_events"`
	PaidTickets          int64 `json:"paid_tickets"`
	PaidRevenueCents     int64 `json:"paid_revenue_cents"`
	PendingPaymentOrders int64 `json:"pending_payment_orders"`
}

type TicketCatalogService struct {
	repo repository.TicketCatalogRepository
	db   *gorm.DB
	rdb  *redis.Client
}

func NewTicketCatalogService(c *container.Container) *TicketCatalogService {
	return &TicketCatalogService{repo: c.TicketCatalogRepo, db: c.DB, rdb: c.RDB}
}

func (s *TicketCatalogService) CreateOrganizer(
	ctx context.Context,
	input CreateOrganizerInput,
) (*models.Organizer, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	if input.Name == "" || !organizerSlugPattern.MatchString(input.Slug) || input.OwnerUserID <= 0 {
		return nil, ErrInvalidTicketCatalog
	}
	var userCount int64
	if err := s.db.WithContext(ctx).Model(&models.User{}).
		Where("id = ?", input.OwnerUserID).Count(&userCount).Error; err != nil {
		return nil, err
	}
	if userCount == 0 {
		return nil, fmt.Errorf("%w: 负责人用户不存在", ErrInvalidTicketCatalog)
	}
	organizer := &models.Organizer{
		Name:         input.Name,
		Slug:         input.Slug,
		LogoURL:      strings.TrimSpace(input.LogoURL),
		Description:  strings.TrimSpace(input.Description),
		ContactName:  strings.TrimSpace(input.ContactName),
		ContactPhone: strings.TrimSpace(input.ContactPhone),
		Status:       models.OrganizerStatusActive,
		AuditStatus:  models.AuditStatusApproved,
	}
	if err := s.repo.CreateOrganizerWithOwner(ctx, organizer, input.OwnerUserID); err != nil {
		return nil, err
	}
	return organizer, nil
}

func (s *TicketCatalogService) ListOrganizers(
	ctx context.Context,
	page, pageSize int,
) ([]models.Organizer, int64, error) {
	query := TicketCatalogListQuery{Page: page, PageSize: pageSize}
	query.Normalize()
	return s.repo.ListOrganizers(ctx, query.Page, query.PageSize)
}

func (s *TicketCatalogService) ListMyOrganizers(
	ctx context.Context,
	userID int64,
) ([]MyOrganizerView, error) {
	if userID <= 0 {
		return nil, ErrOrganizerForbidden
	}
	memberships, err := s.repo.ListActiveMembershipsByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	organizers := make([]MyOrganizerView, 0, len(memberships))
	for _, membership := range memberships {
		if membership.Organizer.Status != models.OrganizerStatusActive {
			continue
		}
		organizers = append(organizers, MyOrganizerView{
			Organizer: membership.Organizer,
			Role:      membership.Role,
		})
	}
	return organizers, nil
}

func (s *TicketCatalogService) requireOrganizerAccess(
	ctx context.Context,
	organizerID, userID int64,
) error {
	if organizerID <= 0 || userID <= 0 {
		return ErrOrganizerForbidden
	}
	if _, err := s.repo.FindActiveMembership(ctx, organizerID, userID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrganizerForbidden
		}
		return err
	}
	organizer, err := s.repo.FindOrganizerByID(ctx, organizerID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrOrganizerForbidden
		}
		return err
	}
	if organizer.Status != models.OrganizerStatusActive {
		return ErrOrganizerUnavailable
	}
	return nil
}

func (s *TicketCatalogService) CreateVenue(
	ctx context.Context,
	userID, organizerID int64,
	input CreateVenueInput,
) (*models.Venue, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	input.Name = strings.TrimSpace(input.Name)
	input.City = strings.TrimSpace(input.City)
	input.Address = strings.TrimSpace(input.Address)
	if input.Name == "" || input.City == "" || input.Address == "" {
		return nil, ErrInvalidTicketCatalog
	}
	timezone := strings.TrimSpace(input.Timezone)
	if timezone == "" {
		timezone = "Asia/Shanghai"
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return nil, fmt.Errorf("%w: 无效时区", ErrInvalidTicketCatalog)
	}
	venue := &models.Venue{
		OrganizerID: organizerID,
		Name:        input.Name,
		City:        input.City,
		District:    strings.TrimSpace(input.District),
		Address:     input.Address,
		Latitude:    input.Latitude,
		Longitude:   input.Longitude,
		Timezone:    timezone,
		Status:      models.OrganizerStatusActive,
	}
	if err := s.repo.CreateVenue(ctx, venue); err != nil {
		return nil, err
	}
	return venue, nil
}

func (s *TicketCatalogService) ListVenues(
	ctx context.Context,
	userID, organizerID int64,
) ([]models.Venue, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	return s.repo.ListVenuesByOrganizer(ctx, organizerID)
}

func (s *TicketCatalogService) CreateEvent(
	ctx context.Context,
	userID, organizerID int64,
	input CreateEventInput,
) (*models.Event, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Category = strings.TrimSpace(input.Category)
	if input.Title == "" || input.Category == "" {
		return nil, ErrInvalidTicketCatalog
	}
	if input.MaxTicketsPerOrder == 0 {
		input.MaxTicketsPerOrder = 6
	}
	if input.MaxTicketsPerOrder < 1 || input.MaxTicketsPerOrder > 20 {
		return nil, fmt.Errorf("%w: 单笔限购须为 1 到 20", ErrInvalidTicketCatalog)
	}
	event := &models.Event{
		OrganizerID:        organizerID,
		Title:              input.Title,
		Subtitle:           strings.TrimSpace(input.Subtitle),
		Category:           input.Category,
		CoverURL:           strings.TrimSpace(input.CoverURL),
		Description:        strings.TrimSpace(input.Description),
		Status:             models.EventStatusDraft,
		RealNameRequired:   input.RealNameRequired,
		MaxTicketsPerOrder: input.MaxTicketsPerOrder,
	}
	if err := s.repo.CreateEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *TicketCatalogService) PublishEvent(
	ctx context.Context,
	userID, organizerID, eventID int64,
) (*models.Event, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	organizer, err := s.repo.FindOrganizerByID(ctx, organizerID)
	if err != nil {
		return nil, err
	}
	if organizer.Status != models.OrganizerStatusActive ||
		organizer.AuditStatus != models.AuditStatusApproved {
		return nil, ErrOrganizerUnavailable
	}
	event, err := s.repo.FindEventDetail(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status != models.EventStatusDraft {
		return nil, fmt.Errorf("%w: 只有草稿活动可以发布", ErrInvalidTicketCatalog)
	}
	if len(event.Sessions) == 0 {
		return nil, fmt.Errorf("%w: 至少需要一个场次", ErrInvalidTicketCatalog)
	}
	for _, session := range event.Sessions {
		if len(session.TicketTiers) == 0 {
			return nil, fmt.Errorf("%w: 每个场次至少需要一个票档", ErrInvalidTicketCatalog)
		}
	}
	stockValues := make(map[string]interface{})
	for _, session := range event.Sessions {
		for _, tier := range session.TicketTiers {
			stockValues[ticketStockKey(tier.ID)] = tier.RemainingQuota
		}
	}
	if err := s.rdb.MSet(ctx, stockValues).Err(); err != nil {
		return nil, fmt.Errorf("发布前初始化票额缓存: %w", err)
	}
	now := time.Now()
	event.Status = models.EventStatusPublished
	event.PublishedAt = &now
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *TicketCatalogService) CancelEvent(
	ctx context.Context,
	userID, organizerID, eventID int64,
) (*models.Event, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	event, err := s.repo.FindEventDetail(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status != models.EventStatusPublished && event.Status != models.EventStatusDraft {
		return nil, fmt.Errorf("%w: 只有草稿或已发布活动可以取消", ErrInvalidTicketCatalog)
	}
	stockKeys := make([]string, 0)
	for si := range event.Sessions {
		event.Sessions[si].Status = models.SessionStatusCancelled
		for ti := range event.Sessions[si].TicketTiers {
			event.Sessions[si].TicketTiers[ti].Status = models.TicketTierStatusDisabled
			stockKeys = append(stockKeys, ticketStockKey(event.Sessions[si].TicketTiers[ti].ID))
		}
	}
	event.Status = models.EventStatusCancelled
	if len(stockKeys) > 0 {
		if err := s.rdb.Del(ctx, stockKeys...).Err(); err != nil {
			return nil, fmt.Errorf("清理票额缓存: %w", err)
		}
	}
	for _, session := range event.Sessions {
		if err := s.db.WithContext(ctx).Model(&models.EventSession{}).
			Where("id = ?", session.ID).
			Update("status", models.SessionStatusCancelled).Error; err != nil {
			return nil, err
		}
		for _, tier := range session.TicketTiers {
			if err := s.db.WithContext(ctx).Model(&models.TicketTier{}).
				Where("id = ?", tier.ID).
				Update("status", models.TicketTierStatusDisabled).Error; err != nil {
				return nil, err
			}
		}
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *TicketCatalogService) UnpublishEvent(
	ctx context.Context,
	userID, organizerID, eventID int64,
) (*models.Event, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	event, err := s.repo.FindEventDetail(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status != models.EventStatusPublished {
		return nil, fmt.Errorf("%w: 只有已发布活动可以下架", ErrInvalidTicketCatalog)
	}
	var paidCount int64
	if err := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("event_id = ? AND status = ?", eventID, models.TicketOrderStatusPaid).
		Count(&paidCount).Error; err != nil {
		return nil, err
	}
	if paidCount > 0 {
		return nil, fmt.Errorf("%w: 存在已支付订单，请先取消活动并退款", ErrInvalidTicketCatalog)
	}
	stockKeys := make([]string, 0)
	for _, session := range event.Sessions {
		for _, tier := range session.TicketTiers {
			stockKeys = append(stockKeys, ticketStockKey(tier.ID))
		}
	}
	if len(stockKeys) > 0 {
		if err := s.rdb.Del(ctx, stockKeys...).Err(); err != nil {
			return nil, fmt.Errorf("清理票额缓存: %w", err)
		}
	}
	event.Status = models.EventStatusDraft
	event.PublishedAt = nil
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *TicketCatalogService) DisableTicketTier(
	ctx context.Context,
	userID, organizerID, tierID int64,
) (*models.TicketTier, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	var tier models.TicketTier
	if err := s.db.WithContext(ctx).First(&tier, tierID).Error; err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	session, err := s.repo.FindSessionByID(ctx, tier.SessionID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	event, err := s.repo.FindEventByID(ctx, session.EventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	tier.Status = models.TicketTierStatusDisabled
	if err := s.db.WithContext(ctx).Save(&tier).Error; err != nil {
		return nil, err
	}
	if err := s.rdb.Del(ctx, ticketStockKey(tier.ID)).Err(); err != nil {
		return nil, fmt.Errorf("清理票额缓存: %w", err)
	}
	return &tier, nil
}

func (s *TicketCatalogService) CreateSession(
	ctx context.Context,
	userID, organizerID, eventID int64,
	input CreateEventSessionInput,
) (*models.EventSession, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	event, err := s.repo.FindEventByID(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	venue, err := s.repo.FindVenueByID(ctx, input.VenueID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID || venue.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status != models.EventStatusDraft {
		return nil, fmt.Errorf("%w: 已发布活动不可新增场次", ErrInvalidTicketCatalog)
	}
	if !input.StartsAt.Before(input.EndsAt) ||
		!input.SaleStartsAt.Before(input.SaleEndsAt) ||
		input.SaleEndsAt.After(input.StartsAt) {
		return nil, fmt.Errorf("%w: 场次或售票时间范围错误", ErrInvalidTicketCatalog)
	}
	session := &models.EventSession{
		EventID:      eventID,
		VenueID:      input.VenueID,
		StartsAt:     input.StartsAt,
		EndsAt:       input.EndsAt,
		SaleStartsAt: input.SaleStartsAt,
		SaleEndsAt:   input.SaleEndsAt,
		Status:       models.SessionStatusOnSale,
	}
	if err := s.repo.CreateSession(ctx, session); err != nil {
		return nil, err
	}
	return session, nil
}

func (s *TicketCatalogService) CreateTicketTier(
	ctx context.Context,
	userID, organizerID, sessionID int64,
	input CreateTicketTierInput,
) (*models.TicketTier, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	session, err := s.repo.FindSessionByID(ctx, sessionID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	event, err := s.repo.FindEventByID(ctx, session.EventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status == models.EventStatusPublished {
		return nil, fmt.Errorf("%w: 已发布活动不可新增票档，请先下架或创建新活动", ErrInvalidTicketCatalog)
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || input.PriceCents <= 0 || input.TotalQuota <= 0 {
		return nil, ErrInvalidTicketCatalog
	}
	if input.PurchaseLimit == 0 {
		input.PurchaseLimit = event.MaxTicketsPerOrder
	}
	if input.PurchaseLimit < 1 || input.PurchaseLimit > event.MaxTicketsPerOrder {
		return nil, fmt.Errorf("%w: 票档限购不能超过活动单笔限购", ErrInvalidTicketCatalog)
	}
	if input.OriginalPriceCents != nil && *input.OriginalPriceCents < input.PriceCents {
		return nil, fmt.Errorf("%w: 原价不能低于售价", ErrInvalidTicketCatalog)
	}
	tier := &models.TicketTier{
		SessionID:          sessionID,
		Name:               input.Name,
		Description:        strings.TrimSpace(input.Description),
		PriceCents:         input.PriceCents,
		OriginalPriceCents: input.OriginalPriceCents,
		TotalQuota:         input.TotalQuota,
		RemainingQuota:     input.TotalQuota,
		PurchaseLimit:      input.PurchaseLimit,
		Status:             models.TicketTierStatusOnSale,
	}
	if err := s.repo.CreateTicketTier(ctx, tier); err != nil {
		return nil, err
	}
	return tier, nil
}

func (s *TicketCatalogService) ListPublishedEvents(
	ctx context.Context,
	query TicketCatalogListQuery,
) ([]models.Event, int64, error) {
	query.Normalize()
	return s.repo.ListPublishedEvents(ctx, query.City, query.Category, query.Page, query.PageSize)
}

func (s *TicketCatalogService) GetPublishedEvent(
	ctx context.Context,
	eventID int64,
) (*models.Event, error) {
	event, err := s.repo.FindEventDetail(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.Status != models.EventStatusPublished {
		return nil, ErrTicketResourceNotFound
	}
	return event, nil
}

func (s *TicketCatalogService) ListOrganizerEvents(
	ctx context.Context,
	userID, organizerID int64,
	query TicketCatalogListQuery,
) ([]models.Event, int64, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, 0, err
	}
	query.Normalize()
	return s.repo.ListEventsByOrganizer(ctx, organizerID, query.Page, query.PageSize)
}

func (s *TicketCatalogService) GetOrganizerOverview(
	ctx context.Context,
	userID, organizerID int64,
) (*OrganizerOverview, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	overview := &OrganizerOverview{}
	if err := s.db.WithContext(ctx).Model(&models.Event{}).
		Where("organizer_id = ? AND status = ?", organizerID, models.EventStatusPublished).
		Count(&overview.OnSaleEvents).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("organizer_id = ? AND status = ?", organizerID, models.TicketOrderStatusPendingPayment).
		Count(&overview.PendingPaymentOrders).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("organizer_id = ? AND status = ? AND payment_status = ?",
			organizerID, models.TicketOrderStatusPaid, models.PaymentStatusPaid).
		Select("COALESCE(SUM(total_amount_cents), 0)").
		Scan(&overview.PaidRevenueCents).Error; err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Model(&models.TicketOrderItem{}).
		Joins("JOIN ticket_order ON ticket_order.id = ticket_order_item.order_id").
		Where("ticket_order.organizer_id = ? AND ticket_order.status = ?",
			organizerID, models.TicketOrderStatusPaid).
		Where("ticket_order.delete_time IS NULL").
		Select("COALESCE(SUM(ticket_order_item.quantity), 0)").
		Scan(&overview.PaidTickets).Error; err != nil {
		return nil, err
	}
	return overview, nil
}

func (s *TicketCatalogService) ListOrganizerOrders(
	ctx context.Context,
	userID, organizerID int64,
	page, pageSize int,
) ([]models.TicketOrder, int64, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, 0, err
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}
	var orders []models.TicketOrder
	var total int64
	query := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("organizer_id = ?", organizerID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Preload("Items").
		Order("create_time DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&orders).Error
	return orders, total, err
}

func normalizeTicketNotFound(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrTicketResourceNotFound
	}
	return err
}
