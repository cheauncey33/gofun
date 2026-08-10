package service

import (
	"context"
	"errors"
	"fmt"
	"gofun/container"
	"gofun/metrics"
	"gofun/models"
	"strconv"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"
)

const rushSaleKeyPrefix = "fuchang:rush:"

// 读侧本地缓存极短 TTL：挡住开售瞬间对同一 Redis stock key 的读风暴（hotkey），
// 允许展示短暂滞后；真正扣减仍只走 Redis Lua。
const rushStockLocalTTL = 300 * time.Millisecond

// 秒杀直抢：无候场、无前置 token。闸门靠登录 + 写限流 + 幂等键 + Lua 原子扣减/限购。
var executeRushSaleScript = redis.NewScript(`
local rushStock = tonumber(redis.call('GET', KEYS[1]))
local ticketStock = tonumber(redis.call('GET', KEYS[2]))
if rushStock == nil or ticketStock == nil then
  return -2
end
local quantity = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local bought = tonumber(redis.call('GET', KEYS[3]) or '0')
if bought + quantity > limit then
  return -4
end
if rushStock < quantity or ticketStock < quantity then
  return -1
end
redis.call('DECRBY', KEYS[1], quantity)
redis.call('DECRBY', KEYS[2], quantity)
redis.call('INCRBY', KEYS[3], quantity)
redis.call('EXPIRE', KEYS[3], tonumber(ARGV[3]))
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
	db               *gorm.DB
	rdb              *redis.Client
	localCache       *gocache.Cache
	catalog          *TicketCatalogService
	order            *TicketOrderService
	stockSF          singleflight.Group
	campaignCacheTTL time.Duration
}

func NewRushSaleService(
	c *container.Container,
	catalog *TicketCatalogService,
	order *TicketOrderService,
	campaignCacheTTL time.Duration,
) *RushSaleService {
	return &RushSaleService{
		db:               c.DB,
		rdb:              c.RDB,
		localCache:       c.LocalCache,
		catalog:          catalog,
		order:            order,
		campaignCacheTTL: campaignCacheTTL,
	}
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
	ttl := time.Until(campaign.EndsAt) + time.Hour
	if s.order.inventory.ShardEnabled {
		// 活动元数据先落主库，库存桶随后落独立库存库；批处理账本负责跨库重试幂等。
		err = s.db.WithContext(ctx).Create(campaign).Error
		if err == nil {
			err = EnsureRushBuckets(s.order.inventoryDatabase().WithContext(ctx), campaign, s.order.inventory)
		}
	} else {
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(campaign).Error; err != nil {
				return err
			}
			return EnsureRushBuckets(tx, campaign, s.order.inventory)
		})
	}
	if err != nil {
		return nil, err
	}
	if s.order.inventory.Enabled {
		var buckets []models.RushCampaignBucket
		if err := s.order.inventoryDatabase().WithContext(ctx).
			Where("campaign_id = ?", campaign.ID).Find(&buckets).Error; err != nil {
			return nil, err
		}
		pipe := s.rdb.Pipeline()
		for _, bucket := range buckets {
			pipe.Set(ctx, RushStockBucketKey(bucket.CampaignID, bucket.BucketNo), bucket.RemainingQuota, ttl)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			return nil, err
		}
	} else if err := s.rdb.Set(
		ctx, rushStockKey(campaign.ID), campaign.RemainingQuota, ttl,
	).Err(); err != nil {
		return nil, err
	}
	s.setRushStockLocal(campaign.ID, int64(campaign.RemainingQuota))
	if s.localCache != nil && s.campaignCacheTTL > 0 {
		s.localCache.Set(rushCampaignLocalKey(campaign.ID), *campaign, s.campaignCacheTTL)
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
		// 展示余量优先读本地短缓存 → Redis（分桶时 SUM），减轻开售热 key 读压力。
		if stock, ok, err := s.loadRushStock(ctx, campaign.ID, campaign.TotalQuota); err == nil && ok {
			view.RemainingQuota = int(stock)
		} else if s.order.inventory.Enabled {
			if sum, sumErr := sumRushBucketRemaining(ctx, s.order.inventoryDatabase(), campaign.ID); sumErr == nil {
				view.RemainingQuota = sum
			}
		}
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

func (s *RushSaleService) Execute(
	ctx context.Context,
	userID, campaignID int64,
	idempotencyKey, requestID string,
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
	if receipt, err := s.order.lookupIdempotentOrder(ctx, userID, idempotencyKey); err != nil {
		return nil, err
	} else if receipt != nil {
		return receipt, nil
	}

	ttl := int64(time.Until(campaign.EndsAt).Seconds()) + 3600
	bucketNo, result, err := s.reserveRushStock(ctx, userID, campaign, quantity, ttl)
	if err != nil {
		return nil, err
	}
	switch result {
	case -4:
		return nil, fmt.Errorf("%w: 已达到限购数量", ErrTicketOrderUnavailable)
	case -2:
		return nil, fmt.Errorf("%w: 票额缓存尚未预热", ErrTicketOrderUnavailable)
	case -1:
		s.setRushStockLocal(campaignID, 0)
		return nil, ErrTicketQuotaInsufficient
	}

	// Lua 返回扣减后该桶余量；列表展示用 SUM，本地短缓存仅作热点挡板。
	if !s.order.inventory.Enabled {
		s.setRushStockLocal(campaignID, result)
	} else {
		s.invalidateRushStockLocal(campaignID)
	}

	var bucketPtr *int
	if s.order.inventory.Enabled {
		bucketPtr = intPtr(bucketNo)
	}
	receipt, rollback, err := s.order.createRushOrderAfterReservation(
		ctx, userID, idempotencyKey, requestID, campaign, quantity, purchase, bucketPtr,
	)
	if rollback {
		s.rollbackRushReservation(ctx, campaign, userID, quantity, bucketPtr)
	}
	return receipt, err
}

func (s *RushSaleService) reserveRushStock(
	ctx context.Context,
	userID int64,
	campaign *models.RushSaleCampaign,
	quantity int,
	ttl int64,
) (bucketNo int, result int64, err error) {
	if !s.order.inventory.Enabled {
		result, err = executeRushSaleScript.Run(ctx, s.rdb, []string{
			rushStockKey(campaign.ID),
			ticketStockKey(campaign.TicketTierID),
			rushUserCountKey(campaign.ID, userID),
		}, quantity, campaign.PerUserLimit, ttl).Int64()
		return 0, result, err
	}
	n := s.order.inventory.EffectiveBucketCount(campaign.TotalQuota)
	maxAttempts := 1 + s.order.inventory.BucketRetry
	if maxAttempts > n {
		maxAttempts = n
	}
	var last int64 = -1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		b := SelectBucketNo(userID, n, attempt)
		rem, runErr := executeRushSaleScript.Run(ctx, s.rdb, []string{
			RushStockBucketKey(campaign.ID, b),
			TicketStockBucketKey(campaign.TicketTierID, b),
			rushUserCountKey(campaign.ID, userID),
		}, quantity, campaign.PerUserLimit, ttl).Int64()
		if runErr != nil {
			return 0, 0, runErr
		}
		if rem >= 0 {
			return b, rem, nil
		}
		last = rem
		if rem == -4 {
			// 限购全局，换桶无意义
			return b, rem, nil
		}
	}
	return 0, last, nil
}

func (s *RushSaleService) getAvailableCampaign(
	ctx context.Context,
	campaignID int64,
) (*models.RushSaleCampaign, error) {
	cacheKey := rushCampaignLocalKey(campaignID)
	if s.localCache != nil && s.campaignCacheTTL > 0 {
		if cached, found := s.localCache.Get(cacheKey); found {
			campaign := cached.(models.RushSaleCampaign)
			return validateAvailableCampaign(&campaign)
		}
	}
	value, err, _ := s.stockSF.Do(cacheKey, func() (interface{}, error) {
		if s.localCache != nil && s.campaignCacheTTL > 0 {
			if cached, found := s.localCache.Get(cacheKey); found {
				return cached.(models.RushSaleCampaign), nil
			}
		}
		var campaign models.RushSaleCampaign
		if err := s.db.WithContext(ctx).First(&campaign, campaignID).Error; err != nil {
			return models.RushSaleCampaign{}, err
		}
		if s.localCache != nil && s.campaignCacheTTL > 0 {
			s.localCache.Set(cacheKey, campaign, s.campaignCacheTTL)
		}
		return campaign, nil
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTicketResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	campaign := value.(models.RushSaleCampaign)
	return validateAvailableCampaign(&campaign)
}

func validateAvailableCampaign(campaign *models.RushSaleCampaign) (*models.RushSaleCampaign, error) {
	if campaign.Status != models.RushSaleStatusScheduled &&
		campaign.Status != models.RushSaleStatusActive {
		return nil, ErrTicketOrderUnavailable
	}
	return campaign, nil
}

func (s *RushSaleService) rollbackRushReservation(
	ctx context.Context,
	campaign *models.RushSaleCampaign,
	userID int64,
	quantity int,
	bucketNo *int,
) {
	pipe := s.rdb.TxPipeline()
	if s.order.inventory.Enabled && bucketNo != nil {
		pipe.IncrBy(ctx, RushStockBucketKey(campaign.ID, *bucketNo), int64(quantity))
		pipe.IncrBy(ctx, TicketStockBucketKey(campaign.TicketTierID, *bucketNo), int64(quantity))
	} else {
		pipe.IncrBy(ctx, rushStockKey(campaign.ID), int64(quantity))
		pipe.IncrBy(ctx, ticketStockKey(campaign.TicketTierID), int64(quantity))
	}
	pipe.DecrBy(ctx, rushUserCountKey(campaign.ID, userID), int64(quantity))
	_, _ = pipe.Exec(ctx)
	s.invalidateRushStockLocal(campaign.ID)
}

// loadRushStock 读路径：local(短 TTL) → singleflight → Redis（分桶 SUM）。
// ok=false 表示 Redis 尚无预热 key，调用方应回退 MySQL 字段。
func (s *RushSaleService) loadRushStock(
	ctx context.Context,
	campaignID int64,
	totalQuota int,
) (stock int64, ok bool, err error) {
	localKey := rushStockLocalKey(campaignID)
	if s.localCache != nil {
		if v, found := s.localCache.Get(localKey); found {
			return v.(int64), true, nil
		}
	}

	v, err, _ := s.stockSF.Do(localKey, func() (interface{}, error) {
		if s.localCache != nil {
			if cached, found := s.localCache.Get(localKey); found {
				return cached.(int64), nil
			}
		}
		if s.order.inventory.Enabled {
			n := s.order.inventory.EffectiveBucketCount(totalQuota)
			keys := make([]string, n)
			for i := 0; i < n; i++ {
				keys[i] = RushStockBucketKey(campaignID, i)
			}
			vals, err := s.rdb.MGet(ctx, keys...).Result()
			if err != nil {
				return int64(0), err
			}
			var sum int64
			seen := 0
			for _, v := range vals {
				if v == nil {
					continue
				}
				seen++
				switch t := v.(type) {
				case string:
					parsed, parseErr := strconv.ParseInt(t, 10, 64)
					if parseErr == nil {
						sum += parsed
					}
				}
			}
			if seen == 0 {
				return int64(-1), nil
			}
			s.setRushStockLocal(campaignID, sum)
			return sum, nil
		}
		n, err := s.rdb.Get(ctx, rushStockKey(campaignID)).Int64()
		if err == redis.Nil {
			return int64(-1), nil
		}
		if err != nil {
			return int64(0), err
		}
		s.setRushStockLocal(campaignID, n)
		return n, nil
	})
	if err != nil {
		return 0, false, err
	}
	n := v.(int64)
	if n < 0 {
		return 0, false, nil
	}
	return n, true, nil
}

func (s *RushSaleService) setRushStockLocal(campaignID int64, stock int64) {
	if s.localCache == nil {
		return
	}
	s.localCache.Set(rushStockLocalKey(campaignID), stock, rushStockLocalTTL)
}

func (s *RushSaleService) invalidateRushStockLocal(campaignID int64) {
	if s.localCache == nil {
		return
	}
	s.localCache.Delete(rushStockLocalKey(campaignID))
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
	bucketNo *int,
) (receipt *TicketOrderReceipt, rollback bool, err error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 64 {
		return nil, true, ErrInvalidTicketCatalog
	}
	// 入口已做过幂等短路；此处依赖唯一索引处理并发双写，避免再查一次 MySQL。
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
		Base:                   models.Base{ID: orderID},
		OrderNo:                "FC" + strconv.FormatInt(orderID, 10),
		UserID:                 userID,
		OrganizerID:            event.OrganizerID,
		EventID:                event.ID,
		SessionID:              session.ID,
		RushSaleCampaignID:     &campaign.ID,
		OrderSource:            models.TicketOrderSourceRushSale,
		Status:                 models.TicketOrderStatusQueued,
		PaymentStatus:          models.PaymentStatusUnpaid,
		TotalAmountCents:       campaign.RushPriceCents * int64(quantity),
		ContactName:            strings.TrimSpace(purchase.ContactName),
		ContactPhone:           strings.TrimSpace(purchase.ContactPhone),
		RealNameRequired:       event.RealNameRequired,
		PurchaseNoticeVersion:  purchaseNoticeVersion,
		TermsAcceptedAt:        &acceptedAt,
		IdempotencyKey:         idempotencyKey,
		RequestID:              requestID,
		ExpiresAt:              time.Now().Add(s.paymentTimeout),
		InventoryNextAttemptAt: acceptedAt,
		StockBucketNo:          bucketNo,
		RushBucketNo:           bucketNo,
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
	}
	if attendees := s.buildAttendeeSnapshots(orderID, purchase.Attendees, event.RealNameRequired); len(attendees) > 0 {
		order.Attendees = attendees
	}
	message := TicketOrderMessage{
		OrderID:            orderID,
		UserID:             userID,
		TicketTierID:       tier.ID,
		Quantity:           quantity,
		RushSaleCampaignID: &campaign.ID,
		StockBucketNo:      bucketNo,
		RushBucketNo:       bucketNo,
	}
	if err := s.createOrderAndOutbox(ctx, order, message); err != nil {
		var existing models.TicketOrder
		if lookupErr := s.db.WithContext(ctx).
			Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
			First(&existing).Error; lookupErr == nil {
			got := ticketOrderReceipt(&existing)
			s.rememberIdempotentOrder(ctx, userID, idempotencyKey, got)
			return got, true, nil
		}
		return nil, true, err
	}
	s.notifyOutboxPublisher()
	got := ticketOrderReceipt(order)
	s.rememberIdempotentOrder(ctx, userID, idempotencyKey, got)
	metrics.OrdersCreated.Inc()
	return got, false, nil
}

func rushStockKey(campaignID int64) string {
	return rushSaleKeyPrefix + "stock:" + strconv.FormatInt(campaignID, 10)
}

func rushStockLocalKey(campaignID int64) string {
	return "local:rush:stock:" + strconv.FormatInt(campaignID, 10)
}

func rushCampaignLocalKey(campaignID int64) string {
	return "local:rush:campaign:" + strconv.FormatInt(campaignID, 10)
}

func rushUserCountKey(campaignID, userID int64) string {
	return rushSaleKeyPrefix + "user-count:" +
		strconv.FormatInt(campaignID, 10) + ":" + strconv.FormatInt(userID, 10)
}
