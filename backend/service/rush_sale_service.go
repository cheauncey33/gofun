package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/metrics"
	"WHU_Snack_GO/models"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const rushSaleKeyPrefix = "fuchang:rush:"

var executeRushSaleScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return -3
end
local rushStock = tonumber(redis.call('GET', KEYS[2]))
local ticketStock = tonumber(redis.call('GET', KEYS[3]))
if rushStock == nil or ticketStock == nil then
  return -2
end
local quantity = tonumber(ARGV[2])
local limit = tonumber(ARGV[3])
local bought = tonumber(redis.call('GET', KEYS[4]) or '0')
if bought + quantity > limit then
  return -4
end
if rushStock < quantity or ticketStock < quantity then
  return -1
end
redis.call('DEL', KEYS[1])
redis.call('DECRBY', KEYS[2], quantity)
redis.call('DECRBY', KEYS[3], quantity)
redis.call('INCRBY', KEYS[4], quantity)
redis.call('EXPIRE', KEYS[4], tonumber(ARGV[4]))
return rushStock - quantity
`)

type CreateRushSaleInput struct {
	TicketTierID   int64     `json:"ticket_tier_id,string" binding:"required"`
	Name           string    `json:"name" binding:"required"`
	RushPriceCents int64     `json:"rush_price_cents" binding:"required"`
	TotalQuota     int       `json:"total_quota" binding:"required"`
	PerUserLimit   int       `json:"per_user_limit" binding:"required"`
	StartsAt       time.Time `json:"starts_at" binding:"required"`
	EndsAt         time.Time `json:"ends_at" binding:"required"`
}

type RushSaleService struct {
	db      *gorm.DB
	rdb     *redis.Client
	catalog *TicketCatalogService
	order   *TicketOrderService
}

func NewRushSaleService(
	c *container.Container,
	catalog *TicketCatalogService,
	order *TicketOrderService,
) *RushSaleService {
	return &RushSaleService{db: c.DB, rdb: c.RDB, catalog: catalog, order: order}
}

func (s *RushSaleService) CreateCampaign(
	ctx context.Context,
	userID, organizerID int64,
	input CreateRushSaleInput,
) (*models.RushSaleCampaign, error) {
	if err := s.catalog.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	tier, err := s.catalog.repo.FindTicketTierByID(ctx, input.TicketTierID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	session, err := s.catalog.repo.FindSessionByID(ctx, tier.SessionID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	event, err := s.catalog.repo.FindEventByID(ctx, session.EventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	input.Name = strings.TrimSpace(input.Name)
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if input.Name == "" || input.RushPriceCents <= 0 ||
		input.RushPriceCents >= tier.PriceCents ||
		input.TotalQuota <= 0 || input.TotalQuota > tier.RemainingQuota ||
		input.PerUserLimit <= 0 || input.PerUserLimit > tier.PurchaseLimit ||
		!input.StartsAt.Before(input.EndsAt) || input.EndsAt.Before(time.Now()) {
		return nil, fmt.Errorf("%w: 限时开售参数错误", ErrInvalidTicketCatalog)
	}
	campaign := &models.RushSaleCampaign{
		OrganizerID:    organizerID,
		TicketTierID:   tier.ID,
		Name:           input.Name,
		RushPriceCents: input.RushPriceCents,
		TotalQuota:     input.TotalQuota,
		RemainingQuota: input.TotalQuota,
		PerUserLimit:   input.PerUserLimit,
		StartsAt:       input.StartsAt,
		EndsAt:         input.EndsAt,
		Status:         models.RushSaleStatusScheduled,
	}
	if err := s.db.WithContext(ctx).Create(campaign).Error; err != nil {
		return nil, err
	}
	if err := s.rdb.Set(
		ctx, rushStockKey(campaign.ID), campaign.RemainingQuota,
		time.Until(campaign.EndsAt)+time.Hour,
	).Err(); err != nil {
		return nil, err
	}
	return campaign, nil
}

type RushSaleCampaignView struct {
	models.RushSaleCampaign
	EventID          int64  `json:"event_id,string"`
	EventTitle       string `json:"event_title"`
	RealNameRequired bool   `json:"real_name_required"`
}

func (s *RushSaleService) ListCampaigns(
	ctx context.Context,
) ([]RushSaleCampaignView, error) {
	var campaigns []models.RushSaleCampaign
	err := s.db.WithContext(ctx).
		Where("status IN ? AND ends_at > ?", []models.RushSaleStatus{
			models.RushSaleStatusScheduled,
			models.RushSaleStatusActive,
		}, time.Now()).
		Order("starts_at ASC").Find(&campaigns).Error
	if err != nil {
		return nil, err
	}
	views := make([]RushSaleCampaignView, 0, len(campaigns))
	for _, campaign := range campaigns {
		view := RushSaleCampaignView{RushSaleCampaign: campaign}
		tier, err := s.catalog.repo.FindTicketTierByID(ctx, campaign.TicketTierID)
		if err == nil {
			session, sessionErr := s.catalog.repo.FindSessionByID(ctx, tier.SessionID)
			if sessionErr == nil {
				event, eventErr := s.catalog.repo.FindEventByID(ctx, session.EventID)
				if eventErr == nil {
					view.EventID = event.ID
					view.EventTitle = event.Title
					view.RealNameRequired = event.RealNameRequired
				}
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *RushSaleService) IssueToken(
	ctx context.Context,
	userID, campaignID int64,
) (string, error) {
	campaign, err := s.getAvailableCampaign(ctx, campaignID)
	if err != nil {
		return "", err
	}
	if time.Now().Before(campaign.StartsAt) {
		return "", fmt.Errorf("%w: 限时开售尚未开始", ErrTicketOrderUnavailable)
	}
	tokenBytes := make([]byte, 24)
	if _, err := rand.Read(tokenBytes); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tokenBytes)
	if err := s.rdb.Set(
		ctx, rushTokenKey(campaignID, userID), token, 60*time.Second,
	).Err(); err != nil {
		return "", err
	}
	return token, nil
}

func (s *RushSaleService) Execute(
	ctx context.Context,
	userID, campaignID int64,
	token, idempotencyKey, requestID string,
	quantity int,
	purchase PurchaseInfoInput,
) (*TicketOrderReceipt, error) {
	campaign, err := s.getAvailableCampaign(ctx, campaignID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if now.Before(campaign.StartsAt) || now.After(campaign.EndsAt) {
		return nil, ErrTicketOrderUnavailable
	}
	if quantity <= 0 || quantity > campaign.PerUserLimit {
		return nil, fmt.Errorf("%w: 超过限购数量", ErrTicketOrderUnavailable)
	}
	var existing models.TicketOrder
	err = s.db.WithContext(ctx).
		Where("user_id = ? AND idempotency_key = ?", userID, strings.TrimSpace(idempotencyKey)).
		First(&existing).Error
	if err == nil {
		return ticketOrderReceipt(&existing), nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	ttl := int64(time.Until(campaign.EndsAt).Seconds()) + 3600
	result, err := executeRushSaleScript.Run(ctx, s.rdb, []string{
		rushTokenKey(campaignID, userID),
		rushStockKey(campaignID),
		ticketStockKey(campaign.TicketTierID),
		rushUserCountKey(campaignID, userID),
	}, strings.TrimSpace(token), quantity, campaign.PerUserLimit, ttl).Int64()
	if err != nil {
		return nil, err
	}
	switch result {
	case -4:
		return nil, fmt.Errorf("%w: 已达到限购数量", ErrTicketOrderUnavailable)
	case -3:
		return nil, fmt.Errorf("%w: 限时开售令牌无效", ErrTicketOrderUnavailable)
	case -2:
		return nil, fmt.Errorf("%w: 票额缓存尚未预热", ErrTicketOrderUnavailable)
	case -1:
		return nil, ErrTicketQuotaInsufficient
	}

	receipt, rollback, err := s.order.createRushOrderAfterReservation(
		ctx, userID, idempotencyKey, requestID, campaign, quantity, purchase,
	)
	if rollback {
		s.rollbackRushReservation(ctx, campaign, userID, quantity)
	}
	return receipt, err
}

func (s *RushSaleService) getAvailableCampaign(
	ctx context.Context,
	campaignID int64,
) (*models.RushSaleCampaign, error) {
	var campaign models.RushSaleCampaign
	err := s.db.WithContext(ctx).First(&campaign, campaignID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTicketResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	if campaign.Status != models.RushSaleStatusScheduled &&
		campaign.Status != models.RushSaleStatusActive {
		return nil, ErrTicketOrderUnavailable
	}
	return &campaign, nil
}

func (s *RushSaleService) rollbackRushReservation(
	ctx context.Context,
	campaign *models.RushSaleCampaign,
	userID int64,
	quantity int,
) {
	pipe := s.rdb.TxPipeline()
	pipe.IncrBy(ctx, rushStockKey(campaign.ID), int64(quantity))
	pipe.IncrBy(ctx, ticketStockKey(campaign.TicketTierID), int64(quantity))
	pipe.DecrBy(ctx, rushUserCountKey(campaign.ID, userID), int64(quantity))
	_, _ = pipe.Exec(ctx)
}

// createRushOrderAfterReservation 在 Redis 已预扣后创建订单。
// 返回的 rollback=true 表示调用方必须归还本次 Lua 预扣（含幂等命中已有订单的情况）。
func (s *TicketOrderService) createRushOrderAfterReservation(
	ctx context.Context,
	userID int64,
	idempotencyKey, requestID string,
	campaign *models.RushSaleCampaign,
	quantity int,
	purchase PurchaseInfoInput,
) (receipt *TicketOrderReceipt, rollback bool, err error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 64 {
		return nil, true, ErrInvalidTicketCatalog
	}
	var existing models.TicketOrder
	err = s.db.WithContext(ctx).
		Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
		First(&existing).Error
	if err == nil {
		// Lua 已扣库存，但订单已由并发请求创建：归还本次多余预扣，返回已有凭据。
		return ticketOrderReceipt(&existing), true, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, true, err
	}
	tier, session, event, venue, err := s.loadPurchasableTier(ctx, campaign.TicketTierID)
	if err != nil {
		return nil, true, err
	}
	if quantity > tier.PurchaseLimit || quantity > event.MaxTicketsPerOrder {
		return nil, true, fmt.Errorf("%w: 超过限购数量", ErrTicketOrderUnavailable)
	}
	if err := validatePurchaseInfo(purchase, quantity, event.RealNameRequired); err != nil {
		return nil, true, err
	}
	orderID := s.node.Generate().Int64()
	acceptedAt := time.Now()
	order := &models.TicketOrder{
		Base:                  models.Base{ID: orderID},
		OrderNo:               "FC" + strconv.FormatInt(orderID, 10),
		UserID:                userID,
		OrganizerID:           event.OrganizerID,
		EventID:               event.ID,
		SessionID:             session.ID,
		RushSaleCampaignID:    &campaign.ID,
		OrderSource:           models.TicketOrderSourceRushSale,
		Status:                models.TicketOrderStatusQueued,
		PaymentStatus:         models.PaymentStatusUnpaid,
		TotalAmountCents:      campaign.RushPriceCents * int64(quantity),
		ContactName:           strings.TrimSpace(purchase.ContactName),
		ContactPhone:          strings.TrimSpace(purchase.ContactPhone),
		RealNameRequired:      event.RealNameRequired,
		PurchaseNoticeVersion: purchaseNoticeVersion,
		TermsAcceptedAt:       &acceptedAt,
		IdempotencyKey:        idempotencyKey,
		RequestID:             requestID,
		// 支付窗口在消费者转入 pending_payment 时写入，避免排队耗尽超时。
		ExpiresAt: time.Now().Add(s.paymentTimeout),
		Items: []models.TicketOrderItem{{
			TicketTierID:            tier.ID,
			Quantity:                quantity,
			UnitPriceCents:          campaign.RushPriceCents,
			EventTitleSnapshot:      event.Title,
			SessionStartsAtSnapshot: session.StartsAt,
			VenueNameSnapshot:       venue.Name,
			VenueAddressSnapshot:    venue.Address,
			TierNameSnapshot:        tier.Name,
		}},
		Attendees: s.buildAttendeeSnapshots(orderID, purchase.Attendees, event.RealNameRequired),
	}
	if err := s.db.WithContext(ctx).Create(order).Error; err != nil {
		if lookupErr := s.db.WithContext(ctx).
			Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
			First(&existing).Error; lookupErr == nil {
			return ticketOrderReceipt(&existing), true, nil
		}
		return nil, true, err
	}
	message := TicketOrderMessage{
		OrderID:            orderID,
		UserID:             userID,
		TicketTierID:       tier.ID,
		Quantity:           quantity,
		RushSaleCampaignID: &campaign.ID,
	}
	if err := s.enqueueOutbox(ctx, orderID, message); err != nil {
		_ = s.markQueuedOrderFailed(ctx, orderID, "订单 outbox 写入失败")
		return nil, true, err
	}
	metrics.OrdersCreated.Inc()
	return ticketOrderReceipt(order), false, nil
}

func rushStockKey(campaignID int64) string {
	return rushSaleKeyPrefix + "stock:" + strconv.FormatInt(campaignID, 10)
}

func rushTokenKey(campaignID, userID int64) string {
	return rushSaleKeyPrefix + "token:" +
		strconv.FormatInt(campaignID, 10) + ":" + strconv.FormatInt(userID, 10)
}

func rushUserCountKey(campaignID, userID int64) string {
	return rushSaleKeyPrefix + "user-count:" +
		strconv.FormatInt(campaignID, 10) + ":" + strconv.FormatInt(userID, 10)
}
