package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"gofun/config"
	"gofun/container"
	"gofun/metrics"
	"gofun/models"
	"gofun/pkg/ws"
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

// Publisher 批量读取与兜底 tick 的默认值；它不是订单/库存 Batch Saga。
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
	outboxNotify chan struct{}
	inventory    InventoryBucketSettings
	orderEvents  *ws.Hub
}

func (s *TicketOrderService) ConfigureInventory(cfg config.InventoryConfig) {
	s.inventory = NewInventoryBucketSettings(cfg)
}

func (s *TicketOrderService) ConfigureOrderEvents(hub *ws.Hub) {
	s.orderEvents = hub
}

func (s *TicketOrderService) publishOrderEvent(
	userID, orderID int64,
	event string,
	status models.TicketOrderStatus,
	message string,
) {
	if s.orderEvents == nil {
		return
	}
	s.orderEvents.Publish(userID, ws.Event{
		Event:   event,
		OrderID: orderID,
		Status:  string(status),
		Message: message,
	})
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
	bucketDB := s.db.WithContext(ctx)
	for i := range tiers {
		if err := EnsureTierBuckets(bucketDB, &tiers[i], s.inventory); err != nil {
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
		if err := EnsureRushBuckets(bucketDB, &campaigns[i], s.inventory); err != nil {
			return err
		}
	}
	return nil
}

func NewTicketOrderService(c *container.Container, timeoutMinutes int, paymentCfg config.PaymentConfig) *TicketOrderService {
	if timeoutMinutes <= 0 {
		timeoutMinutes = 15
	}
	s := &TicketOrderService{
		db:               c.DB,
		rdb:              c.RDB,
		node:             c.SnowflakeNode,
		newMQChannel:     c.NewMQChannel,
		mqQueueName:      c.MQQueueName,
		mqQueueType:      c.MQQueueType,
		localCache:       c.LocalCache,
		paymentTimeout:   time.Duration(timeoutMinutes) * time.Minute,
		payment:          NewSandboxPaymentGateway(paymentCfg.SandboxSecret, time.Duration(paymentCfg.SandboxCallbackDelayMS)*time.Millisecond),
		credentialSigner: NewTicketCredentialSigner(c.TicketQRSecrets...),
		identityHashKey:  append([]byte(nil), c.TicketQRSecret...),
		scannerInterval:  2 * time.Minute,
		outboxNotify:     make(chan struct{}, 1),
	}
	if gateway, ok := s.payment.(*SandboxPaymentGateway); ok {
		gateway.SetCallback(s.HandlePaymentCallback)
	}
	return s
}

// RecoverPaymentState rebuilds the sandbox provider view from the durable
// payment transactions before background consumers and HTTP traffic start.
func (s *TicketOrderService) RecoverPaymentState(ctx context.Context) error {
	restorer, ok := s.payment.(PaymentStateRestorer)
	if !ok {
		return nil
	}
	var payments []models.PaymentTransaction
	if err := s.db.WithContext(ctx).
		Where("status IN ?", []models.PaymentTransactionStatus{
			models.PaymentTransactionPending,
			models.PaymentTransactionSuccess,
			models.PaymentTransactionFailed,
			models.PaymentTransactionClosed,
			models.PaymentTransactionRefunded,
		}).Find(&payments).Error; err != nil {
		return err
	}
	now := time.Now()
	for _, payment := range payments {
		if err := restorer.RestorePayment(PaymentStateRestoreRequest{
			PaymentNo: payment.PaymentNo, OrderID: payment.OrderID,
			UserID: payment.UserID, AmountCents: payment.AmountCents,
			Status: string(payment.Status),
		}); err != nil {
			return fmt.Errorf("restore payment %s: %w", payment.PaymentNo, err)
		}
		if payment.Status == models.PaymentTransactionPending && payment.ExpiresAt.After(now) {
			s.payment.ScheduleCallback(payment.PaymentNo, normalizePaymentScenario(payment.Scenario))
		}
	}
	return nil
}

func (s *TicketOrderService) restoreAndSchedulePayment(payment models.PaymentTransaction) error {
	if restorer, ok := s.payment.(PaymentStateRestorer); ok {
		if err := restorer.RestorePayment(PaymentStateRestoreRequest{
			PaymentNo: payment.PaymentNo, OrderID: payment.OrderID,
			UserID: payment.UserID, AmountCents: payment.AmountCents,
			Status: string(payment.Status),
		}); err != nil {
			return err
		}
	}
	s.payment.ScheduleCallback(payment.PaymentNo, normalizePaymentScenario(payment.Scenario))
	return nil
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
			bucketKey := fmt.Sprintf("%d:%d", bucket.TierID, bucket.BucketNo)
			available := bucket.RemainingQuota - queuedByTierBucket[bucketKey]
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
			bucketKey := fmt.Sprintf("%d:%d", bucket.CampaignID, bucket.BucketNo)
			available := bucket.RemainingQuota - queuedByCampaignBucket[bucketKey]
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
	ctx, span := otel.Tracer("gofun-ticketing/order").Start(
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
	var err error
	for attempt := 1; attempt <= ticketOrderTxMaxAttempts; attempt++ {
		enteredPending = false
		pendingOrderID = 0
		pendingUserID = 0
		txStarted := time.Now()
		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var order models.TicketOrder
			stageStarted := time.Now()
			orderQuery := tx.Select("id, user_id, status, rush_sale_campaign_id, stock_bucket_no, rush_bucket_no")
			orderQuery = orderQuery.Clauses(clause.Locking{Strength: "UPDATE"})
			if err := orderQuery.First(&order, message.OrderID).Error; err != nil {
				metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_lock").Observe(time.Since(stageStarted).Seconds())
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("%w: 订单凭据不存在", ErrTicketOrderNonRetryable)
				}
				return err
			}
			metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_lock").Observe(time.Since(stageStarted).Seconds())
			if order.Status != models.TicketOrderStatusQueued {
				return nil
			}
			var orderItems []models.TicketOrderItem
			stageStarted = time.Now()
			if err := tx.Select("ticket_tier_id, quantity").
				Where("order_id = ?", order.ID).
				Limit(2).
				Find(&orderItems).Error; err != nil {
				metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_items_read").Observe(time.Since(stageStarted).Seconds())
				return err
			}
			metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_items_read").Observe(time.Since(stageStarted).Seconds())
			if order.UserID != message.UserID || len(orderItems) != 1 ||
				orderItems[0].TicketTierID != message.TicketTierID ||
				orderItems[0].Quantity != message.Quantity {
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
				rushBucket := 0
				if order.RushSaleCampaignID != nil {
					rushBucket, _ = resolveRushBucketForOrder(&order, message.RushBucketNo, s.inventory, 0)
				}
				if order.RushSaleCampaignID != nil {
					bucketLabel := strconv.Itoa(rushBucket)
					stageStarted = time.Now()
					err := deductRushBucket(tx, *order.RushSaleCampaignID, message.TicketTierID, rushBucket, message.Quantity)
					metrics.TicketOrderConsumerStageDuration.WithLabelValues("rush_bucket_update").Observe(time.Since(stageStarted).Seconds())
					metrics.TicketOrderConsumerInventoryBucketDuration.WithLabelValues("rush", bucketLabel).Observe(time.Since(stageStarted).Seconds())
					bucketResult := "success"
					if err != nil {
						bucketResult = "error"
					}
					metrics.TicketOrderConsumerInventoryBucketOperations.WithLabelValues("rush", bucketLabel, bucketResult).Inc()
					if err != nil {
						return err
					}
				}
				bucketLabel := strconv.Itoa(stockBucket)
				stageStarted = time.Now()
				err := deductTierBucket(tx, message.TicketTierID, stockBucket, message.Quantity)
				metrics.TicketOrderConsumerStageDuration.WithLabelValues("tier_bucket_update").Observe(time.Since(stageStarted).Seconds())
				metrics.TicketOrderConsumerInventoryBucketDuration.WithLabelValues("tier", bucketLabel).Observe(time.Since(stageStarted).Seconds())
				bucketResult := "success"
				if err != nil {
					bucketResult = "error"
				}
				metrics.TicketOrderConsumerInventoryBucketOperations.WithLabelValues("tier", bucketLabel, bucketResult).Inc()
				if err != nil {
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
			orderUpdates := map[string]interface{}{
				"status":     models.TicketOrderStatusPendingPayment,
				"expires_at": expiresAt,
			}
			stageStarted = time.Now()
			result := tx.Model(&models.TicketOrder{}).
				Where("id = ? AND status = ?", order.ID, models.TicketOrderStatusQueued).
				Updates(orderUpdates)
			metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_state_update").Observe(time.Since(stageStarted).Seconds())
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
		transactionResult := "success"
		if err != nil {
			transactionResult = "error"
			if isRetryableMySQLTransactionError(err) && attempt < ticketOrderTxMaxAttempts {
				transactionResult = "retryable_error"
			}
		}
		metrics.TicketOrderConsumerTransactionDuration.WithLabelValues(transactionResult).Observe(time.Since(txStarted).Seconds())
		metrics.TicketOrderConsumerTransactions.WithLabelValues(transactionResult).Inc()
		if err == nil || !isRetryableMySQLTransactionError(err) ||
			attempt == ticketOrderTxMaxAttempts {
			break
		}
		delay := time.Duration(attempt*10+int(message.OrderID%7)) * time.Millisecond
		log.Printf(
			"ticket order consumer transaction retry: order=%d attempt=%d delay=%s err=%v",
			message.OrderID,
			attempt,
			delay,
			err,
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
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
		s.publishOrderEvent(
			pendingUserID,
			pendingOrderID,
			"pending_payment",
			models.TicketOrderStatusPendingPayment,
			"票额确认成功，请在有效期内完成支付",
		)
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
		s.publishOrderEvent(
			message.UserID,
			message.OrderID,
			"failed",
			models.TicketOrderStatusFailed,
			"订单确认失败，预占票额已释放",
		)
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
	scenario string,
) (*PaymentIntent, error) {
	var order models.TicketOrder
	if err := s.db.WithContext(ctx).Preload("Items").
		Where("id = ? AND user_id = ?", orderID, userID).
		First(&order).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTicketOrderNotFound
		}
		return nil, err
	}
	if order.Status != models.TicketOrderStatusPendingPayment || time.Now().After(order.ExpiresAt) {
		return nil, ErrTicketOrderState
	}
	if len(order.Items) == 0 {
		return nil, fmt.Errorf("订单明细异常")
	}

	// Retry an unfinished payment instead of creating a second provider charge.
	var pending models.PaymentTransaction
	if err := s.db.WithContext(ctx).
		Where("order_id = ? AND status = ?", orderID, models.PaymentTransactionPending).
		Order("id DESC").First(&pending).Error; err == nil {
		if err := s.restoreAndSchedulePayment(pending); err != nil {
			return nil, err
		}
		return &PaymentIntent{
			PaymentNo: pending.PaymentNo, Provider: pending.Provider,
			Status: string(pending.Status), AmountCents: pending.AmountCents,
			ExpiresAt: pending.ExpiresAt,
		}, nil
	}

	intent, err := s.payment.CreatePayment(ctx, PaymentCreateRequest{
		OrderID: order.ID, UserID: userID, AmountCents: order.TotalAmountCents,
		ExpiresAt: order.ExpiresAt, Scenario: scenario,
	})
	if err != nil {
		return nil, err
	}
	var scheduledPayment models.PaymentTransaction
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked models.TicketOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", orderID, userID).First(&locked).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTicketOrderNotFound
			}
			return err
		}
		if locked.Status != models.TicketOrderStatusPendingPayment || time.Now().After(locked.ExpiresAt) {
			return ErrTicketOrderState
		}
		var active models.PaymentTransaction
		if err := tx.Where("order_id = ? AND status = ?", orderID, models.PaymentTransactionPending).
			First(&active).Error; err == nil {
			scheduledPayment = active
			intent.PaymentNo = active.PaymentNo
			intent.Provider = active.Provider
			intent.Status = string(active.Status)
			intent.AmountCents = active.AmountCents
			intent.ExpiresAt = active.ExpiresAt
			return nil
		}
		scheduledPayment = models.PaymentTransaction{
			PaymentNo: intent.PaymentNo, OrderID: orderID, UserID: userID,
			Provider: intent.Provider, ProviderPaymentID: intent.PaymentNo,
			AmountCents: intent.AmountCents, Status: models.PaymentTransactionPending,
			Scenario: normalizePaymentScenario(scenario), ExpiresAt: order.ExpiresAt,
		}
		return tx.Create(&scheduledPayment).Error
	})
	if err != nil {
		return nil, err
	}
	if err := s.restoreAndSchedulePayment(scheduledPayment); err != nil {
		return nil, err
	}
	return intent, nil
}

// HandlePaymentCallback is the same state transition a real provider webhook
// would invoke. The callback is authenticated and idempotent before tickets
// are issued.
func (s *TicketOrderService) HandlePaymentCallback(
	ctx context.Context,
	notification PaymentNotification,
) error {
	startedAt := time.Now()
	callbackResult := "error"
	providerMetric := notification.Provider
	if providerMetric != "sandbox" {
		providerMetric = "unknown"
	}
	defer func() {
		metrics.PaymentCallbackDuration.WithLabelValues(providerMetric).Observe(time.Since(startedAt).Seconds())
		metrics.PaymentCallbacksTotal.WithLabelValues(providerMetric, callbackResult).Inc()
	}()
	if !s.payment.VerifyNotification(notification) {
		callbackResult = "signature_invalid"
		return ErrPaymentSignature
	}
	payload, _ := json.Marshal(notification)
	var paid bool
	var eventUserID, eventOrderID int64
	var eventName, eventMessage string
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var payment models.PaymentTransaction
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("payment_no = ?", notification.PaymentNo).First(&payment).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrPaymentNotFound
			}
			return err
		}
		if payment.AmountCents != notification.AmountCents {
			return ErrPaymentAmountMismatch
		}
		if payment.Provider != notification.Provider {
			return ErrPaymentInvalidNotification
		}
		var callback models.PaymentCallback
		if err := tx.Where("provider_event_id = ?", notification.ProviderEventID).
			First(&callback).Error; err == nil {
			return nil
		}
		callback = models.PaymentCallback{
			ProviderEventID: notification.ProviderEventID,
			PaymentNo:       notification.PaymentNo,
			Provider:        notification.Provider,
			Status:          notification.Status,
			AmountCents:     notification.AmountCents,
			Payload:         string(payload),
		}
		if err := tx.Create(&callback).Error; err != nil {
			return err
		}
		if payment.Status != models.PaymentTransactionPending {
			return nil
		}
		var order models.TicketOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Items").Where("id = ?", payment.OrderID).First(&order).Error; err != nil {
			return err
		}
		if notification.Status != "success" {
			reason := notification.FailureReason
			if reason == "" {
				reason = "payment provider rejected the transaction"
			}
			if err := tx.Model(&models.PaymentTransaction{}).Where("id = ?", payment.ID).
				Updates(map[string]interface{}{
					"status":         models.PaymentTransactionFailed,
					"failure_reason": reason,
				}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.TicketOrder{}).Where("id = ? AND status = ?", payment.OrderID, models.TicketOrderStatusPendingPayment).
				Update("payment_status", models.PaymentStatusFailed).Error; err != nil {
				return err
			}
			eventUserID, eventOrderID = payment.UserID, payment.OrderID
			eventName, eventMessage = "payment_failed", reason
			now := time.Now()
			return tx.Model(&models.PaymentCallback{}).Where("id = ?", callback.ID).
				Update("processed_at", &now).Error
		}

		if order.Status != models.TicketOrderStatusPendingPayment || time.Now().After(order.ExpiresAt) {
			if err := tx.Model(&models.PaymentTransaction{}).Where("id = ?", payment.ID).
				Update("status", models.PaymentTransactionClosed).Error; err != nil {
				return err
			}
			now := time.Now()
			return tx.Model(&models.PaymentCallback{}).Where("id = ?", callback.ID).
				Update("processed_at", &now).Error
		}
		now := time.Now()
		if err := s.issueAdmissionTickets(tx, &order, now); err != nil {
			return err
		}
		if err := tx.Model(&models.TicketOrder{}).Where("id = ?", order.ID).
			Updates(map[string]interface{}{
				"status":         models.TicketOrderStatusPaid,
				"payment_status": models.PaymentStatusPaid,
				"paid_at":        &now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.PaymentTransaction{}).Where("id = ?", payment.ID).
			Updates(map[string]interface{}{
				"status":  models.PaymentTransactionSuccess,
				"paid_at": &now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.PaymentCallback{}).Where("id = ?", callback.ID).
			Update("processed_at", &now).Error; err != nil {
			return err
		}
		paid = true
		eventUserID, eventOrderID = order.UserID, order.ID
		eventName, eventMessage = "paid", "支付成功，电子票已签发"
		return nil
	})
	if err == nil && eventName != "" {
		if paid {
			callbackResult = "success"
		} else {
			callbackResult = "failed"
		}
		status := models.TicketOrderStatusPendingPayment
		if paid {
			status = models.TicketOrderStatusPaid
		}
		s.publishOrderEvent(eventUserID, eventOrderID, eventName, status, eventMessage)
	}
	if err == nil && eventName == "" {
		callbackResult = "duplicate_or_ignored"
	}
	if errors.Is(err, ErrPaymentAmountMismatch) {
		callbackResult = "amount_mismatch"
	}
	if errors.Is(err, ErrPaymentNotFound) {
		callbackResult = "not_found"
	}
	return err
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
	var refundPaymentNo string
	var rushCampaignID *int64
	var stockBucketNo *int
	var rushBucketNo *int
	paid := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var payment models.PaymentTransaction
		paymentErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_id = ? AND status IN ?", orderID, []models.PaymentTransactionStatus{
				models.PaymentTransactionPending,
				models.PaymentTransactionSuccess,
			}).Order("id DESC").First(&payment).Error
		if paymentErr != nil && !errors.Is(paymentErr, gorm.ErrRecordNotFound) {
			return paymentErr
		}
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
			if errors.Is(paymentErr, gorm.ErrRecordNotFound) {
				return ErrPaymentNotFound
			}
			if order.PaymentStatus != models.PaymentStatusPaid &&
				order.PaymentStatus != models.PaymentStatusRefunding {
				return ErrTicketOrderState
			}
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
			refundPaymentNo = payment.PaymentNo
			paid = true
			if order.PaymentStatus == models.PaymentStatusPaid {
				if err := tx.Model(&models.TicketOrder{}).
					Where("id = ? AND status = ? AND payment_status = ?", order.ID,
						models.TicketOrderStatusPaid, models.PaymentStatusPaid).
					Update("payment_status", models.PaymentStatusRefunding).Error; err != nil {
					return err
				}
			}
			return nil
		}
		if s.inventory.Enabled {
			var releaseErr error
			stockBucketNo, rushBucketNo, releaseErr = s.releaseInventoryForOrder(tx, &order)
			if releaseErr != nil {
				return releaseErr
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
		if paymentErr == nil && payment.Status == models.PaymentTransactionPending {
			if err := tx.Model(&models.PaymentTransaction{}).
				Where("id = ? AND status = ?", payment.ID, models.PaymentTransactionPending).
				Update("status", models.PaymentTransactionClosed).Error; err != nil {
				return err
			}
		}
		return tx.Model(&models.TicketOrder{}).Where("id = ?", orderID).
			Updates(map[string]interface{}{
				"status":        models.TicketOrderStatusCancelled,
				"cancelled_at":  &now,
				"cancel_reason": strings.TrimSpace(reason),
			}).Error
	})
	if err != nil {
		return err
	}
	if paid {
		if err := s.payment.Refund(ctx, refundPaymentNo, refundCents); err != nil {
			_ = s.db.WithContext(ctx).Model(&models.TicketOrder{}).
				Where("id = ? AND status = ? AND payment_status = ?", orderID,
					models.TicketOrderStatusPaid, models.PaymentStatusRefunding).
				Update("payment_status", models.PaymentStatusPaid).Error
			return err
		}

		err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var payment models.PaymentTransaction
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("payment_no = ?", refundPaymentNo).First(&payment).Error; err != nil {
				return err
			}
			var order models.TicketOrder
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Preload("Items").Where("id = ? AND user_id = ?", orderID, userID).
				First(&order).Error; err != nil {
				return err
			}
			if order.Status != models.TicketOrderStatusPaid ||
				(order.PaymentStatus != models.PaymentStatusRefunding &&
					order.PaymentStatus != models.PaymentStatusPaid) {
				return ErrTicketOrderState
			}
			if s.inventory.Enabled {
				var releaseErr error
				stockBucketNo, rushBucketNo, releaseErr = s.releaseInventoryForOrder(tx, &order)
				if releaseErr != nil {
					return releaseErr
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
			if err := tx.Model(&models.PaymentTransaction{}).
				Where("id = ? AND status IN ?", payment.ID, []models.PaymentTransactionStatus{
					models.PaymentTransactionSuccess,
					models.PaymentTransactionRefunded,
				}).Updates(map[string]interface{}{
				"status":      models.PaymentTransactionRefunded,
				"refunded_at": &now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.AdmissionTicket{}).
				Where("order_id = ? AND status = ?", order.ID, models.AdmissionTicketStatusValid).
				Updates(map[string]interface{}{
					"status":        models.AdmissionTicketStatusRevoked,
					"revoked_at":    &now,
					"revoke_reason": strings.TrimSpace(reason),
				}).Error; err != nil {
				return err
			}
			return tx.Model(&models.TicketOrder{}).
				Where("id = ? AND status = ?", order.ID, models.TicketOrderStatusPaid).
				Updates(map[string]interface{}{
					"status":         models.TicketOrderStatusCancelled,
					"payment_status": models.PaymentStatusRefunded,
					"cancelled_at":   &now,
					"cancel_reason":  strings.TrimSpace(reason),
				}).Error
		})
		if err != nil {
			return err
		}
	}

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
		message := "订单已取消，票额已释放"
		if refundCents > 0 {
			message = "退款完成，电子票已作废"
		}
		s.publishOrderEvent(
			userID,
			orderID,
			"cancelled",
			models.TicketOrderStatusCancelled,
			message,
		)
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
	txStarted := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pendingPayment models.PaymentTransaction
		paymentErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("order_id = ? AND status = ?", orderID, models.PaymentTransactionPending).
			Order("id DESC").First(&pendingPayment).Error
		if paymentErr != nil && !errors.Is(paymentErr, gorm.ErrRecordNotFound) {
			return paymentErr
		}
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
			var releaseErr error
			stockBucketNo, rushBucketNo, releaseErr = s.releaseInventoryForOrder(tx, &order)
			if releaseErr != nil {
				return releaseErr
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
		if paymentErr == nil {
			if err := tx.Model(&models.PaymentTransaction{}).
				Where("id = ? AND status = ?", pendingPayment.ID, models.PaymentTransactionPending).
				Update("status", models.PaymentTransactionClosed).Error; err != nil {
				return err
			}
		}
		return nil
	})
	txResult := "success"
	if err != nil {
		txResult = "error"
	}
	metrics.TicketPaymentTimeoutTransactionDuration.WithLabelValues(txResult).Observe(time.Since(txStarted).Seconds())
	metrics.TicketPaymentTimeoutTransactions.WithLabelValues(txResult).Inc()
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
		s.publishOrderEvent(
			userID,
			orderID,
			"timeout_cancelled",
			models.TicketOrderStatusCancelled,
			"订单支付超时，票额已自动释放",
		)
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
