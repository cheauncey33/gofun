package service

import (
	"gofun/config"
	"gofun/container"
	"gofun/metrics"
	"gofun/models"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/snowflake"
	gocache "github.com/patrickmn/go-cache"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const ticketOutboxMaxAttempts = 20

// 兼容旧常量；实际 batch/tick 以 order_outbox 配置为准。
const ticketOutboxPublishBatch = 200
const ticketOutboxTickInterval = 200 * time.Millisecond

var (
	ErrTicketOrderNotFound     = errors.New("票务订单不存在")
	ErrTicketOrderUnavailable  = errors.New("当前票档不可购买")
	ErrTicketQuotaInsufficient = errors.New("剩余票额不足")
	ErrTicketOrderState        = errors.New("当前订单状态不允许此操作")
	ErrTicketBalance           = errors.New("余额不足")
	ErrTicketOrderNonRetryable = errors.New("票务订单不可重试")
	ErrTicketAlreadyUsed       = errors.New("订单中已有电子票完成核销，不能退款")
)

const ticketStockKeyPrefix = "fuchang:ticket:stock:"

const purchaseNoticeVersion = "ticket-purchase-v1"

var mainlandPhonePattern = regexp.MustCompile(`^1[3-9]\d{9}$`)

var reserveTicketQuotaScript = redis.NewScript(`
local stock = tonumber(redis.call('GET', KEYS[1]))
if stock == nil then
  return -2
end
local quantity = tonumber(ARGV[1])
if stock < quantity then
  return -1
end
redis.call('DECRBY', KEYS[1], quantity)
return stock - quantity
`)

type TicketAttendeeInput struct {
	Name     string `json:"name"`
	IDType   string `json:"id_type"`
	IDNumber string `json:"id_number"`
}

type PurchaseInfoInput struct {
	ContactName   string                `json:"contact_name"`
	ContactPhone  string                `json:"contact_phone"`
	TermsAccepted bool                  `json:"terms_accepted"`
	Attendees     []TicketAttendeeInput `json:"attendees"`
}

type CreateTicketOrderInput struct {
	TicketTierID int64 `json:"ticket_tier_id,string" binding:"required"`
	Quantity     int   `json:"quantity" binding:"required"`
	PurchaseInfoInput
}

type TicketOrderReceipt struct {
	OrderID int64                    `json:"order_id,string"`
	OrderNo string                   `json:"order_no"`
	Status  models.TicketOrderStatus `json:"status"`
}

type TicketOrderMessage struct {
	OrderID            int64             `json:"order_id"`
	UserID             int64             `json:"user_id"`
	TicketTierID       int64             `json:"ticket_tier_id"`
	Quantity           int               `json:"quantity"`
	RushSaleCampaignID *int64            `json:"rush_sale_campaign_id,omitempty"`
	StockBucketNo      *int              `json:"stock_bucket_no,omitempty"`
	RushBucketNo       *int              `json:"rush_bucket_no,omitempty"`
	TraceContext       map[string]string `json:"trace_context,omitempty"`
}

type TicketOrderService struct {
	db                *gorm.DB
	rdb               *redis.Client
	node              *snowflake.Node
	newMQChannel      func() (*amqp.Channel, error)
	mqQueueName       string
	mqQueueType       string
	localCache        *gocache.Cache
	paymentTimeout    time.Duration
	payment           PaymentGateway
	credentialSigner  *TicketCredentialSigner
	identityHashKey   []byte
	timeoutExchange   string
	timeoutDelayQueue string
	timeoutQueue      string
	timeoutRoutingKey string
	scannerInterval   time.Duration
	// outboxNotify 在写入 pending outbox 后唤醒发布循环；容量 1，合并突发通知。
	outboxNotify       chan struct{}
	outboxWriteMode    string
	outboxBuffer       *outboxWriteBuffer
	outboxRecoverEvery time.Duration
	inventory          InventoryBucketSettings
}

func (s *TicketOrderService) ConfigureInventory(cfg config.InventoryConfig) {
	s.inventory = NewInventoryBucketSettings(cfg)
}

// EnsureInventoryBuckets 存量票档/活动幂等拆桶；开关关闭时 no-op。
func (s *TicketOrderService) EnsureInventoryBuckets(ctx context.Context) error {
	if !s.inventory.Enabled {
		return nil
	}
	var tiers []models.TicketTier
	if err := s.db.WithContext(ctx).Find(&tiers).Error; err != nil {
		return err
	}
	for i := range tiers {
		if err := EnsureTierBuckets(s.db.WithContext(ctx), &tiers[i], s.inventory); err != nil {
			return err
		}
	}
	var campaigns []models.RushSaleCampaign
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []models.RushSaleStatus{
			models.RushSaleStatusScheduled,
			models.RushSaleStatusActive,
			models.RushSaleStatusDraft,
		}).Find(&campaigns).Error; err != nil {
		return err
	}
	for i := range campaigns {
		if err := EnsureRushBuckets(s.db.WithContext(ctx), &campaigns[i], s.inventory); err != nil {
			return err
		}
	}
	return nil
}

func NewTicketOrderService(c *container.Container, timeoutMinutes int) *TicketOrderService {
	if timeoutMinutes <= 0 {
		timeoutMinutes = 15
	}
	return &TicketOrderService{
		db:               c.DB,
		rdb:              c.RDB,
		node:             c.SnowflakeNode,
		newMQChannel:     c.NewMQChannel,
		mqQueueName:      c.MQQueueName,
		mqQueueType:      c.MQQueueType,
		localCache:       c.LocalCache,
		paymentTimeout:   time.Duration(timeoutMinutes) * time.Minute,
		payment:          NewBalancePaymentGateway(),
		credentialSigner: NewTicketCredentialSigner(c.TicketQRSecrets...),
		identityHashKey:  append([]byte(nil), c.TicketQRSecret...),
		scannerInterval:  2 * time.Minute,
		outboxNotify:     make(chan struct{}, 1),
	}
}

func (s *TicketOrderService) WarmTicketQuota(ctx context.Context) error {
	if err := s.EnsureInventoryBuckets(ctx); err != nil {
		return err
	}
	var tiers []models.TicketTier
	if err := s.db.WithContext(ctx).Find(&tiers).Error; err != nil {
		return err
	}
	var queuedOrders []models.TicketOrder
	if err := s.db.WithContext(ctx).Preload("Items").
		Where("status = ?", models.TicketOrderStatusQueued).
		Find(&queuedOrders).Error; err != nil {
		return err
	}
	queuedByTier := make(map[int64]int)
	queuedByCampaign := make(map[int64]int)
	queuedByTierBucket := make(map[string]int)
	queuedByCampaignBucket := make(map[string]int)
	for _, order := range queuedOrders {
		for _, item := range order.Items {
			queuedByTier[item.TicketTierID] += item.Quantity
			if order.RushSaleCampaignID != nil {
				queuedByCampaign[*order.RushSaleCampaignID] += item.Quantity
			}
			if s.inventory.Enabled {
				stockBucket := queuedOrderStockBucket(&order, s.inventory)
				queuedByTierBucket[fmt.Sprintf("%d:%d", item.TicketTierID, stockBucket)] += item.Quantity
				if order.RushSaleCampaignID != nil {
					rushBucket := queuedOrderRushBucket(&order, s.inventory)
					queuedByCampaignBucket[fmt.Sprintf("%d:%d", *order.RushSaleCampaignID, rushBucket)] += item.Quantity
				}
			}
		}
	}
	values := make(map[string]interface{})
	if s.inventory.Enabled {
		var tierBuckets []models.TicketTierBucket
		if err := s.db.WithContext(ctx).Find(&tierBuckets).Error; err != nil {
			return err
		}
		for _, bucket := range tierBuckets {
			available := bucket.RemainingQuota - queuedByTierBucket[fmt.Sprintf("%d:%d", bucket.TierID, bucket.BucketNo)]
			if available < 0 {
				available = 0
			}
			values[TicketStockBucketKey(bucket.TierID, bucket.BucketNo)] = available
		}
	} else {
		for _, tier := range tiers {
			available := tier.RemainingQuota - queuedByTier[tier.ID]
			if available < 0 {
				available = 0
			}
			values[ticketStockKey(tier.ID)] = available
		}
	}
	var campaigns []models.RushSaleCampaign
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []models.RushSaleStatus{
			models.RushSaleStatusScheduled,
			models.RushSaleStatusActive,
		}).Find(&campaigns).Error; err != nil {
		return err
	}
	if s.inventory.Enabled {
		var rushBuckets []models.RushCampaignBucket
		if err := s.db.WithContext(ctx).Find(&rushBuckets).Error; err != nil {
			return err
		}
		for _, bucket := range rushBuckets {
			available := bucket.RemainingQuota - queuedByCampaignBucket[fmt.Sprintf("%d:%d", bucket.CampaignID, bucket.BucketNo)]
			if available < 0 {
				available = 0
			}
			values[RushStockBucketKey(bucket.CampaignID, bucket.BucketNo)] = available
		}
	} else {
		for _, campaign := range campaigns {
			available := campaign.RemainingQuota - queuedByCampaign[campaign.ID]
			if available < 0 {
				available = 0
			}
			values[rushStockKey(campaign.ID)] = available
		}
	}
	var activeRushOrders []models.TicketOrder
	if err := s.db.WithContext(ctx).Preload("Items").
		Where("rush_sale_campaign_id IS NOT NULL AND status IN ?", []models.TicketOrderStatus{
			models.TicketOrderStatusQueued,
			models.TicketOrderStatusPendingPayment,
			models.TicketOrderStatusPaid,
		}).Find(&activeRushOrders).Error; err != nil {
		return err
	}
	type campaignUser struct {
		campaignID int64
		userID     int64
	}
	purchasedByUser := make(map[campaignUser]int)
	for _, order := range activeRushOrders {
		if order.RushSaleCampaignID == nil {
			continue
		}
		for _, item := range order.Items {
			purchasedByUser[campaignUser{
				campaignID: *order.RushSaleCampaignID,
				userID:     order.UserID,
			}] += item.Quantity
		}
	}
	for key, quantity := range purchasedByUser {
		values[rushUserCountKey(key.campaignID, key.userID)] = quantity
	}
	if len(values) == 0 {
		return nil
	}
	return s.rdb.MSet(ctx, values).Err()
}

// RecoverQueuedOrders 为尚未投递的 queued 订单补写 outbox。
// 跳过：缓冲中已有草稿，或库中已有 pending/publishing/published 记录。
func (s *TicketOrderService) RecoverQueuedOrders(ctx context.Context) error {
	var orders []models.TicketOrder
	if err := s.db.WithContext(ctx).Preload("Items").
		Where("status = ?", models.TicketOrderStatusQueued).
		Limit(500).Find(&orders).Error; err != nil {
		return err
	}
	backfilled := 0
	for _, order := range orders {
		if len(order.Items) != 1 {
			result := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
				Where("id = ? AND status = ?", order.ID, models.TicketOrderStatusQueued).
				Updates(map[string]interface{}{
					"status":        models.TicketOrderStatusFailed,
					"cancel_reason": "queued 订单明细异常",
				})
			if result.Error == nil && result.RowsAffected > 0 {
				totalQty := 0
				for _, item := range order.Items {
					totalQty += item.Quantity
					s.rollbackRedisQuota(ctx, item.TicketTierID, item.Quantity, order.StockBucketNo)
				}
				if order.RushSaleCampaignID != nil && totalQty > 0 {
					pipe := s.rdb.TxPipeline()
					if s.inventory.Enabled {
						rb := queuedOrderRushBucket(&order, s.inventory)
						pipe.IncrBy(ctx, RushStockBucketKey(*order.RushSaleCampaignID, rb), int64(totalQty))
					} else {
						pipe.IncrBy(ctx, rushStockKey(*order.RushSaleCampaignID), int64(totalQty))
					}
					pipe.DecrBy(
						ctx,
						rushUserCountKey(*order.RushSaleCampaignID, order.UserID),
						int64(totalQty),
					)
					_, _ = pipe.Exec(ctx)
				}
			}
			continue
		}
		if s.outboxBuffer != nil && s.outboxBuffer.containsOrder(order.ID) {
			continue
		}
		var activeCount int64
		if err := s.db.WithContext(ctx).Model(&models.TicketOrderOutbox{}).
			Where("order_id = ? AND status IN ?", order.ID, []models.TicketOrderOutboxStatus{
				models.TicketOrderOutboxPending,
				models.TicketOrderOutboxPublishing,
				models.TicketOrderOutboxPublished,
			}).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount > 0 {
			continue
		}
		message := TicketOrderMessage{
			OrderID:            order.ID,
			UserID:             order.UserID,
			TicketTierID:       order.Items[0].TicketTierID,
			Quantity:           order.Items[0].Quantity,
			RushSaleCampaignID: order.RushSaleCampaignID,
			StockBucketNo:      order.StockBucketNo,
			RushBucketNo:       order.RushBucketNo,
		}
		if err := s.enqueueOutbox(ctx, order.ID, message); err != nil {
			return err
		}
		backfilled++
		metrics.OutboxRecoverBackfill.Inc()
	}
	if backfilled > 0 {
		s.notifyOutboxPublisher()
	}
	return nil
}

func (s *TicketOrderService) enqueueOutbox(
	ctx context.Context,
	orderID int64,
	message TicketOrderMessage,
) error {
	if err := s.enqueueOutboxInTx(ctx, s.db, orderID, message); err != nil {
		return err
	}
	s.notifyOutboxPublisher()
	return nil
}

func (s *TicketOrderService) notifyOutboxPublisher() {
	if s.outboxNotify == nil {
		return
	}
	select {
	case s.outboxNotify <- struct{}{}:
	default:
	}
}

func (s *TicketOrderService) CreateOrder(
	ctx context.Context,
	userID int64,
	idempotencyKey, requestID string,
	input CreateTicketOrderInput,
) (*TicketOrderReceipt, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if userID <= 0 || input.TicketTierID <= 0 || input.Quantity <= 0 ||
		len(idempotencyKey) < 8 || len(idempotencyKey) > 64 {
		return nil, ErrInvalidTicketCatalog
	}

	if receipt, err := s.lookupIdempotentOrder(ctx, userID, idempotencyKey); err != nil {
		return nil, err
	} else if receipt != nil {
		return receipt, nil
	}

	tier, session, event, venue, err := s.loadPurchasableTier(ctx, input.TicketTierID)
	if err != nil {
		return nil, err
	}
	if input.Quantity > tier.PurchaseLimit || input.Quantity > event.MaxTicketsPerOrder {
		return nil, fmt.Errorf("%w: 超过限购数量", ErrTicketOrderUnavailable)
	}
	if err := validatePurchaseInfo(input.PurchaseInfoInput, input.Quantity, event.RealNameRequired); err != nil {
		return nil, err
	}

	bucketNo, remaining, err := s.reserveTicketStock(ctx, userID, tier, input.Quantity)
	if err != nil || remaining < 0 {
		if remaining == -2 {
			return nil, fmt.Errorf("%w: 票额缓存尚未预热", ErrTicketOrderUnavailable)
		}
		if remaining == -1 {
			return nil, ErrTicketQuotaInsufficient
		}
		return nil, fmt.Errorf("预扣票额: %w", err)
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
		OrderSource:           models.TicketOrderSourceNormal,
		Status:                models.TicketOrderStatusQueued,
		PaymentStatus:         models.PaymentStatusUnpaid,
		TotalAmountCents:      tier.PriceCents * int64(input.Quantity),
		ContactName:           strings.TrimSpace(input.ContactName),
		ContactPhone:          strings.TrimSpace(input.ContactPhone),
		RealNameRequired:      event.RealNameRequired,
		PurchaseNoticeVersion: purchaseNoticeVersion,
		TermsAcceptedAt:       &acceptedAt,
		IdempotencyKey:        idempotencyKey,
		RequestID:             strings.TrimSpace(requestID),
		ExpiresAt:             time.Now().Add(s.paymentTimeout),
		Items: []models.TicketOrderItem{{
			TicketTierID:            tier.ID,
			Quantity:                input.Quantity,
			UnitPriceCents:          tier.PriceCents,
			EventTitleSnapshot:      event.Title,
			SessionStartsAtSnapshot: session.StartsAt,
			VenueNameSnapshot:       venue.Name,
			VenueAddressSnapshot:    venue.Address,
			TierNameSnapshot:        tier.Name,
		}},
	}
	if s.inventory.Enabled {
		order.StockBucketNo = intPtr(bucketNo)
	}
	if attendees := s.buildAttendeeSnapshots(orderID, input.Attendees, event.RealNameRequired); len(attendees) > 0 {
		order.Attendees = attendees
	}
	message := TicketOrderMessage{
		OrderID:      orderID,
		UserID:       userID,
		TicketTierID: tier.ID,
		Quantity:     input.Quantity,
	}
	if s.inventory.Enabled {
		message.StockBucketNo = intPtr(bucketNo)
	}
	if err := s.createOrderAndOutbox(ctx, order, message); err != nil {
		s.rollbackRedisQuota(ctx, tier.ID, input.Quantity, order.StockBucketNo)
		var existing models.TicketOrder
		if lookupErr := s.db.WithContext(ctx).
			Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
			First(&existing).Error; lookupErr == nil {
			receipt := ticketOrderReceipt(&existing)
			s.rememberIdempotentOrder(ctx, userID, idempotencyKey, receipt)
			return receipt, nil
		}
		return nil, err
	}
	s.notifyOutboxPublisher()
	receipt := ticketOrderReceipt(order)
	s.rememberIdempotentOrder(ctx, userID, idempotencyKey, receipt)
	metrics.OrdersCreated.Inc()
	return receipt, nil
}

func (s *TicketOrderService) ProcessOrderTask(
	ctx context.Context,
	message TicketOrderMessage,
) error {
	ctx, span := otel.Tracer("fuchang-ticketing/order").Start(
		ctx,
		"ticket.order.finalize",
		trace.WithAttributes(
			attribute.Int64("ticket.order.id", message.OrderID),
			attribute.Int64("ticket.user.id", message.UserID),
		),
	)
	defer span.End()
	var (
		enteredPending bool
		pendingOrderID int64
		pendingUserID  int64
	)
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order models.TicketOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items").First(&order, message.OrderID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("%w: 订单凭据不存在", ErrTicketOrderNonRetryable)
			}
			return err
		}
		if order.Status != models.TicketOrderStatusQueued {
			return nil
		}
		if order.UserID != message.UserID || len(order.Items) != 1 ||
			order.Items[0].TicketTierID != message.TicketTierID ||
			order.Items[0].Quantity != message.Quantity {
			return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
		}
		if (order.RushSaleCampaignID == nil) != (message.RushSaleCampaignID == nil) {
			return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
		}
		if order.RushSaleCampaignID != nil &&
			*order.RushSaleCampaignID != *message.RushSaleCampaignID {
			return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
		}

		// 分桶开启时扣 bucket 行；关闭时仍扣父表 remaining_quota。
		// 订单行 FOR UPDATE 防同单并发消费。
		if s.inventory.Enabled {
			stockBucket, _ := resolveStockBucketForOrder(&order, message.StockBucketNo, s.inventory, 0)
			if order.RushSaleCampaignID != nil {
				rushBucket, _ := resolveRushBucketForOrder(&order, message.RushBucketNo, s.inventory, 0)
				if err := deductRushBucket(tx, *order.RushSaleCampaignID, message.TicketTierID, rushBucket, message.Quantity); err != nil {
					return err
				}
			}
			if err := deductTierBucket(tx, message.TicketTierID, stockBucket, message.Quantity); err != nil {
				return err
			}
		} else {
			if order.RushSaleCampaignID != nil {
				campaignResult := tx.Model(&models.RushSaleCampaign{}).
					Where(
						"id = ? AND ticket_tier_id = ? AND remaining_quota >= ?",
						*order.RushSaleCampaignID, message.TicketTierID, message.Quantity,
					).
					Update("remaining_quota", gorm.Expr("remaining_quota - ?", message.Quantity))
				if campaignResult.Error != nil {
					return campaignResult.Error
				}
				if campaignResult.RowsAffected == 0 {
					return classifyRushQuotaUpdateMiss(tx, *order.RushSaleCampaignID, message.TicketTierID, message.Quantity)
				}
			}
			tierResult := tx.Model(&models.TicketTier{}).
				Where("id = ? AND remaining_quota >= ?", message.TicketTierID, message.Quantity).
				Updates(map[string]interface{}{
					"remaining_quota": gorm.Expr("remaining_quota - ?", message.Quantity),
					"sold_count":      gorm.Expr("sold_count + ?", message.Quantity),
					"version":         gorm.Expr("version + 1"),
					"status": gorm.Expr(
						"CASE WHEN remaining_quota <= ? AND status = ? THEN ? ELSE status END",
						message.Quantity,
						models.TicketTierStatusOnSale,
						models.TicketTierStatusSoldOut,
					),
				})
			if tierResult.Error != nil {
				return tierResult.Error
			}
			if tierResult.RowsAffected == 0 {
				return classifyTierQuotaUpdateMiss(tx, message.TicketTierID, message.Quantity)
			}
		}
		expiresAt := time.Now().Add(s.paymentTimeout)
		result := tx.Model(&models.TicketOrder{}).
			Where("id = ? AND status = ?", order.ID, models.TicketOrderStatusQueued).
			Updates(map[string]interface{}{
				"status":     models.TicketOrderStatusPendingPayment,
				"expires_at": expiresAt,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 1 {
			enteredPending = true
			pendingOrderID = order.ID
			pendingUserID = order.UserID
		}
		return nil
	})
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	// 进入待支付后投递延时关单；失败由扫描器兜底，不阻塞消费 ack。
	if enteredPending {
		if pubErr := s.PublishPaymentTimeout(ctx, pendingOrderID, pendingUserID); pubErr != nil {
			log.Printf("ticket payment timeout publish order %d: %v", pendingOrderID, pubErr)
		}
	}
	return nil
}

func classifyTierQuotaUpdateMiss(tx *gorm.DB, tierID int64, quantity int) error {
	var tier models.TicketTier
	err := tx.Select("id", "remaining_quota").First(&tier, tierID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: 票档不存在", ErrTicketOrderNonRetryable)
	}
	if err != nil {
		return err
	}
	if tier.RemainingQuota < quantity {
		return fmt.Errorf("%w: MySQL 票额不足", ErrTicketOrderNonRetryable)
	}
	return fmt.Errorf("%w: MySQL 票额不足", ErrTicketOrderNonRetryable)
}

func classifyRushQuotaUpdateMiss(tx *gorm.DB, campaignID, tierID int64, quantity int) error {
	var campaign models.RushSaleCampaign
	err := tx.Select("id", "ticket_tier_id", "remaining_quota").First(&campaign, campaignID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: 限时开售活动不存在", ErrTicketOrderNonRetryable)
	}
	if err != nil {
		return err
	}
	if campaign.TicketTierID != tierID || campaign.RemainingQuota < quantity {
		return fmt.Errorf("%w: 限时开售票额不足", ErrTicketOrderNonRetryable)
	}
	return fmt.Errorf("%w: 限时开售票额不足", ErrTicketOrderNonRetryable)
}

func (s *TicketOrderService) FinalizeFailedMessage(
	ctx context.Context,
	message TicketOrderMessage,
	reason string,
) {
	result := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("id = ? AND status = ?", message.OrderID, models.TicketOrderStatusQueued).
		Updates(map[string]interface{}{
			"status":        models.TicketOrderStatusFailed,
			"cancel_reason": reason,
		})
	if result.Error == nil && result.RowsAffected > 0 {
		s.rollbackRedisQuota(ctx, message.TicketTierID, message.Quantity, message.StockBucketNo)
		if message.RushSaleCampaignID != nil {
			pipe := s.rdb.TxPipeline()
			if s.inventory.Enabled {
				rushBucket := resolveMessageRushBucket(message, s.inventory)
				pipe.IncrBy(ctx, RushStockBucketKey(*message.RushSaleCampaignID, rushBucket), int64(message.Quantity))
			} else {
				pipe.IncrBy(ctx, rushStockKey(*message.RushSaleCampaignID), int64(message.Quantity))
			}
			pipe.DecrBy(
				ctx,
				rushUserCountKey(*message.RushSaleCampaignID, message.UserID),
				int64(message.Quantity),
			)
			_, _ = pipe.Exec(ctx)
		}
	}
}

func (s *TicketOrderService) GetOrder(
	ctx context.Context,
	userID, orderID int64,
) (*models.TicketOrder, error) {
	var order models.TicketOrder
	err := s.db.WithContext(ctx).Preload("Items").Preload("Attendees").Preload("Tickets.OrderItem").
		Where("id = ? AND user_id = ?", orderID, userID).
		First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTicketOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	s.attachTicketCredentials(order.Tickets)
	return &order, nil
}

func (s *TicketOrderService) ListOrders(
	ctx context.Context,
	userID int64,
	page, pageSize int,
) ([]models.TicketOrder, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}
	var orders []models.TicketOrder
	var total int64
	query := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("user_id = ?", userID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Preload("Items").Order("create_time DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&orders).Error
	return orders, total, err
}

func (s *TicketOrderService) PayOrder(
	ctx context.Context,
	userID, orderID int64,
) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order models.TicketOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items").
			Where("id = ? AND user_id = ?", orderID, userID).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTicketOrderNotFound
			}
			return err
		}
		if order.Status != models.TicketOrderStatusPendingPayment ||
			time.Now().After(order.ExpiresAt) {
			return ErrTicketOrderState
		}
		if len(order.Items) == 0 {
			return fmt.Errorf("订单明细异常")
		}
		if err := s.payment.Debit(ctx, tx, userID, order.TotalAmountCents); err != nil {
			return err
		}
		now := time.Now()
		if err := s.issueAdmissionTickets(tx, &order, now); err != nil {
			return err
		}
		return tx.Model(&models.TicketOrder{}).Where("id = ?", orderID).
			Updates(map[string]interface{}{
				"status":         models.TicketOrderStatusPaid,
				"payment_status": models.PaymentStatusPaid,
				"paid_at":        &now,
			}).Error
	})
}

type BatchRefundResult struct {
	Refunded    int `json:"refunded"`
	SkippedUsed int `json:"skipped_used"`
	Failed      int `json:"failed"`
	TotalPaid   int `json:"total_paid"`
}

func (s *TicketOrderService) BatchRefundEvent(
	ctx context.Context,
	userID, organizerID, eventID int64,
	reason string,
) (*BatchRefundResult, error) {
	var memberCount int64
	if err := s.db.WithContext(ctx).Model(&models.OrganizerMember{}).
		Where(
			"organizer_id = ? AND user_id = ? AND status = ?",
			organizerID, userID, models.OrganizerStatusActive,
		).Count(&memberCount).Error; err != nil {
		return nil, err
	}
	if memberCount == 0 {
		return nil, ErrOrganizerForbidden
	}
	var event models.Event
	if err := s.db.WithContext(ctx).
		Where("id = ? AND organizer_id = ?", eventID, organizerID).
		First(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTicketResourceNotFound
		}
		return nil, err
	}
	var orders []models.TicketOrder
	if err := s.db.WithContext(ctx).
		Where("event_id = ? AND status = ?", eventID, models.TicketOrderStatusPaid).
		Find(&orders).Error; err != nil {
		return nil, err
	}
	result := &BatchRefundResult{TotalPaid: len(orders)}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "活动取消批量退款"
	}
	for _, order := range orders {
		if err := s.CancelOrder(ctx, order.UserID, order.ID, reason); err != nil {
			if errors.Is(err, ErrTicketAlreadyUsed) {
				result.SkippedUsed++
			} else {
				result.Failed++
			}
			continue
		}
		result.Refunded++
	}
	return result, nil
}

func (s *TicketOrderService) CancelOrder(
	ctx context.Context,
	userID, orderID int64,
	reason string,
) error {
	var tierID int64
	var quantity int
	var refundCents int64
	var rushCampaignID *int64
	var stockBucketNo *int
	var rushBucketNo *int
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order models.TicketOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items").
			Where("id = ? AND user_id = ?", orderID, userID).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTicketOrderNotFound
			}
			return err
		}
		if order.Status != models.TicketOrderStatusPendingPayment &&
			order.Status != models.TicketOrderStatusPaid {
			return ErrTicketOrderState
		}
		if len(order.Items) != 1 {
			return fmt.Errorf("订单明细异常")
		}
		tierID, quantity = order.Items[0].TicketTierID, order.Items[0].Quantity
		rushCampaignID = order.RushSaleCampaignID
		if order.Status.HasBeenPaid() {
			var usedCount int64
			if err := tx.Model(&models.AdmissionTicket{}).
				Where("order_id = ? AND status = ?", order.ID, models.AdmissionTicketStatusUsed).
				Count(&usedCount).Error; err != nil {
				return err
			}
			if usedCount > 0 {
				return ErrTicketAlreadyUsed
			}
			refundCents = order.TotalAmountCents
			if err := s.payment.Credit(ctx, tx, userID, refundCents); err != nil {
				return err
			}
		}
		if s.inventory.Enabled {
			sb, _ := resolveStockBucketForOrder(&order, nil, s.inventory, 0)
			stockBucketNo = intPtr(sb)
			if err := restoreTierBucket(tx, tierID, sb, quantity); err != nil {
				return err
			}
			if rushCampaignID != nil {
				rb, _ := resolveRushBucketForOrder(&order, nil, s.inventory, 0)
				rushBucketNo = intPtr(rb)
				if err := restoreRushBucket(tx, *rushCampaignID, rb, quantity); err != nil {
					return err
				}
			}
		} else {
			if err := restoreTierQuota(tx, tierID, quantity); err != nil {
				return err
			}
			if rushCampaignID != nil {
				if err := tx.Model(&models.RushSaleCampaign{}).
					Where("id = ?", *rushCampaignID).
					Update("remaining_quota", gorm.Expr("remaining_quota + ?", quantity)).Error; err != nil {
					return err
				}
			}
		}
		now := time.Now()
		if refundCents > 0 {
			if err := tx.Model(&models.AdmissionTicket{}).
				Where("order_id = ? AND status = ?", order.ID, models.AdmissionTicketStatusValid).
				Updates(map[string]interface{}{
					"status":        models.AdmissionTicketStatusRevoked,
					"revoked_at":    &now,
					"revoke_reason": strings.TrimSpace(reason),
				}).Error; err != nil {
				return err
			}
		}
		paymentStatus := order.PaymentStatus
		if refundCents > 0 {
			paymentStatus = models.PaymentStatusRefunded
		}
		return tx.Model(&models.TicketOrder{}).Where("id = ?", orderID).
			Updates(map[string]interface{}{
				"status":         models.TicketOrderStatusCancelled,
				"payment_status": paymentStatus,
				"cancelled_at":   &now,
				"cancel_reason":  strings.TrimSpace(reason),
			}).Error
	})
	if err == nil {
		s.rollbackRedisQuota(ctx, tierID, quantity, stockBucketNo)
		if rushCampaignID != nil {
			pipe := s.rdb.TxPipeline()
			if s.inventory.Enabled && rushBucketNo != nil {
				pipe.IncrBy(ctx, RushStockBucketKey(*rushCampaignID, *rushBucketNo), int64(quantity))
			} else {
				pipe.IncrBy(ctx, rushStockKey(*rushCampaignID), int64(quantity))
			}
			pipe.DecrBy(
				ctx,
				rushUserCountKey(*rushCampaignID, userID),
				int64(quantity),
			)
			_, _ = pipe.Exec(ctx)
		}
	}
	return err
}

func (s *TicketOrderService) issueAdmissionTickets(
	tx *gorm.DB,
	order *models.TicketOrder,
	issuedAt time.Time,
) error {
	for _, item := range order.Items {
		for sequence := 1; sequence <= item.Quantity; sequence++ {
			ticketID := s.node.Generate().Int64()
			ticket := models.AdmissionTicket{
				Base:         models.Base{ID: ticketID},
				TicketNo:     "FT" + strconv.FormatInt(ticketID, 10),
				OrderID:      order.ID,
				OrderItemID:  item.ID,
				SequenceNo:   sequence,
				UserID:       order.UserID,
				OrganizerID:  order.OrganizerID,
				EventID:      order.EventID,
				SessionID:    order.SessionID,
				TicketTierID: item.TicketTierID,
				Status:       models.AdmissionTicketStatusValid,
				IssuedAt:     issuedAt,
			}
			if err := tx.Create(&ticket).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *TicketOrderService) attachTicketCredentials(tickets []models.AdmissionTicket) {
	for index := range tickets {
		if tickets[index].Status == models.AdmissionTicketStatusValid {
			tickets[index].Credential = s.credentialSigner.Sign(tickets[index].ID)
		}
	}
}

// BackfillPaidAdmissionTickets 为电子票功能上线前已经支付的订单补签入场票。
// 唯一键 (order_item_id, sequence_no) 使该过程可以安全重跑。
func (s *TicketOrderService) BackfillPaidAdmissionTickets(ctx context.Context) error {
	var orderIDs []int64
	if err := s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("status = ?", models.TicketOrderStatusPaid).
		Pluck("id", &orderIDs).Error; err != nil {
		return err
	}
	for _, orderID := range orderIDs {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var order models.TicketOrder
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Preload("Items").
				First(&order, orderID).Error; err != nil {
				return err
			}
			for _, item := range order.Items {
				for sequence := 1; sequence <= item.Quantity; sequence++ {
					var count int64
					if err := tx.Model(&models.AdmissionTicket{}).
						Where("order_item_id = ? AND sequence_no = ?", item.ID, sequence).
						Count(&count).Error; err != nil {
						return err
					}
					if count > 0 {
						continue
					}
					ticketID := s.node.Generate().Int64()
					issuedAt := order.CreateTime
					if order.PaidAt != nil {
						issuedAt = *order.PaidAt
					}
					if err := tx.Create(&models.AdmissionTicket{
						Base:         models.Base{ID: ticketID},
						TicketNo:     "FT" + strconv.FormatInt(ticketID, 10),
						OrderID:      order.ID,
						OrderItemID:  item.ID,
						SequenceNo:   sequence,
						UserID:       order.UserID,
						OrganizerID:  order.OrganizerID,
						EventID:      order.EventID,
						SessionID:    order.SessionID,
						TicketTierID: item.TicketTierID,
						Status:       models.AdmissionTicketStatusValid,
						IssuedAt:     issuedAt,
					}).Error; err != nil {
						return err
					}
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *TicketOrderService) CancelExpiredOrders(ctx context.Context) error {
	var orders []models.TicketOrder
	if err := s.db.WithContext(ctx).Preload("Items").
		Where("status = ? AND expires_at <= ?",
			models.TicketOrderStatusPendingPayment, time.Now()).
		Limit(100).Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		err := s.cancelPendingPaymentOnly(ctx, order.UserID, order.ID, "支付超时自动取消")
		if err == nil || errors.Is(err, ErrTicketOrderState) || errors.Is(err, ErrTicketOrderNotFound) {
			continue
		}
		switch classifyPaymentTimeoutError(err) {
		case paymentTimeoutErrorPermanent:
			metrics.TicketTimeoutMessages.WithLabelValues("scanner_permanent_discard").Inc()
			log.Printf("ticket timeout scanner order %d permanent failure: %v", order.ID, err)
		case paymentTimeoutErrorTransient:
			metrics.TicketTimeoutMessages.WithLabelValues("scanner_error").Inc()
			log.Printf("ticket timeout scanner order %d transient failure: %v", order.ID, err)
		}
	}
	return nil
}

// cancelPendingPaymentOnly 仅取消仍为 pending_payment 的订单（超时/扫描专用）。
// 与 PayOrder 经行锁串行：已支付则直接跳过，绝不会走退款路径。
func (s *TicketOrderService) cancelPendingPaymentOnly(
	ctx context.Context,
	userID, orderID int64,
	reason string,
) error {
	var tierID int64
	var quantity int
	var rushCampaignID *int64
	var stockBucketNo *int
	var rushBucketNo *int
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var order models.TicketOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items").
			Where("id = ? AND user_id = ?", orderID, userID).
			First(&order).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTicketOrderNotFound
			}
			return err
		}
		if order.Status != models.TicketOrderStatusPendingPayment {
			return ErrTicketOrderState
		}
		if len(order.Items) != 1 {
			return fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
		}
		tierID, quantity = order.Items[0].TicketTierID, order.Items[0].Quantity
		rushCampaignID = order.RushSaleCampaignID
		if s.inventory.Enabled {
			sb, _ := resolveStockBucketForOrder(&order, nil, s.inventory, 0)
			stockBucketNo = intPtr(sb)
			if err := restoreTierBucket(tx, tierID, sb, quantity); err != nil {
				return err
			}
			if rushCampaignID != nil {
				rb, _ := resolveRushBucketForOrder(&order, nil, s.inventory, 0)
				rushBucketNo = intPtr(rb)
				if err := restoreRushBucket(tx, *rushCampaignID, rb, quantity); err != nil {
					return err
				}
			}
		} else {
			if err := restoreTierQuota(tx, tierID, quantity); err != nil {
				return err
			}
			if rushCampaignID != nil {
				if err := tx.Model(&models.RushSaleCampaign{}).
					Where("id = ?", *rushCampaignID).
					Update("remaining_quota", gorm.Expr("remaining_quota + ?", quantity)).Error; err != nil {
					return err
				}
			}
		}
		now := time.Now()
		result := tx.Model(&models.TicketOrder{}).
			Where("id = ? AND status = ?", orderID, models.TicketOrderStatusPendingPayment).
			Updates(map[string]interface{}{
				"status":        models.TicketOrderStatusCancelled,
				"cancelled_at":  &now,
				"cancel_reason": strings.TrimSpace(reason),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return ErrTicketOrderState
		}
		return nil
	})
	if err == nil {
		s.rollbackRedisQuota(ctx, tierID, quantity, stockBucketNo)
		if rushCampaignID != nil {
			pipe := s.rdb.TxPipeline()
			if s.inventory.Enabled && rushBucketNo != nil {
				pipe.IncrBy(ctx, RushStockBucketKey(*rushCampaignID, *rushBucketNo), int64(quantity))
			} else {
				pipe.IncrBy(ctx, rushStockKey(*rushCampaignID), int64(quantity))
			}
			pipe.DecrBy(
				ctx,
				rushUserCountKey(*rushCampaignID, userID),
				int64(quantity),
			)
			_, _ = pipe.Exec(ctx)
		}
	}
	return err
}

func (s *TicketOrderService) StartTimeoutScanner(ctx context.Context) {
	interval := s.scannerInterval
	if interval <= 0 {
		interval = 2 * time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = s.CancelExpiredOrders(ctx)
		}
	}
}

func (s *TicketOrderService) loadPurchasableTier(
	ctx context.Context,
	tierID int64,
) (*models.TicketTier, *models.EventSession, *models.Event, *models.Venue, error) {
	cacheKey := purchasableContextLocalKey(tierID)
	if s.localCache != nil {
		if cached, found := s.localCache.Get(cacheKey); found {
			pc := cached.(purchasableContext)
			now := time.Now()
			if pc.Tier.Status == models.TicketTierStatusOnSale &&
				pc.Session.Status == models.SessionStatusOnSale &&
				pc.Event.Status == models.EventStatusPublished &&
				!now.Before(pc.Session.SaleStartsAt) && !now.After(pc.Session.SaleEndsAt) {
				tier, session, event, venue := pc.Tier, pc.Session, pc.Event, pc.Venue
				return &tier, &session, &event, &venue, nil
			}
		}
	}

	var tier models.TicketTier
	if err := s.db.WithContext(ctx).First(&tier, tierID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, nil, ErrTicketOrderUnavailable
		}
		return nil, nil, nil, nil, err
	}
	var session models.EventSession
	if err := s.db.WithContext(ctx).First(&session, tier.SessionID).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	var event models.Event
	if err := s.db.WithContext(ctx).First(&event, session.EventID).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	var venue models.Venue
	if err := s.db.WithContext(ctx).First(&venue, session.VenueID).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	now := time.Now()
	if tier.Status != models.TicketTierStatusOnSale ||
		session.Status != models.SessionStatusOnSale ||
		event.Status != models.EventStatusPublished ||
		now.Before(session.SaleStartsAt) || now.After(session.SaleEndsAt) {
		return nil, nil, nil, nil, ErrTicketOrderUnavailable
	}
	if s.localCache != nil {
		s.localCache.Set(cacheKey, purchasableContext{
			Tier: tier, Session: session, Event: event, Venue: venue,
		}, purchasableContextLocalTTL)
	}
	return &tier, &session, &event, &venue, nil
}

func (s *TicketOrderService) markQueuedOrderFailed(
	ctx context.Context,
	orderID int64,
	reason string,
) error {
	return s.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("id = ? AND status = ?", orderID, models.TicketOrderStatusQueued).
		Updates(map[string]interface{}{
			"status":        models.TicketOrderStatusFailed,
			"cancel_reason": reason,
		}).Error
}

func (s *TicketOrderService) rollbackRedisQuota(ctx context.Context, tierID int64, quantity int, bucketNo *int) {
	if s.inventory.Enabled {
		b := 0
		if bucketNo != nil {
			b = *bucketNo
		}
		_ = s.rdb.IncrBy(ctx, TicketStockBucketKey(tierID, b), int64(quantity)).Err()
		return
	}
	_ = s.rdb.IncrBy(ctx, ticketStockKey(tierID), int64(quantity)).Err()
}

// reserveTicketStock Redis 预扣；分桶开启时按 userID%N 选桶并环形重试。
func (s *TicketOrderService) reserveTicketStock(
	ctx context.Context,
	userID int64,
	tier *models.TicketTier,
	quantity int,
) (bucketNo int, remaining int64, err error) {
	if !s.inventory.Enabled {
		remaining, err = reserveTicketQuotaScript.Run(
			ctx, s.rdb, []string{ticketStockKey(tier.ID)}, quantity,
		).Int64()
		return 0, remaining, err
	}
	n := s.inventory.EffectiveBucketCount(tier.TotalQuota)
	maxAttempts := 1 + s.inventory.BucketRetry
	if maxAttempts > n {
		maxAttempts = n
	}
	var last int64 = -1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		b := SelectBucketNo(userID, n, attempt)
		rem, runErr := reserveTicketQuotaScript.Run(
			ctx, s.rdb, []string{TicketStockBucketKey(tier.ID, b)}, quantity,
		).Int64()
		if runErr != nil {
			return 0, 0, runErr
		}
		if rem >= 0 {
			return b, rem, nil
		}
		last = rem
	}
	return 0, last, nil
}

func queuedOrderStockBucket(order *models.TicketOrder, settings InventoryBucketSettings) int {
	if order.StockBucketNo != nil {
		return *order.StockBucketNo
	}
	return SelectBucketNo(order.UserID, settings.BucketCount, 0)
}

func queuedOrderRushBucket(order *models.TicketOrder, settings InventoryBucketSettings) int {
	if order.RushBucketNo != nil {
		return *order.RushBucketNo
	}
	if order.StockBucketNo != nil {
		return *order.StockBucketNo
	}
	return SelectBucketNo(order.UserID, settings.BucketCount, 0)
}

func resolveMessageRushBucket(message TicketOrderMessage, settings InventoryBucketSettings) int {
	if message.RushBucketNo != nil {
		return *message.RushBucketNo
	}
	if message.StockBucketNo != nil {
		return *message.StockBucketNo
	}
	b, _ := ResolveOrderBucketNo(nil, message.UserID, settings.BucketCount)
	return b
}

func restoreTierQuota(tx *gorm.DB, tierID int64, quantity int) error {
	// 仅在售罄票档因归还而恢复可售；disabled 等人为下架状态保持不变。
	return tx.Model(&models.TicketTier{}).Where("id = ?", tierID).
		Updates(map[string]interface{}{
			"remaining_quota": gorm.Expr("remaining_quota + ?", quantity),
			"sold_count":      gorm.Expr("GREATEST(sold_count - ?, 0)", quantity),
			"status": gorm.Expr(
				"CASE WHEN status = ? THEN ? ELSE status END",
				models.TicketTierStatusSoldOut,
				models.TicketTierStatusOnSale,
			),
			"version": gorm.Expr("version + 1"),
		}).Error
}

func validatePurchaseInfo(input PurchaseInfoInput, quantity int, realNameRequired bool) error {
	contactName := strings.TrimSpace(input.ContactName)
	contactPhone := strings.TrimSpace(input.ContactPhone)
	if utf8.RuneCountInString(contactName) < 2 || utf8.RuneCountInString(contactName) > 64 {
		return fmt.Errorf("%w: 联系人姓名应为 2 到 64 个字符", ErrInvalidTicketCatalog)
	}
	if !mainlandPhonePattern.MatchString(contactPhone) {
		return fmt.Errorf("%w: 联系人手机号格式错误", ErrInvalidTicketCatalog)
	}
	if !input.TermsAccepted {
		return fmt.Errorf("%w: 请先阅读并同意购票须知", ErrInvalidTicketCatalog)
	}
	if !realNameRequired {
		if len(input.Attendees) > 0 {
			return fmt.Errorf("%w: 当前活动不需要实名观演人", ErrInvalidTicketCatalog)
		}
		return nil
	}
	if len(input.Attendees) != quantity {
		return fmt.Errorf("%w: 实名观演人数必须与购票数量一致", ErrInvalidTicketCatalog)
	}
	seen := make(map[string]struct{}, len(input.Attendees))
	for index, attendee := range input.Attendees {
		name := strings.TrimSpace(attendee.Name)
		idNumber := strings.ToUpper(strings.TrimSpace(attendee.IDNumber))
		if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 64 {
			return fmt.Errorf("%w: 第 %d 位观演人姓名格式错误", ErrInvalidTicketCatalog, index+1)
		}
		if attendee.IDType != "id_card" || !validMainlandIDCard(idNumber) {
			return fmt.Errorf("%w: 第 %d 位观演人身份证格式错误", ErrInvalidTicketCatalog, index+1)
		}
		if _, exists := seen[idNumber]; exists {
			return fmt.Errorf("%w: 同一证件不能重复绑定多张票", ErrInvalidTicketCatalog)
		}
		seen[idNumber] = struct{}{}
	}
	return nil
}

func validMainlandIDCard(value string) bool {
	if len(value) != 18 {
		return false
	}
	weights := [...]int{7, 9, 10, 5, 8, 4, 2, 1, 6, 3, 7, 9, 10, 5, 8, 4, 2}
	checks := "10X98765432"
	sum := 0
	for index := 0; index < 17; index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
		sum += int(value[index]-'0') * weights[index]
	}
	return value[17] == checks[sum%11]
}

func (s *TicketOrderService) buildAttendeeSnapshots(
	orderID int64,
	inputs []TicketAttendeeInput,
	realNameRequired bool,
) []models.TicketOrderAttendee {
	if !realNameRequired {
		return nil
	}
	attendees := make([]models.TicketOrderAttendee, 0, len(inputs))
	for index, input := range inputs {
		idNumber := strings.ToUpper(strings.TrimSpace(input.IDNumber))
		mac := hmac.New(sha256.New, s.identityHashKey)
		_, _ = fmt.Fprintf(mac, "attendee-id-v1:%d:%s", orderID, idNumber)
		attendees = append(attendees, models.TicketOrderAttendee{
			Base:           models.Base{ID: s.node.Generate().Int64()},
			OrderID:        orderID,
			SequenceNo:     index + 1,
			Name:           strings.TrimSpace(input.Name),
			IDType:         "id_card",
			IDNumberMasked: idNumber[:3] + "***********" + idNumber[len(idNumber)-4:],
			IDNumberHash:   fmt.Sprintf("%x", mac.Sum(nil)),
		})
	}
	return attendees
}

func ticketStockKey(tierID int64) string {
	return ticketStockKeyPrefix + strconv.FormatInt(tierID, 10)
}

func ticketOrderReceipt(order *models.TicketOrder) *TicketOrderReceipt {
	return &TicketOrderReceipt{
		OrderID: order.ID,
		OrderNo: order.OrderNo,
		Status:  order.Status,
	}
}
