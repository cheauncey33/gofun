package service

import (
	"context"
	"errors"
	"fmt"
	"gofun/config"
	"gofun/container"
	"gofun/models"
	"gofun/repository"
	"gofun/search"
	"log"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

var (
	ErrTicketResourceNotFound = errors.New("票务资源不存在")
	ErrOrganizerForbidden     = errors.New("无权管理该主办方")
	ErrInvalidTicketCatalog   = errors.New("票务目录参数不合法")
	ErrOrganizerUnavailable   = errors.New("主办方尚未通过审核或已停用")
	errESProjectionIncomplete = errors.New("ES 活动投影不完整")
)

var organizerSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$`)

type TicketCatalogListQuery struct {
	Page     int    `form:"page,default=1"`
	PageSize int    `form:"page_size,default=12"`
	City     string `form:"city"`
	// Category 支持单个或逗号分隔多选，如 "脱口秀,音乐节"
	Category string `form:"category"`
	Keyword  string `form:"keyword"`
}

func (q *TicketCatalogListQuery) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 12
	}
	q.City = strings.TrimSpace(q.City)
	q.Category = strings.Join(splitCSV(q.Category), ",")
	q.Keyword = strings.TrimSpace(q.Keyword)
}

func (q TicketCatalogListQuery) Categories() []string {
	return splitCSV(q.Category)
}

func splitCSV(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		out = append(out, item)
	}
	return out
}

type CreateOrganizerInput struct {
	Name          string `json:"name" binding:"required"`
	Slug          string `json:"slug" binding:"required"`
	LogoURL       string `json:"logo_url"`
	Description   string `json:"description"`
	ContactName   string `json:"contact_name"`
	ContactPhone  string `json:"contact_phone"`
	OwnerUserID   int64  `json:"owner_user_id,string"`
	OwnerUsername string `json:"owner_username"`
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
	SaleMode           string `json:"sale_mode"`
}

type CreateEventSessionInput struct {
	VenueID      int64     `json:"venue_id,string" binding:"required"`
	HallID       int64     `json:"hall_id,string"`
	SeatLayoutID int64     `json:"seat_layout_id,string"`
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
	TotalQuota         int    `json:"total_quota"`
	PurchaseLimit      int    `json:"purchase_limit"`
	AssignPlaceNo      *bool  `json:"assign_place_no"`
}

type MyOrganizerView struct {
	Organizer models.Organizer     `json:"organizer"`
	Role      models.OrganizerRole `json:"role"`
}

type OrganizerOverview struct {
	PeriodDays              int       `json:"period_days"`
	PeriodFrom              time.Time `json:"period_from"`
	EventID                 int64     `json:"event_id,string,omitempty"`
	SessionID               int64     `json:"session_id,string,omitempty"`
	OnSaleEvents            int64     `json:"on_sale_events"`
	PaidOrders              int64     `json:"paid_orders"`
	PaidTickets             int64     `json:"paid_tickets"`
	GrossRevenueCents       int64     `json:"gross_revenue_cents"`
	RefundedOrders          int64     `json:"refunded_orders"`
	RefundedAmountCents     int64     `json:"refunded_amount_cents"`
	NetRevenueCents         int64     `json:"net_revenue_cents"`
	PreviousPaidOrders      int64     `json:"previous_paid_orders"`
	PreviousPaidTickets     int64     `json:"previous_paid_tickets"`
	PreviousGrossRevenueCents int64   `json:"previous_gross_revenue_cents"`
	PreviousNetRevenueCents int64     `json:"previous_net_revenue_cents"`
	PaymentFailedOrders     int64     `json:"payment_failed_orders"`
	TimeoutCancelledOrders  int64     `json:"timeout_cancelled_orders"`
	PaymentSuccessRate      float64   `json:"payment_success_rate"`
	PendingPaymentOrders    int64     `json:"pending_payment_orders"`
	RefundingOrders         int64     `json:"refunding_orders"`
	UpcomingSessions        int64     `json:"upcoming_sessions"`
	InventoryTotal          int64     `json:"inventory_total"`
	InventoryOccupied       int64     `json:"inventory_occupied"`
	InventoryOccupancyRate  float64   `json:"inventory_occupancy_rate"`
}

type organizerPeriodSales struct {
	PaidOrders          int64
	PaidTickets         int64
	GrossRevenueCents   int64
	RefundedOrders      int64
	RefundedAmountCents int64
}

type TicketCatalogService struct {
	repo      repository.TicketCatalogRepository
	db        *gorm.DB
	rdb       *redis.Client
	searcher  search.EventSearcher
	preferES  bool
	inventory InventoryBucketSettings
	cacheSF   singleflight.Group
	// rushSales 由 main.go 在构建后注入，用于活动取消时联动关闭关联抢票活动。
	rushSales *RushSaleService
}

// LinkRushSale 注入抢票服务；活动取消时联动关闭其关联的限时开售活动。
func (s *TicketCatalogService) LinkRushSale(rushSales *RushSaleService) {
	s.rushSales = rushSales
}

func NewTicketCatalogService(c *container.Container) *TicketCatalogService {
	searcher := c.EventSearcher
	if searcher == nil {
		searcher = search.NoopEventSearcher{}
	}
	return &TicketCatalogService{
		repo:     c.TicketCatalogRepo,
		db:       c.DB,
		rdb:      c.RDB,
		searcher: searcher,
		preferES: c.SearchPreferES && searcher.Enabled(),
	}
}

func (s *TicketCatalogService) ConfigureInventory(cfg config.InventoryConfig) {
	s.inventory = NewInventoryBucketSettings(cfg)
}

func (s *TicketCatalogService) CreateOrganizer(
	ctx context.Context,
	input CreateOrganizerInput,
) (*models.Organizer, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	if input.Name == "" || !organizerSlugPattern.MatchString(input.Slug) {
		return nil, ErrInvalidTicketCatalog
	}
	ownerID, err := s.resolveOrganizerOwnerID(ctx, input)
	if err != nil {
		return nil, err
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
	if err := s.repo.CreateOrganizerWithOwner(ctx, organizer, ownerID); err != nil {
		return nil, err
	}
	return organizer, nil
}

func (s *TicketCatalogService) resolveOrganizerOwnerID(
	ctx context.Context,
	input CreateOrganizerInput,
) (int64, error) {
	if input.OwnerUserID > 0 {
		var userCount int64
		if err := s.db.WithContext(ctx).Model(&models.User{}).
			Where("id = ?", input.OwnerUserID).Count(&userCount).Error; err != nil {
			return 0, err
		}
		if userCount == 0 {
			return 0, fmt.Errorf("%w: 负责人用户不存在", ErrInvalidTicketCatalog)
		}
		return input.OwnerUserID, nil
	}
	username := strings.TrimSpace(input.OwnerUsername)
	if username == "" {
		return 0, fmt.Errorf("%w: 请填写负责人用户名", ErrInvalidTicketCatalog)
	}
	var user models.User
	err := s.db.WithContext(ctx).Select("id").Where("username = ?", username).First(&user).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, fmt.Errorf("%w: 负责人用户不存在", ErrInvalidTicketCatalog)
	}
	if err != nil {
		return 0, err
	}
	return user.ID, nil
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
		if membership.Organizer.ID == 0 {
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
	if organizer.Status != models.OrganizerStatusActive ||
		organizer.AuditStatus != models.AuditStatusApproved {
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
	saleMode, err := models.ParseEventSaleMode(input.SaleMode)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidTicketCatalog, err.Error())
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
		SaleMode:           saleMode,
	}
	if err := s.repo.CreateEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *TicketCatalogService) UpdateEvent(
	ctx context.Context,
	userID, organizerID, eventID int64,
	input CreateEventInput,
) (*models.Event, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	event, err := s.repo.FindEventByID(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status != models.EventStatusDraft && event.Status != models.EventStatusPublished {
		return nil, fmt.Errorf("%w: 只有草稿或售票中的活动可以编辑", ErrInvalidTicketCatalog)
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Category = strings.TrimSpace(input.Category)
	if input.Title == "" || input.Category == "" {
		return nil, ErrInvalidTicketCatalog
	}
	event.Title = input.Title
	event.Subtitle = strings.TrimSpace(input.Subtitle)
	event.Category = input.Category
	event.CoverURL = strings.TrimSpace(input.CoverURL)
	event.Description = strings.TrimSpace(input.Description)
	if event.Status == models.EventStatusDraft {
		if input.MaxTicketsPerOrder == 0 {
			input.MaxTicketsPerOrder = event.MaxTicketsPerOrder
		}
		if input.MaxTicketsPerOrder == 0 {
			input.MaxTicketsPerOrder = 6
		}
		if input.MaxTicketsPerOrder < 1 || input.MaxTicketsPerOrder > 20 {
			return nil, fmt.Errorf("%w: 单笔限购须为 1 到 20", ErrInvalidTicketCatalog)
		}
		event.MaxTicketsPerOrder = input.MaxTicketsPerOrder
		event.RealNameRequired = input.RealNameRequired
		saleMode, err := models.ParseEventSaleMode(input.SaleMode)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidTicketCatalog, err.Error())
		}
		event.SaleMode = saleMode
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	if event.Status == models.EventStatusPublished {
		if detailed, detailErr := s.repo.FindEventDetail(ctx, event.ID); detailErr == nil {
			s.upsertEventSearchIndex(ctx, detailed)
		}
	}
	s.invalidateCatalogCaches(ctx, event.ID)
	return event, nil
}

func (s *TicketCatalogService) SubmitEventForReview(
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
	if event.Status != models.EventStatusDraft {
		return nil, fmt.Errorf("%w: 只有草稿活动可以提交审核", ErrInvalidTicketCatalog)
	}
	if err := validateEventReadyToSell(event); err != nil {
		return nil, err
	}
	event.Status = models.EventStatusPendingReview
	event.ReviewNote = ""
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *TicketCatalogService) WithdrawEventReview(
	ctx context.Context,
	userID, organizerID, eventID int64,
) (*models.Event, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	event, err := s.repo.FindEventByID(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status != models.EventStatusPendingReview {
		return nil, fmt.Errorf("%w: 只有审核中的活动可以撤回", ErrInvalidTicketCatalog)
	}
	event.Status = models.EventStatusDraft
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func (s *TicketCatalogService) AdminApproveEvent(ctx context.Context, eventID int64) (*models.Event, error) {
	event, err := s.repo.FindEventDetail(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.Status != models.EventStatusPendingReview {
		return nil, fmt.Errorf("%w: 只有待审核活动可以上架", ErrInvalidTicketCatalog)
	}
	organizer, err := s.repo.FindOrganizerByID(ctx, event.OrganizerID)
	if err != nil {
		return nil, err
	}
	if organizer.Status != models.OrganizerStatusActive ||
		organizer.AuditStatus != models.AuditStatusApproved {
		return nil, ErrOrganizerUnavailable
	}
	if err := validateEventReadyToSell(event); err != nil {
		return nil, err
	}
	return s.activatePublishedEvent(ctx, event)
}

func (s *TicketCatalogService) AdminRejectEvent(ctx context.Context, eventID int64, note string) (*models.Event, error) {
	event, err := s.repo.FindEventByID(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.Status != models.EventStatusPendingReview {
		return nil, fmt.Errorf("%w: 只有待审核活动可以驳回", ErrInvalidTicketCatalog)
	}
	event.Status = models.EventStatusDraft
	event.ReviewNote = strings.TrimSpace(note)
	if event.ReviewNote == "" {
		event.ReviewNote = "未通过上架审核"
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	return event, nil
}

func validateEventReadyToSell(event *models.Event) error {
	if event == nil {
		return ErrTicketResourceNotFound
	}
	if len(event.Sessions) == 0 {
		return fmt.Errorf("%w: 至少需要一个场次", ErrInvalidTicketCatalog)
	}
	for _, session := range event.Sessions {
		if len(session.TicketTiers) == 0 {
			return fmt.Errorf("%w: 每个场次至少需要一个票档", ErrInvalidTicketCatalog)
		}
	}
	return nil
}

func (s *TicketCatalogService) activatePublishedEvent(
	ctx context.Context,
	event *models.Event,
) (*models.Event, error) {
	if event.SaleMode.IsSeated() {
		if err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			return s.generateSessionSeats(tx, event)
		}); err != nil {
			return nil, err
		}
	} else {
		stockValues := make(map[string]interface{})
		for _, session := range event.Sessions {
			for _, tier := range session.TicketTiers {
				if s.inventory.Enabled {
					if err := EnsureTierBuckets(s.db.WithContext(ctx), &tier, s.inventory); err != nil {
						return nil, fmt.Errorf("发布前拆桶: %w", err)
					}
					var buckets []models.TicketTierBucket
					if err := s.db.WithContext(ctx).
						Where("tier_id = ?", tier.ID).Find(&buckets).Error; err != nil {
						return nil, err
					}
					for _, bucket := range buckets {
						stockValues[TicketStockBucketKey(bucket.TierID, bucket.BucketNo)] = bucket.RemainingQuota
					}
				} else {
					stockValues[ticketStockKey(tier.ID)] = tier.RemainingQuota
				}
			}
		}
		if err := s.rdb.MSet(ctx, stockValues).Err(); err != nil {
			return nil, fmt.Errorf("发布前初始化票额缓存: %w", err)
		}
	}
	now := time.Now()
	event.Status = models.EventStatusPublished
	event.PublishedAt = &now
	event.ReviewNote = ""
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	s.upsertEventSearchIndex(ctx, event)
	s.invalidateCatalogCaches(ctx, event.ID)
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
	if event.Status != models.EventStatusPublished && event.Status != models.EventStatusDraft &&
		event.Status != models.EventStatusPendingReview {
		return nil, fmt.Errorf("%w: 只有草稿、审核中或已发布活动可以取消", ErrInvalidTicketCatalog)
	}
	stockKeys := make([]string, 0)
	for si := range event.Sessions {
		event.Sessions[si].Status = models.SessionStatusCancelled
		for ti := range event.Sessions[si].TicketTiers {
			tier := &event.Sessions[si].TicketTiers[ti]
			tier.Status = models.TicketTierStatusDisabled
			stockKeys = append(stockKeys, s.tierStockRedisKeys(tier)...)
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
	s.removeEventSearchIndex(ctx, event.ID)
	s.invalidateCatalogCaches(ctx, event.ID)
	// 联动关闭该活动所有进行中的抢票活动，防止取消后仍可抢购下单。
	if s.rushSales != nil {
		if err := s.rushSales.CancelEventCampaigns(ctx, event.ID); err != nil {
			return nil, err
		}
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
	if event.SaleMode.IsSeated() {
		sessionIDs := make([]int64, 0, len(event.Sessions))
		for _, session := range event.Sessions {
			sessionIDs = append(sessionIDs, session.ID)
		}
		if len(sessionIDs) > 0 {
			var occupied int64
			if err := s.db.WithContext(ctx).Model(&models.SessionSeat{}).
				Where("session_id IN ? AND status IN ?", sessionIDs, []models.SessionSeatStatus{
					models.SessionSeatHeld, models.SessionSeatSold,
				}).Count(&occupied).Error; err != nil {
				return nil, err
			}
			if occupied > 0 {
				return nil, fmt.Errorf("%w: 仍有占用中的座位，请先处理待支付订单", ErrInvalidTicketCatalog)
			}
			if err := s.db.WithContext(ctx).Unscoped().
				Where("session_id IN ?", sessionIDs).
				Delete(&models.SessionSeat{}).Error; err != nil {
				return nil, err
			}
		}
	} else {
		stockKeys := make([]string, 0)
		for _, session := range event.Sessions {
			for i := range session.TicketTiers {
				stockKeys = append(stockKeys, s.tierStockRedisKeys(&session.TicketTiers[i])...)
			}
		}
		if len(stockKeys) > 0 {
			if err := s.rdb.Del(ctx, stockKeys...).Err(); err != nil {
				return nil, fmt.Errorf("清理票额缓存: %w", err)
			}
		}
	}
	event.Status = models.EventStatusDraft
	event.PublishedAt = nil
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return nil, err
	}
	s.removeEventSearchIndex(ctx, event.ID)
	s.invalidateCatalogCaches(ctx, event.ID)
	return event, nil
}

func (s *TicketCatalogService) tierStockRedisKeys(tier *models.TicketTier) []string {
	if !s.inventory.Enabled {
		return []string{ticketStockKey(tier.ID)}
	}
	n := s.inventory.EffectiveBucketCount(tier.TotalQuota)
	keys := make([]string, 0, n+1)
	keys = append(keys, ticketStockKey(tier.ID)) // 兼容清理旧单 key
	for i := 0; i < n; i++ {
		keys = append(keys, TicketStockBucketKey(tier.ID, i))
	}
	return keys
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
	s.invalidateCatalogCaches(ctx, event.ID)
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
	if err := validateSessionTimes(input.StartsAt, input.EndsAt, input.SaleStartsAt, input.SaleEndsAt); err != nil {
		return nil, err
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
	if event.SaleMode.IsSeated() {
		if input.HallID <= 0 || input.SeatLayoutID <= 0 {
			return nil, fmt.Errorf("%w: 选座场次必须选择演出厅和已发布厅图", ErrInvalidTicketCatalog)
		}
		if err := s.validatePublishedLayout(ctx, organizerID, input.VenueID, input.HallID, input.SeatLayoutID); err != nil {
			return nil, err
		}
		session.HallID = &input.HallID
		session.SeatLayoutID = &input.SeatLayoutID
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
	if input.Name == "" || input.PriceCents <= 0 {
		return nil, ErrInvalidTicketCatalog
	}
	if event.SaleMode.IsSeated() {
		input.TotalQuota = 0
	} else if input.TotalQuota <= 0 {
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
	assignPlaceNo := true
	if input.AssignPlaceNo != nil {
		assignPlaceNo = *input.AssignPlaceNo
	}
	if event.SaleMode.IsSeated() || event.Category == "展览" {
		assignPlaceNo = false
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
		AssignPlaceNo:      assignPlaceNo,
		Status:             models.TicketTierStatusOnSale,
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(tier).Error; err != nil {
			return err
		}
		if event.SaleMode.IsSeated() {
			return nil
		}
		return EnsureTierBuckets(tx, tier, s.inventory)
	})
	if err != nil {
		return nil, err
	}
	return tier, nil
}

func validateSessionTimes(startsAt, endsAt, saleStartsAt, saleEndsAt time.Time) error {
	if startsAt.IsZero() || endsAt.IsZero() || saleStartsAt.IsZero() || saleEndsAt.IsZero() {
		return fmt.Errorf("%w: 场次或售票时间范围错误", ErrInvalidTicketCatalog)
	}
	if !startsAt.Before(endsAt) || !saleStartsAt.Before(saleEndsAt) || saleEndsAt.After(startsAt) {
		return fmt.Errorf("%w: 场次或售票时间范围错误", ErrInvalidTicketCatalog)
	}
	return nil
}

func (s *TicketCatalogService) UpdateSession(
	ctx context.Context,
	userID, organizerID, sessionID int64,
	input CreateEventSessionInput,
) (*models.EventSession, error) {
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
	if event.Status != models.EventStatusDraft && event.Status != models.EventStatusPublished {
		return nil, fmt.Errorf("%w: 只有草稿或售票中的活动可以改场次", ErrInvalidTicketCatalog)
	}
	if err := validateSessionTimes(input.StartsAt, input.EndsAt, input.SaleStartsAt, input.SaleEndsAt); err != nil {
		return nil, err
	}
	if event.Status == models.EventStatusDraft {
		venue, venueErr := s.repo.FindVenueByID(ctx, input.VenueID)
		if venueErr != nil {
			return nil, normalizeTicketNotFound(venueErr)
		}
		if venue.OrganizerID != organizerID {
			return nil, ErrOrganizerForbidden
		}
		session.VenueID = input.VenueID
		if event.SaleMode.IsSeated() {
			if input.HallID <= 0 || input.SeatLayoutID <= 0 {
				return nil, fmt.Errorf("%w: 选座场次必须选择演出厅和已发布厅图", ErrInvalidTicketCatalog)
			}
			if err := s.validatePublishedLayout(ctx, organizerID, input.VenueID, input.HallID, input.SeatLayoutID); err != nil {
				return nil, err
			}
			session.HallID = &input.HallID
			session.SeatLayoutID = &input.SeatLayoutID
		}
	}
	session.StartsAt = input.StartsAt
	session.EndsAt = input.EndsAt
	session.SaleStartsAt = input.SaleStartsAt
	session.SaleEndsAt = input.SaleEndsAt
	if err := s.db.WithContext(ctx).Save(session).Error; err != nil {
		return nil, err
	}
	s.invalidateCatalogCaches(ctx, event.ID)
	return session, nil
}

type UpdateTicketTierInput struct {
	Name        string `json:"name" binding:"required"`
	Description string `json:"description"`
	PriceCents  int64  `json:"price_cents" binding:"required"`
	TotalQuota  *int   `json:"total_quota"`
}

func (s *TicketCatalogService) UpdateTicketTier(
	ctx context.Context,
	userID, organizerID, tierID int64,
	input UpdateTicketTierInput,
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
	if event.Status != models.EventStatusDraft && event.Status != models.EventStatusPublished {
		return nil, fmt.Errorf("%w: 只有草稿或售票中的活动可以改票档", ErrInvalidTicketCatalog)
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || input.PriceCents <= 0 {
		return nil, ErrInvalidTicketCatalog
	}
	quotaDelta := 0
	if input.TotalQuota != nil {
		if event.SaleMode.IsSeated() {
			return nil, fmt.Errorf("%w: 选座票额由厅图决定，请改厅图", ErrInvalidTicketCatalog)
		}
		next := *input.TotalQuota
		if next <= 0 {
			return nil, ErrInvalidTicketCatalog
		}
		if event.Status == models.EventStatusPublished {
			if next < tier.TotalQuota {
				return nil, fmt.Errorf("%w: 售票中只能加票，不能减票额", ErrInvalidTicketCatalog)
			}
			quotaDelta = next - tier.TotalQuota
			tier.RemainingQuota += quotaDelta
			tier.TotalQuota = next
			if tier.RemainingQuota > 0 {
				tier.Status = models.TicketTierStatusOnSale
			}
		} else {
			tier.TotalQuota = next
			tier.RemainingQuota = next
		}
	}
	tier.Name = input.Name
	tier.Description = strings.TrimSpace(input.Description)
	tier.PriceCents = input.PriceCents
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Save(&tier).Error; err != nil {
			return err
		}
		if quotaDelta > 0 {
			return s.addPublishedQuota(tx, &tier, quotaDelta)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if quotaDelta > 0 {
		if err := s.incrPublishedStock(ctx, &tier, quotaDelta); err != nil {
			return nil, fmt.Errorf("更新票额缓存: %w", err)
		}
	}
	s.invalidateCatalogCaches(ctx, event.ID)
	return &tier, nil
}

func (s *TicketCatalogService) addPublishedQuota(tx *gorm.DB, tier *models.TicketTier, delta int) error {
	if delta <= 0 || !s.inventory.Enabled {
		return nil
	}
	result := tx.Model(&models.TicketTierBucket{}).
		Where("tier_id = ? AND bucket_no = 0", tier.ID).
		Update("remaining_quota", gorm.Expr("remaining_quota + ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return EnsureTierBuckets(tx, tier, s.inventory)
	}
	return nil
}

func (s *TicketCatalogService) incrPublishedStock(ctx context.Context, tier *models.TicketTier, delta int) error {
	if s.rdb == nil || delta <= 0 {
		return nil
	}
	if s.inventory.Enabled {
		return s.rdb.IncrBy(ctx, TicketStockBucketKey(tier.ID, 0), int64(delta)).Err()
	}
	return s.rdb.IncrBy(ctx, ticketStockKey(tier.ID), int64(delta)).Err()
}

func (s *TicketCatalogService) ListPublishedEvents(
	ctx context.Context,
	query TicketCatalogListQuery,
) ([]models.Event, int64, error) {
	query.Normalize()
	listKey := catalogEventsListKey(
		s.catalogListVersion(ctx),
		query.City,
		query.Category,
		query.Keyword,
		query.Page,
		query.PageSize,
	)
	var cached cachedEventList
	err := cacheAsideJSON(ctx, s.rdb, &s.cacheSF, listKey, catalogEventsListTTL, &cached, func(ctx context.Context) (cachedEventList, error) {
		var (
			events []models.Event
			total  int64
			err    error
		)
		if query.Keyword != "" && s.preferES {
			events, total, err = s.listPublishedViaES(ctx, query)
			if err != nil {
				log.Printf("[catalog] ES 检索不可用或结果不完整，降级 MySQL LIKE: %v", err)
				events, total, err = s.repo.ListPublishedEvents(
					ctx, query.City, query.Categories(), query.Keyword, query.Page, query.PageSize,
				)
			}
		} else {
			events, total, err = s.repo.ListPublishedEvents(
				ctx, query.City, query.Categories(), query.Keyword, query.Page, query.PageSize,
			)
		}
		if err != nil {
			return cachedEventList{}, err
		}
		return cachedEventList{Events: events, Total: total}, nil
	})
	if err != nil {
		return nil, 0, err
	}
	return cached.Events, cached.Total, nil
}

func (s *TicketCatalogService) listPublishedViaES(
	ctx context.Context,
	query TicketCatalogListQuery,
) ([]models.Event, int64, error) {
	ids, total, err := s.searcher.Search(ctx, search.Query{
		Keyword:    query.Keyword,
		City:       query.City,
		Categories: query.Categories(),
		Page:       query.Page,
		PageSize:   query.PageSize,
	})
	if err != nil {
		return nil, 0, err
	}
	if len(ids) == 0 {
		return nil, 0, fmt.Errorf("%w: ES 未返回当前页 ID（total=%d）", errESProjectionIncomplete, total)
	}
	events, err := s.repo.ListPublishedEventsByIDs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	if len(events) != len(ids) {
		return nil, 0, fmt.Errorf(
			"%w: ES 返回 %d 个 ID，MySQL 仅确认 %d 个已发布活动",
			errESProjectionIncomplete, len(ids), len(events),
		)
	}
	return events, total, nil
}

// ReindexPublishedEvents 启动时全量重建 ES 投影（best-effort）。
func (s *TicketCatalogService) ReindexPublishedEvents(ctx context.Context) (err error) {
	if !s.searcher.Enabled() {
		return nil
	}
	defer func() {
		if err != nil {
			// ResetIndex 后若只写入部分文档，继续使用 ES 会产生无法检测的漏检。
			s.preferES = false
		}
	}()
	if err := s.searcher.ResetIndex(ctx); err != nil {
		return err
	}
	_, err = s.SyncPublishedEvents(ctx)
	return err
}

type EventSearchSyncResult struct {
	Indexed int
	Missing int
	Deleted int
}

// SyncPublishedEvents 以 MySQL 为准修复 ES 投影，并清理已取消、下架或删除的孤儿文档。
func (s *TicketCatalogService) SyncPublishedEvents(ctx context.Context) (EventSearchSyncResult, error) {
	var result EventSearchSyncResult
	if !s.searcher.Enabled() {
		return result, nil
	}
	indexedIDs, err := s.searcher.ListEventIDs(ctx)
	if err != nil {
		return result, err
	}
	existingIDs := make(map[int64]struct{}, len(indexedIDs))
	for _, eventID := range indexedIDs {
		existingIDs[eventID] = struct{}{}
	}
	publishedIDs := make(map[int64]struct{})
	const pageSize = 50
	page := 1
	for {
		events, total, err := s.repo.ListPublishedEvents(ctx, "", nil, "", page, pageSize)
		if err != nil {
			return result, err
		}
		for i := range events {
			if err := s.indexEventSearchIndex(ctx, &events[i], false); err != nil {
				return result, fmt.Errorf("同步 ES 活动 %d: %w", events[i].ID, err)
			}
			publishedIDs[events[i].ID] = struct{}{}
			result.Indexed++
			if _, ok := existingIDs[events[i].ID]; !ok {
				result.Missing++
			}
		}
		if int64(page*pageSize) >= total || len(events) == 0 {
			break
		}
		page++
	}

	for _, eventID := range indexedIDs {
		if _, ok := publishedIDs[eventID]; ok {
			continue
		}
		event, findErr := s.repo.FindEventByID(ctx, eventID)
		if findErr != nil && !errors.Is(findErr, gorm.ErrRecordNotFound) {
			return result, findErr
		}
		if findErr == nil && event.Status == models.EventStatusPublished {
			// 活动可能在分页扫描后刚发布，不能误删。
			continue
		}
		if err := s.searcher.DeleteEvent(ctx, eventID, false); err != nil {
			return result, fmt.Errorf("清理 ES 孤儿活动 %d: %w", eventID, err)
		}
		result.Deleted++
	}
	if result.Indexed == 0 && result.Deleted == 0 {
		return result, nil
	}
	if err := s.searcher.RefreshIndex(ctx); err != nil {
		return result, err
	}
	return result, nil
}

func (s *TicketCatalogService) upsertEventSearchIndex(ctx context.Context, event *models.Event) {
	if s.searcher == nil || !s.searcher.Enabled() || event == nil {
		return
	}
	if err := s.indexEventSearchIndex(ctx, event, true); err != nil {
		log.Printf("[catalog] ES 索引活动 %d 失败: %v", event.ID, err)
	}
}

func (s *TicketCatalogService) indexEventSearchIndex(
	ctx context.Context,
	event *models.Event,
	refresh bool,
) error {
	cities := make([]string, 0)
	venues := make([]string, 0)
	seenCity := map[string]struct{}{}
	for _, session := range event.Sessions {
		if session.Venue.City != "" {
			if _, ok := seenCity[session.Venue.City]; !ok {
				seenCity[session.Venue.City] = struct{}{}
				cities = append(cities, session.Venue.City)
			}
		}
		if session.Venue.Name != "" {
			venues = append(venues, session.Venue.Name)
		}
	}
	doc := search.BuildEventDoc(
		event.ID, event.Title, event.Subtitle, event.Category, string(event.Status),
		event.PublishedAt, cities, venues,
	)
	return s.searcher.IndexEvent(ctx, doc, refresh)
}

func (s *TicketCatalogService) removeEventSearchIndex(ctx context.Context, eventID int64) {
	if s.searcher == nil || !s.searcher.Enabled() {
		return
	}
	if err := s.searcher.DeleteEvent(ctx, eventID, true); err != nil {
		log.Printf("[catalog] ES 删除活动 %d 失败: %v", eventID, err)
	}
}

type CatalogMeta struct {
	Cities     []string `json:"cities"`
	Categories []string `json:"categories"`
}

func (s *TicketCatalogService) GetCatalogMeta(ctx context.Context) (*CatalogMeta, error) {
	var cached CatalogMeta
	err := cacheAsideJSON(ctx, s.rdb, &s.cacheSF, catalogMetaKey, catalogMetaTTL, &cached, func(ctx context.Context) (CatalogMeta, error) {
		cities, categories, err := s.repo.ListPublishedFacets(ctx)
		if err != nil {
			return CatalogMeta{}, err
		}
		return CatalogMeta{Cities: cities, Categories: categories}, nil
	})
	if err != nil {
		return nil, err
	}
	return &cached, nil
}

func (s *TicketCatalogService) GetPublishedEvent(
	ctx context.Context,
	eventID int64,
) (*models.Event, error) {
	key := catalogEventKey(eventID)
	var event models.Event
	err := cacheAsideJSON(ctx, s.rdb, &s.cacheSF, key, catalogEventDetailTTL, &event, func(ctx context.Context) (models.Event, error) {
		loaded, err := s.repo.FindEventDetail(ctx, eventID)
		if err != nil {
			if normalized := normalizeTicketNotFound(err); normalized == ErrTicketResourceNotFound {
				return models.Event{}, ErrTicketResourceNotFound
			}
			return models.Event{}, err
		}
		if loaded.Status != models.EventStatusPublished {
			return models.Event{}, ErrTicketResourceNotFound
		}
		// Cache static catalog shape; remaining_quota is overlaid from Redis after load.
		return *loaded, nil
	})
	if err != nil {
		return nil, err
	}
	s.overlayTierRemainingFromRedis(ctx, &event)
	return &event, nil
}

// overlayTierRemainingFromRedis prefers warm Redis stock keys; falls back to MySQL
// bucket sum only when Redis has not been warmed for that tier.
func (s *TicketCatalogService) overlayTierRemainingFromRedis(ctx context.Context, event *models.Event) {
	if event == nil {
		return
	}
	for si := range event.Sessions {
		for ti := range event.Sessions[si].TicketTiers {
			tier := &event.Sessions[si].TicketTiers[ti]
			if stock, ok := s.loadTierStockFromRedis(ctx, tier); ok {
				tier.RemainingQuota = stock
				continue
			}
			if s.inventory.Enabled {
				if sum, err := sumTierBucketRemaining(ctx, s.db, tier.ID); err == nil {
					tier.RemainingQuota = sum
				}
			}
		}
	}
}

func (s *TicketCatalogService) loadTierStockFromRedis(ctx context.Context, tier *models.TicketTier) (int, bool) {
	if s.rdb == nil || tier == nil {
		return 0, false
	}
	if s.inventory.Enabled {
		n := s.inventory.EffectiveBucketCount(tier.TotalQuota)
		keys := make([]string, n)
		for i := 0; i < n; i++ {
			keys[i] = TicketStockBucketKey(tier.ID, i)
		}
		vals, err := s.rdb.MGet(ctx, keys...).Result()
		if err != nil {
			return 0, false
		}
		sum := 0
		seen := 0
		for _, v := range vals {
			if v == nil {
				continue
			}
			seen++
			switch t := v.(type) {
			case string:
				if parsed, parseErr := strconv.Atoi(t); parseErr == nil {
					sum += parsed
				}
			}
		}
		if seen == 0 {
			return 0, false
		}
		return sum, true
	}
	n, err := s.rdb.Get(ctx, ticketStockKey(tier.ID)).Int()
	if err != nil {
		return 0, false
	}
	return n, true
}

// Deprecated path kept for callers that still want MySQL-only overlay.
func (s *TicketCatalogService) overlayTierRemainingFromBuckets(ctx context.Context, event *models.Event) {
	s.overlayTierRemainingFromRedis(ctx, event)
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
	userID, organizerID, eventID, sessionID int64,
) (*OrganizerOverview, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	eventID, sessionID, err := s.resolveOverviewScope(ctx, organizerID, eventID, sessionID)
	if err != nil {
		return nil, err
	}
	const periodDays = 7
	now := time.Now()
	periodFrom, previousFrom, periodTo := rollingCalendarPeriod(now, periodDays)
	overview := &OrganizerOverview{
		PeriodDays: periodDays,
		PeriodFrom: periodFrom,
		EventID:    eventID,
		SessionID:  sessionID,
	}
	onSaleQuery := s.db.WithContext(ctx).Model(&models.Event{}).
		Where("organizer_id = ? AND status = ?", organizerID, models.EventStatusPublished)
	if eventID > 0 {
		onSaleQuery = onSaleQuery.Where("id = ?", eventID)
	}
	if err := onSaleQuery.Count(&overview.OnSaleEvents).Error; err != nil {
		return nil, err
	}
	pendingQuery := applyOverviewOrderScope(
		s.db.WithContext(ctx).Model(&models.TicketOrder{}).
			Where("organizer_id = ? AND status = ?", organizerID, models.TicketOrderStatusPendingPayment),
		"ticket_order", eventID, sessionID,
	)
	if err := pendingQuery.Count(&overview.PendingPaymentOrders).Error; err != nil {
		return nil, err
	}
	refundingQuery := applyOverviewOrderScope(
		s.db.WithContext(ctx).Model(&models.TicketOrder{}).
			Where("organizer_id = ? AND payment_status = ?", organizerID, models.PaymentStatusRefunding),
		"ticket_order", eventID, sessionID,
	)
	if err := refundingQuery.Count(&overview.RefundingOrders).Error; err != nil {
		return nil, err
	}
	upcomingQuery := s.db.WithContext(ctx).Model(&models.EventSession{}).
		Joins("JOIN event ON event.id = event_session.event_id AND event.delete_time IS NULL").
		Where("event.organizer_id = ?", organizerID).
		Where("event_session.starts_at >= ? AND event_session.starts_at < ?", now, now.AddDate(0, 0, periodDays)).
		Where("event_session.status NOT IN ?", []models.SessionStatus{
			models.SessionStatusCancelled, models.SessionStatusFinished,
		})
	upcomingQuery = applyOverviewSessionScope(upcomingQuery, eventID, sessionID)
	if err := upcomingQuery.Count(&overview.UpcomingSessions).Error; err != nil {
		return nil, err
	}

	var inventory struct {
		Total    int64
		Occupied int64
	}
	inventoryQuery := s.db.WithContext(ctx).Model(&models.TicketTier{}).
		Select("COALESCE(SUM(ticket_tier.total_quota), 0) AS total, COALESCE(SUM(ticket_tier.sold_count), 0) AS occupied").
		Joins("JOIN event_session ON event_session.id = ticket_tier.session_id AND event_session.delete_time IS NULL").
		Joins("JOIN event ON event.id = event_session.event_id AND event.delete_time IS NULL").
		Where("event.organizer_id = ? AND event.status = ?", organizerID, models.EventStatusPublished)
	inventoryQuery = applyOverviewInventoryScope(inventoryQuery, eventID, sessionID)
	if err := inventoryQuery.Scan(&inventory).Error; err != nil {
		return nil, err
	}
	overview.InventoryTotal = inventory.Total
	overview.InventoryOccupied = inventory.Occupied
	if inventory.Total > 0 {
		overview.InventoryOccupancyRate = math.Round(float64(inventory.Occupied)/float64(inventory.Total)*1000) / 10
	}

	current, err := s.loadOrganizerPeriodSales(ctx, organizerID, eventID, sessionID, periodFrom, periodTo)
	if err != nil {
		return nil, err
	}
	previous, err := s.loadOrganizerPeriodSales(ctx, organizerID, eventID, sessionID, previousFrom, periodFrom)
	if err != nil {
		return nil, err
	}
	leaks, err := s.loadOrganizerPeriodLeaks(ctx, organizerID, eventID, sessionID, periodFrom, periodTo)
	if err != nil {
		return nil, err
	}
	overview.PaidOrders = current.PaidOrders
	overview.PaidTickets = current.PaidTickets
	overview.GrossRevenueCents = current.GrossRevenueCents
	overview.RefundedOrders = current.RefundedOrders
	overview.RefundedAmountCents = current.RefundedAmountCents
	overview.NetRevenueCents = current.GrossRevenueCents - current.RefundedAmountCents
	overview.PreviousPaidOrders = previous.PaidOrders
	overview.PreviousPaidTickets = previous.PaidTickets
	overview.PreviousGrossRevenueCents = previous.GrossRevenueCents
	overview.PreviousNetRevenueCents = previous.GrossRevenueCents - previous.RefundedAmountCents
	overview.PaymentFailedOrders = leaks.PaymentFailedOrders
	overview.TimeoutCancelledOrders = leaks.TimeoutCancelledOrders
	overview.PaymentSuccessRate = overviewPaymentSuccessRate(current.PaidOrders, leaks.TimeoutCancelledOrders)
	return overview, nil
}

func (s *TicketCatalogService) resolveOverviewScope(
	ctx context.Context,
	organizerID, eventID, sessionID int64,
) (int64, int64, error) {
	if eventID > 0 {
		var n int64
		if err := s.db.WithContext(ctx).Model(&models.Event{}).
			Where("id = ? AND organizer_id = ?", eventID, organizerID).
			Count(&n).Error; err != nil {
			return 0, 0, err
		}
		if n == 0 {
			return 0, 0, ErrTicketResourceNotFound
		}
	}
	if sessionID <= 0 {
		return eventID, 0, nil
	}
	var session models.EventSession
	err := s.db.WithContext(ctx).Model(&models.EventSession{}).
		Select("event_session.id", "event_session.event_id").
		Joins("JOIN event ON event.id = event_session.event_id AND event.delete_time IS NULL").
		Where("event_session.id = ? AND event.organizer_id = ?", sessionID, organizerID).
		First(&session).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, 0, ErrTicketResourceNotFound
		}
		return 0, 0, err
	}
	if eventID > 0 && session.EventID != eventID {
		return 0, 0, ErrTicketResourceNotFound
	}
	return session.EventID, session.ID, nil
}

func rollingCalendarPeriod(now time.Time, days int) (from, previousFrom, to time.Time) {
	if days < 1 {
		days = 7
	}
	from = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -(days - 1))
	previousFrom = from.AddDate(0, 0, -days)
	to = now.Add(time.Second)
	return from, previousFrom, to
}

func applyOverviewOwnerScope(db *gorm.DB, table string, organizerID int64) *gorm.DB {
	if organizerID > 0 {
		return db.Where(table+".organizer_id = ?", organizerID)
	}
	return db
}

func applyOverviewOrderScope(db *gorm.DB, table string, eventID, sessionID int64) *gorm.DB {
	if sessionID > 0 {
		return db.Where(table+".session_id = ?", sessionID)
	}
	if eventID > 0 {
		return db.Where(table+".event_id = ?", eventID)
	}
	return db
}

func applyOverviewSessionScope(db *gorm.DB, eventID, sessionID int64) *gorm.DB {
	if sessionID > 0 {
		return db.Where("event_session.id = ?", sessionID)
	}
	if eventID > 0 {
		return db.Where("event.id = ?", eventID)
	}
	return db
}

func applyOverviewInventoryScope(db *gorm.DB, eventID, sessionID int64) *gorm.DB {
	if sessionID > 0 {
		return db.Where("ticket_tier.session_id = ?", sessionID)
	}
	if eventID > 0 {
		return db.Where("event.id = ?", eventID)
	}
	return db
}

func overviewPaymentSuccessRate(paid, timeoutCancelled int64) float64 {
	resolved := paid + timeoutCancelled
	if resolved <= 0 {
		return 0
	}
	return math.Round(float64(paid)/float64(resolved)*1000) / 10
}

func isTimeoutCancelReason(reason string) bool {
	return strings.TrimSpace(reason) == orderPaymentTimeoutReason
}

// 与 ticket_order_timeout 写入的 cancel_reason 对齐。
const orderPaymentTimeoutReason = "支付超时自动取消"

type organizerPeriodLeaks struct {
	PaymentFailedOrders    int64
	TimeoutCancelledOrders int64
}

func (s *TicketCatalogService) loadOrganizerPeriodSales(
	ctx context.Context,
	organizerID, eventID, sessionID int64,
	from, to time.Time,
) (organizerPeriodSales, error) {
	var out organizerPeriodSales
	paidStatuses := []models.PaymentStatus{
		models.PaymentStatusPaid,
		models.PaymentStatusRefunding,
		models.PaymentStatusRefunded,
	}
	// 历史演示单可能缺 paid_at：已支付状态用 create_time 回退，避免经营卡和订单列表对不上。
	paidAtExpr := "COALESCE(ticket_order.paid_at, ticket_order.create_time)"
	paidQuery := applyOverviewOrderScope(
		applyOverviewOwnerScope(
			s.db.WithContext(ctx).Model(&models.TicketOrder{}).
				Select("COUNT(*) AS paid_orders, COALESCE(SUM(total_amount_cents), 0) AS gross_revenue_cents").
				Where(paidAtExpr+" >= ? AND "+paidAtExpr+" < ? AND payment_status IN ? AND delete_time IS NULL",
					from, to, paidStatuses),
			"ticket_order", organizerID,
		),
		"ticket_order", eventID, sessionID,
	)
	if err := paidQuery.Scan(&out).Error; err != nil {
		return out, err
	}
	ticketQuery := applyOverviewOrderScope(
		applyOverviewOwnerScope(
			s.db.WithContext(ctx).Model(&models.TicketOrderItem{}).
				Select("COALESCE(SUM(ticket_order_item.quantity), 0)").
				Joins("JOIN ticket_order ON ticket_order.id = ticket_order_item.order_id AND ticket_order.delete_time IS NULL").
				Where(paidAtExpr+" >= ? AND "+paidAtExpr+" < ? AND ticket_order.payment_status IN ?",
					from, to, paidStatuses),
			"ticket_order", organizerID,
		),
		"ticket_order", eventID, sessionID,
	)
	if err := ticketQuery.Scan(&out.PaidTickets).Error; err != nil {
		return out, err
	}
	var refunds struct {
		RefundedOrders      int64
		RefundedAmountCents int64
	}
	refundQuery := applyOverviewOrderScope(
		applyOverviewOwnerScope(
			s.db.WithContext(ctx).Model(&models.PaymentTransaction{}).
				Select("COUNT(DISTINCT payment_transaction.order_id) AS refunded_orders, COALESCE(SUM(payment_transaction.amount_cents), 0) AS refunded_amount_cents").
				Joins("JOIN ticket_order ON ticket_order.id = payment_transaction.order_id AND ticket_order.delete_time IS NULL").
				Where("payment_transaction.status = ? AND payment_transaction.refunded_at >= ? AND payment_transaction.refunded_at < ? AND payment_transaction.delete_time IS NULL",
					models.PaymentTransactionRefunded, from, to),
			"ticket_order", organizerID,
		),
		"ticket_order", eventID, sessionID,
	)
	if err := refundQuery.Scan(&refunds).Error; err != nil {
		return out, err
	}
	out.RefundedOrders = refunds.RefundedOrders
	out.RefundedAmountCents = refunds.RefundedAmountCents
	return out, nil
}

func (s *TicketCatalogService) loadOrganizerPeriodLeaks(
	ctx context.Context,
	organizerID, eventID, sessionID int64,
	from, to time.Time,
) (organizerPeriodLeaks, error) {
	var out organizerPeriodLeaks
	failedAtExpr := "COALESCE(payment_transaction.update_time, payment_transaction.create_time)"
	failedQuery := applyOverviewOrderScope(
		applyOverviewOwnerScope(
			s.db.WithContext(ctx).Model(&models.PaymentTransaction{}).
				Joins("JOIN ticket_order ON ticket_order.id = payment_transaction.order_id AND ticket_order.delete_time IS NULL").
				Where("payment_transaction.order_id > 0").
				Where("payment_transaction.status = ? AND "+failedAtExpr+" >= ? AND "+failedAtExpr+" < ?",
					models.PaymentTransactionFailed, from, to).
				Where("payment_transaction.delete_time IS NULL"),
			"ticket_order", organizerID,
		),
		"ticket_order", eventID, sessionID,
	)
	if err := failedQuery.Distinct("payment_transaction.order_id").Count(&out.PaymentFailedOrders).Error; err != nil {
		return out, err
	}

	cancelledAtExpr := "COALESCE(ticket_order.cancelled_at, ticket_order.create_time)"
	timeoutQuery := applyOverviewOrderScope(
		applyOverviewOwnerScope(
			s.db.WithContext(ctx).Model(&models.TicketOrder{}).
				Where("ticket_order.status = ? AND ticket_order.cancel_reason = ?",
					models.TicketOrderStatusCancelled, orderPaymentTimeoutReason).
				Where(cancelledAtExpr+" >= ? AND "+cancelledAtExpr+" < ? AND ticket_order.delete_time IS NULL", from, to),
			"ticket_order", organizerID,
		),
		"ticket_order", eventID, sessionID,
	)
	if err := timeoutQuery.Count(&out.TimeoutCancelledOrders).Error; err != nil {
		return out, err
	}
	return out, nil
}

func (s *TicketCatalogService) ListOrganizerOrders(
	ctx context.Context,
	userID, organizerID int64,
	page, pageSize int,
	filter OrderListFilter,
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
	query := filter.Apply(s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("organizer_id = ?", organizerID))
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
