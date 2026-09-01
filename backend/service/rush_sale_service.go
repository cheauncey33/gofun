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
	listSF           singleflight.Group
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
	if event.SaleMode.IsSeated() {
		return nil, fmt.Errorf("%w: 选座活动不支持限时开售", ErrInvalidTicketCatalog)
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
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(campaign).Error; err != nil {
			return err
		}
		return EnsureRushBuckets(tx, campaign, s.order.inventory)
	})
	if err != nil {
		return nil, err
	}
	if s.order.inventory.Enabled {
		var buckets []models.RushCampaignBucket
		if err := s.db.WithContext(ctx).
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
	s.invalidateRushSalesListCache(ctx)
	return campaign, nil
}

type RushSaleCampaignView struct {
	models.RushSaleCampaign
	EventID          int64  `json:"event_id,string"`
	EventTitle       string `json:"event_title"`
	CoverURL         string `json:"cover_url"`
	RealNameRequired bool   `json:"real_name_required"`
}

func (s *RushSaleService) ListCampaigns(
	ctx context.Context,
) ([]RushSaleCampaignView, error) {
	var views []RushSaleCampaignView
	err := cacheAsideJSON(ctx, s.rdb, &s.listSF, catalogRushSalesListKey, catalogRushSalesListTTL, &views, func(ctx context.Context) ([]RushSaleCampaignView, error) {
		return s.loadRushSalesListMeta(ctx)
	})
	if err != nil {
		return nil, err
	}
	// Always overlay remaining from Redis stock (local + singleflight); never trust
	// cached RemainingQuota as the live display number.
	for i := range views {
		if stock, ok, stockErr := s.loadRushStock(ctx, views[i].ID, views[i].TotalQuota); stockErr == nil && ok {
			views[i].RemainingQuota = int(stock)
		}
	}
	return views, nil
}

// loadRushSalesListMeta builds the list shape from MySQL (campaign + event title).
// RemainingQuota is filled with Redis/MySQL fallback only for the cached snapshot;
// callers must overlay Redis again on every request.
func (s *RushSaleService) loadRushSalesListMeta(ctx context.Context) ([]RushSaleCampaignView, error) {
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
					view.CoverURL = event.CoverURL
					view.RealNameRequired = event.RealNameRequired
				}
			}
		}
		// Prefer Redis for snapshot remaining; avoid MySQL bucket SUM on the hot path.
		if stock, ok, stockErr := s.loadRushStock(ctx, campaign.ID, campaign.TotalQuota); stockErr == nil && ok {
			view.RemainingQuota = int(stock)
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
	if _, _, event, _, err := s.order.loadPurchasableTier(ctx, campaign.TicketTierID); err != nil {
		return nil, err
	} else if event.SaleMode.IsSeated() {
		return nil, fmt.Errorf("%w: 选座购票尚未开放", ErrTicketOrderUnavailable)
	}
	requestHash := orderRequestHash(ticketOrderOperationRush, struct {
		CampaignID int64             `json:"campaign_id"`
		Quantity   int               `json:"quantity"`
		Purchase   PurchaseInfoInput `json:"purchase"`
	}{campaignID, quantity, purchase})
	if receipt, err := s.order.lookupIdempotentOrder(ctx, userID, idempotencyKey, requestHash); err != nil {
		return nil, err
	} else if receipt != nil {
		return receipt, nil
	}

	ttl := int64(time.Until(campaign.EndsAt).Seconds()) + 3600
	proposedOrderID := s.order.node.Generate().Int64()
	reservation, reserveCode, err := s.reserveRushStock(
		ctx, userID, campaign, quantity, ttl, idempotencyKey, proposedOrderID, requestHash,
	)
	if err != nil {
		return nil, err
	}
	switch reserveCode {
	case -4:
		return nil, fmt.Errorf("%w: 已达到限购数量", ErrTicketOrderUnavailable)
	case -2:
		return nil, fmt.Errorf("%w: 暂时无法购票，请稍后重试", ErrTicketOrderUnavailable)
	case -1:
		s.setRushStockLocal(campaignID, 0)
		return nil, ErrTicketQuotaInsufficient
	case stockReservationCodeConflict:
		return nil, fmt.Errorf("%w: 幂等键已用于其他购票请求", ErrInvalidTicketCatalog)
	}

	// Lua 返回扣减后该桶余量；列表展示用 SUM，本地短缓存仅作热点挡板。
	if !s.order.inventory.Enabled {
		s.setRushStockLocal(campaignID, reservation.Remaining)
	} else {
		s.invalidateRushStockLocal(campaignID)
	}

	bucketNo := reservation.BucketNo
	if err := s.order.injectTicketFault(ctx, FaultAfterRedisReserve, reservation.OrderID); err != nil {
		return nil, err
	}
	var bucketPtr *int
	if s.order.inventory.Enabled {
		bucketPtr = intPtr(bucketNo)
	}
	receipt, err := s.order.createRushOrderAfterReservation(
		ctx, reservation.OrderID, userID, idempotencyKey, requestHash, requestID,
		campaign, quantity, purchase, bucketPtr,
	)
	if err == nil {
		if faultErr := s.order.injectTicketFault(ctx, FaultAfterOrderCommit, reservation.OrderID); faultErr != nil {
			return nil, faultErr
		}
		s.order.confirmStockReservation(ctx, reservation)
		return receipt, nil
	}

	// COMMIT 结果可能因连接中断而未知；先查幂等订单，再决定是否归还预扣。
	recoveryCtx, cancel := detachedReservationContext(ctx)
	defer cancel()
	var existing models.TicketOrder
	lookupErr := s.order.db.WithContext(recoveryCtx).
		Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
		First(&existing).Error
	if lookupErr == nil {
		got := ticketOrderReceipt(&existing)
		s.order.confirmStockReservation(recoveryCtx, reservation)
		s.order.rememberIdempotentOrder(recoveryCtx, userID, idempotencyKey, got)
		return got, nil
	}
	if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
		outcome, recoveryErr := s.order.resolveMissingOrderStockReservation(recoveryCtx, reservation)
		if recoveryErr != nil {
			return nil, errors.Join(err, fmt.Errorf("恢复 Redis 预扣: %w", recoveryErr))
		}
		if outcome == stockReservationRecoveryConfirmed {
			if lookupErr := s.order.db.WithContext(recoveryCtx).
				Where("id = ? AND user_id = ?", reservation.OrderID, userID).
				First(&existing).Error; lookupErr != nil {
				return nil, errors.Join(err, fmt.Errorf("恢复后读取订单: %w", lookupErr))
			}
			got := ticketOrderReceipt(&existing)
			s.order.rememberIdempotentOrder(recoveryCtx, userID, idempotencyKey, got)
			return got, nil
		}
	}
	return nil, err
}

func (s *RushSaleService) reserveRushStock(
	ctx context.Context,
	userID int64,
	campaign *models.RushSaleCampaign,
	quantity int,
	ttl int64,
	idempotencyKey string,
	proposedOrderID int64,
	requestHashes ...string,
) (stockReservationResult, int64, error) {
	requestHash := orderRequestHash(ticketOrderOperationRush, struct {
		CampaignID int64 `json:"campaign_id"`
		Quantity   int   `json:"quantity"`
	}{campaign.ID, quantity})
	if len(requestHashes) > 0 && requestHashes[0] != "" {
		requestHash = requestHashes[0]
	}
	reservationKey := stockReservationKey(proposedOrderID)
	idempotencyMapKey := stockIdempotencyKey(userID, ticketOrderOperationRush, idempotencyKey)
	nowMS := time.Now().UnixMilli()
	run := func(rushKey, ticketKey string, bucketNo int) (stockReservationResult, int64, error) {
		raw, err := reserveRushStockScript.Run(
			ctx,
			s.rdb,
			[]string{
				rushKey,
				ticketKey,
				rushUserCountKey(campaign.ID, userID),
				idempotencyMapKey,
				reservationKey,
				stockReservationPendingKey,
			},
			quantity,
			campaign.PerUserLimit,
			ttl,
			strconv.FormatInt(campaign.ID, 10),
			strconv.FormatInt(campaign.TicketTierID, 10),
			stockReservationKindRush,
			strconv.FormatInt(proposedOrderID, 10),
			strconv.FormatInt(userID, 10),
			bucketNo,
			idempotencyKey,
			requestHash,
			nowMS,
			stockReservationTTLMillis(),
			stockReservationPrefix,
		).Result()
		if err != nil {
			return stockReservationResult{}, 0, err
		}
		return decodeStockReservationResult(raw)
	}
	if !s.order.inventory.Enabled {
		return run(rushStockKey(campaign.ID), ticketStockKey(campaign.TicketTierID), -1)
	}
	n := s.order.inventory.EffectiveBucketCount(campaign.TotalQuota)
	maxAttempts := 1 + s.order.inventory.BucketRetry
	if maxAttempts > n {
		maxAttempts = n
	}
	var last int64 = -1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		b := SelectBucketNo(userID, n, attempt)
		reservation, code, runErr := run(
			RushStockBucketKey(campaign.ID, b),
			TicketStockBucketKey(campaign.TicketTierID, b),
			b,
		)
		if runErr != nil {
			return stockReservationResult{}, 0, runErr
		}
		if code > 0 || code == -4 || code == -2 || code == stockReservationCodeConflict {
			return reservation, code, nil
		}
		last = code
	}
	return stockReservationResult{}, last, nil
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
func (s *TicketOrderService) createRushOrderAfterReservation(
	ctx context.Context,
	orderID int64,
	userID int64,
	idempotencyKey, requestHash, requestID string,
	campaign *models.RushSaleCampaign,
	quantity int,
	purchase PurchaseInfoInput,
	bucketNo *int,
) (*TicketOrderReceipt, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if len(idempotencyKey) < 8 || len(idempotencyKey) > 64 {
		return nil, ErrInvalidTicketCatalog
	}
	// 入口已做过幂等短路；此处依赖唯一索引处理并发双写，避免再查一次 MySQL。
	tier, session, event, venue, err := s.loadPurchasableTier(ctx, campaign.TicketTierID)
	if err != nil {
		return nil, err
	}
	if quantity > tier.PurchaseLimit || quantity > event.MaxTicketsPerOrder {
		return nil, fmt.Errorf("%w: 超过限购数量", ErrTicketOrderUnavailable)
	}
	if event.SaleMode.IsSeated() {
		return nil, fmt.Errorf("%w: 选座购票尚未开放", ErrTicketOrderUnavailable)
	}
	if err := validatePurchaseInfo(purchase, quantity, event.RealNameRequired); err != nil {
		return nil, err
	}
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
		RequestHash:           requestHash,
		RequestID:             requestID,
		FunnelVisitorKey:      funnelVisitorKey(purchase.VisitorID, "", "", userID),
		ExpiresAt:             time.Now().Add(s.paymentTimeout),
		StockBucketNo:         bucketNo,
		RushBucketNo:          bucketNo,
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
		return nil, err
	}
	s.notifyOutboxPublisher()
	s.invalidateUserOrderQueryCache(ctx, userID)
	got := ticketOrderReceipt(order)
	s.rememberIdempotentOrder(ctx, userID, idempotencyKey, got)
	metrics.OrdersCreated.Inc()
	return got, nil
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

// CancelEventCampaigns 活动取消联动：关闭活动下所有进行中的抢票活动，
// 并清理 Redis 库存 key（含分桶）与本地缓存，避免活动取消后仍可抢购下单。
// 用户限购计数 key 无法枚举用户，依赖 Lua 设置的活动结束 TTL 自然过期。
func (s *RushSaleService) CancelEventCampaigns(ctx context.Context, eventID int64) error {
	var campaigns []models.RushSaleCampaign
	if err := s.db.WithContext(ctx).
		Where("ticket_tier_id IN (?) AND status IN ?",
			s.db.WithContext(ctx).Model(&models.TicketTier{}).
				Select("id").
				Where("session_id IN (?)",
					s.db.WithContext(ctx).Model(&models.EventSession{}).
						Select("id").
						Where("event_id = ?", eventID)),
			[]models.RushSaleStatus{
				models.RushSaleStatusScheduled,
				models.RushSaleStatusActive,
			}).
		Find(&campaigns).Error; err != nil {
		return err
	}
	if len(campaigns) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(campaigns))
	for _, campaign := range campaigns {
		ids = append(ids, campaign.ID)
	}
	if err := s.db.WithContext(ctx).Model(&models.RushSaleCampaign{}).
		Where("id IN ?", ids).
		Update("status", models.RushSaleStatusCancelled).Error; err != nil {
		return err
	}
	pipe := s.rdb.Pipeline()
	for _, campaign := range campaigns {
		pipe.Del(ctx, rushStockKey(campaign.ID))
		n := s.order.inventory.EffectiveBucketCount(campaign.TotalQuota)
		for i := 0; i < n; i++ {
			pipe.Del(ctx, RushStockBucketKey(campaign.ID, i))
		}
		s.invalidateRushStockLocal(campaign.ID)
		s.invalidateRushCampaignLocal(campaign.ID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	s.invalidateRushSalesListCache(ctx)
	return nil
}

func (s *RushSaleService) invalidateRushCampaignLocal(campaignID int64) {
	if s.localCache == nil {
		return
	}
	s.localCache.Delete(rushCampaignLocalKey(campaignID))
}
