package service

import (
	"context"
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
	"sort"
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
	"golang.org/x/sync/singleflight"
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

type TicketAttendeeInput struct {
	Name      string     `json:"name"`
	IDType    string     `json:"id_type"`
	IDNumber  string     `json:"id_number"`
	ProfileID FlexibleID `json:"profile_id"`
}

type PurchaseInfoInput struct {
	ContactName        string                `json:"contact_name"`
	ContactPhone       string                `json:"contact_phone"`
	TermsAccepted      bool                  `json:"terms_accepted"`
	Attendees          []TicketAttendeeInput `json:"attendees"`
	AttendeeProfileIDs []FlexibleID          `json:"attendee_profile_ids"`
}

type CreateTicketOrderInput struct {
	TicketTierID int64        `json:"ticket_tier_id,string" binding:"required"`
	Quantity     int          `json:"quantity"`
	SeatIDs      []FlexibleID `json:"seat_ids"`
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
	SeatIDs            []int64           `json:"seat_ids,omitempty"`
	TraceContext       map[string]string `json:"trace_context,omitempty"`
}

type TicketOrderService struct {
	db *gorm.DB
	// workerDB 供 Consumer/Outbox/Timeout 等后台路径；未隔离时与 db 相同。
	workerDB          *gorm.DB
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
	queryCacheSF singleflight.Group
	// faultInjector 仅由同包集成测试设置，运行时默认 nil。
	faultInjector TicketFaultInjector
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
	// List/detail cache: bump only on terminal states. Create paths already
	// invalidate; pending/queued churn can wait for short TTL.
	switch status {
	case models.TicketOrderStatusPaid,
		models.TicketOrderStatusCancelled,
		models.TicketOrderStatusFailed:
		s.invalidateUserOrderQueryCache(context.Background(), userID)
	}
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
	db := s.asyncDB()
	seatedTiers, err := seatedTicketTierIDs(db.WithContext(ctx))
	if err != nil {
		return err
	}
	var tiers []models.TicketTier
	if err := db.WithContext(ctx).Find(&tiers).Error; err != nil {
		return err
	}
	bucketDB := db.WithContext(ctx)
	for i := range tiers {
		if _, skip := seatedTiers[tiers[i].ID]; skip {
			continue
		}
		if err := EnsureTierBuckets(bucketDB, &tiers[i], s.inventory); err != nil {
			return err
		}
	}
	var campaigns []models.RushSaleCampaign
	if err := db.WithContext(ctx).
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
	workerDB := c.WorkerDB
	if workerDB == nil {
		workerDB = c.DB
	}
	s := &TicketOrderService{
		db:               c.DB,
		workerDB:         workerDB,
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

// asyncDB 返回后台路径使用的 DB（Consumer / Outbox / Timeout / 启动暖库）。
func (s *TicketOrderService) asyncDB() *gorm.DB {
	if s.workerDB != nil {
		return s.workerDB
	}
	return s.db
}

// RecoverPaymentState rebuilds the sandbox provider view from the durable
// payment transactions before background consumers and HTTP traffic start.
func (s *TicketOrderService) RecoverPaymentState(ctx context.Context) error {
	restorer, ok := s.payment.(PaymentStateRestorer)
	if !ok {
		return nil
	}
	var payments []models.PaymentTransaction
	if err := s.asyncDB().WithContext(ctx).
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
			PaymentNo: payment.PaymentNo, OrderID: payment.OrderID, WaitlistID: payment.WaitlistID,
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
			PaymentNo: payment.PaymentNo, OrderID: payment.OrderID, WaitlistID: payment.WaitlistID,
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
	db := s.asyncDB()
	// 只预热在售票档：disabled/已取消活动的票档不重建 Redis 库存，
	// 避免活动取消后重启服务导致抢票/购票重新可用。
	var tiers []models.TicketTier
	if err := db.WithContext(ctx).
		Where("status = ?", models.TicketTierStatusOnSale).
		Find(&tiers).Error; err != nil {
		return err
	}
	occ, err := loadQueuedStockOccupancy(ctx, db, s.inventory)
	if err != nil {
		return err
	}
	queuedByTier := occ.byTier
	queuedByCampaign := occ.byCampaign
	queuedByTierBucket := occ.byTierBucket
	queuedByCampaignBucket := occ.byCampaignBucket
	values := make(map[string]interface{})
	if s.inventory.Enabled {
		tierIDs := make([]int64, 0, len(tiers))
		for _, tier := range tiers {
			tierIDs = append(tierIDs, tier.ID)
		}
		var tierBuckets []models.TicketTierBucket
		if len(tierIDs) > 0 {
			if err := db.WithContext(ctx).
				Where("tier_id IN ?", tierIDs).
				Find(&tierBuckets).Error; err != nil {
				return err
			}
		}
		pendingByTier := make(map[int64]int, len(tiers))
		for _, tier := range tiers {
			pendingByTier[tier.ID] = tier.WaitlistPending
		}
		for _, bucket := range tierBuckets {
			bucketKey := fmt.Sprintf("%d:%d", bucket.TierID, bucket.BucketNo)
			available := publicRedisExpected(
				bucket.RemainingQuota,
				pendingByTier[bucket.TierID],
				queuedByTierBucket[bucketKey],
				true,
			)
			values[TicketStockBucketKey(bucket.TierID, bucket.BucketNo)] = available
		}
	} else {
		for _, tier := range tiers {
			available := publicRedisExpected(tier.RemainingQuota, tier.WaitlistPending, queuedByTier[tier.ID], false)
			values[ticketStockKey(tier.ID)] = available
		}
	}
	var campaigns []models.RushSaleCampaign
	if err := db.WithContext(ctx).
		Where("status IN ?", []models.RushSaleStatus{
			models.RushSaleStatusScheduled,
			models.RushSaleStatusActive,
		}).Find(&campaigns).Error; err != nil {
		return err
	}
	if s.inventory.Enabled {
		campaignIDs := make([]int64, 0, len(campaigns))
		for _, campaign := range campaigns {
			campaignIDs = append(campaignIDs, campaign.ID)
		}
		var rushBuckets []models.RushCampaignBucket
		if len(campaignIDs) > 0 {
			if err := db.WithContext(ctx).
				Where("campaign_id IN ?", campaignIDs).
				Find(&rushBuckets).Error; err != nil {
				return err
			}
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
	// Aggregate in SQL instead of Preload(Items) on every active rush order.
	// Sweep / long-lived envs can accumulate tens of thousands of rows; GORM
	// Preload would expand into a prepared statement with too many placeholders.
	type rushUserPurchased struct {
		CampaignID int64 `gorm:"column:campaign_id"`
		UserID     int64 `gorm:"column:user_id"`
		Quantity   int   `gorm:"column:quantity"`
	}
	var purchasedRows []rushUserPurchased
	if err := db.WithContext(ctx).Raw(`
		SELECT o.rush_sale_campaign_id AS campaign_id,
		       o.user_id AS user_id,
		       COALESCE(SUM(i.quantity), 0) AS quantity
		FROM ticket_order o
		INNER JOIN ticket_order_item i ON i.order_id = o.id
		WHERE o.delete_time IS NULL
		  AND i.delete_time IS NULL
		  AND o.rush_sale_campaign_id IS NOT NULL
		  AND o.status IN (?, ?, ?)
		GROUP BY o.rush_sale_campaign_id, o.user_id
	`, models.TicketOrderStatusQueued, models.TicketOrderStatusPendingPayment, models.TicketOrderStatusPaid).
		Scan(&purchasedRows).Error; err != nil {
		return err
	}
	for _, row := range purchasedRows {
		values[rushUserCountKey(row.CampaignID, row.UserID)] = row.Quantity
	}
	pendingKeys, err := s.pendingStockReservationKeys(ctx)
	if err != nil {
		return fmt.Errorf("读取在途库存预扣: %w", err)
	}
	if len(pendingKeys) > 0 {
		filtered := make(map[string]interface{}, len(values))
		for key, value := range values {
			if _, skip := pendingKeys[key]; skip {
				continue
			}
			filtered[key] = value
		}
		values = filtered
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
	if userID <= 0 || input.TicketTierID <= 0 ||
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
	seatIDs := []int64(nil)
	items := []models.TicketOrderItem{{
		TicketTierID:            tier.ID,
		Quantity:                input.Quantity,
		UnitPriceCents:          tier.PriceCents,
		EventTitleSnapshot:      event.Title,
		SessionStartsAtSnapshot: session.StartsAt,
		VenueNameSnapshot:       venue.Name,
		VenueAddressSnapshot:    venue.Address,
		TierNameSnapshot:        tier.Name,
	}}
	totalCents := tier.PriceCents * int64(input.Quantity)
	if event.SaleMode.IsSeated() {
		seatIDs, err = parseSeatIDs(input.SeatIDs)
		if err != nil {
			return nil, err
		}
		plan, planErr := s.buildSeatedItems(ctx, session, event, venue, seatIDs)
		if planErr != nil {
			return nil, planErr
		}
		items = plan
		input.Quantity = 0
		totalCents = 0
		for _, item := range items {
			input.Quantity += item.Quantity
			totalCents += item.UnitPriceCents * int64(item.Quantity)
		}
		tier = &models.TicketTier{Base: models.Base{ID: items[0].TicketTierID}, PriceCents: items[0].UnitPriceCents, Name: items[0].TierNameSnapshot}
	} else if len(input.SeatIDs) > 0 {
		return nil, fmt.Errorf("%w: 计数活动不能选座", ErrInvalidTicketCatalog)
	}
	if input.Quantity <= 0 {
		return nil, ErrInvalidTicketCatalog
	}
	if input.Quantity > event.MaxTicketsPerOrder {
		return nil, fmt.Errorf("%w: 超过账号限购", ErrTicketOrderUnavailable)
	}
	if !event.SaleMode.IsSeated() && input.Quantity > tier.PurchaseLimit {
		return nil, fmt.Errorf("%w: 超过限购数量", ErrTicketOrderUnavailable)
	}
	used, err := s.countUserEventTickets(ctx, userID, event.ID)
	if err != nil {
		return nil, err
	}
	if used+input.Quantity > event.MaxTicketsPerOrder {
		return nil, fmt.Errorf("%w: 本场每账号限购 %d 张", ErrTicketOrderUnavailable, event.MaxTicketsPerOrder)
	}
	if err := s.expandAttendeeProfiles(ctx, userID, &input); err != nil {
		return nil, err
	}
	if err := validatePurchaseInfo(input.PurchaseInfoInput, input.Quantity, event.RealNameRequired); err != nil {
		return nil, err
	}
	if err := s.assertEventIdentitiesFree(ctx, event.ID, input.Attendees); err != nil {
		return nil, err
	}
	if !event.SaleMode.IsSeated() && tier.RemainingQuota-tier.WaitlistPending < input.Quantity {
		return nil, ErrTicketQuotaInsufficient
	}

	proposedOrderID := s.node.Generate().Int64()
	var reservation stockReservationResult
	var bucketNo int
	if !event.SaleMode.IsSeated() {
		var reserveCode int64
		reservation, reserveCode, err = s.reserveTicketStock(
			ctx, userID, tier, input.Quantity, idempotencyKey, proposedOrderID,
		)
		if err != nil || reserveCode < 0 {
			if reserveCode == -2 {
				return nil, fmt.Errorf("%w: 暂时无法购票，请稍后重试", ErrTicketOrderUnavailable)
			}
			if reserveCode == -1 {
				return nil, ErrTicketQuotaInsufficient
			}
			if reserveCode == stockReservationCodeConflict {
				return nil, fmt.Errorf("%w: 幂等键已用于其他购票请求", ErrInvalidTicketCatalog)
			}
			return nil, fmt.Errorf("预扣票额: %w", err)
		}
		bucketNo = reservation.BucketNo
	} else {
		reservation = stockReservationResult{OrderID: proposedOrderID}
	}

	orderID := reservation.OrderID
	if err := s.injectTicketFault(ctx, FaultAfterRedisReserve, orderID); err != nil {
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
		OrderSource:           models.TicketOrderSourceNormal,
		Status:                models.TicketOrderStatusQueued,
		PaymentStatus:         models.PaymentStatusUnpaid,
		TotalAmountCents:      totalCents,
		ContactName:           strings.TrimSpace(input.ContactName),
		ContactPhone:          strings.TrimSpace(input.ContactPhone),
		RealNameRequired:      event.RealNameRequired,
		PurchaseNoticeVersion: purchaseNoticeVersion,
		TermsAcceptedAt:       &acceptedAt,
		IdempotencyKey:        idempotencyKey,
		RequestID:             strings.TrimSpace(requestID),
		ExpiresAt:             time.Now().Add(s.paymentTimeout),
		Items:                 items,
	}
	if s.inventory.Enabled && !event.SaleMode.IsSeated() {
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
		SeatIDs:      seatIDs,
	}
	if s.inventory.Enabled && !event.SaleMode.IsSeated() {
		message.StockBucketNo = intPtr(bucketNo)
	}
	if err := s.createOrderAndOutbox(ctx, order, message); err != nil {
		// COMMIT 结果可能因连接中断而未知：先用独立 Context 查幂等订单，
		// 查不到时还必须先竞争 MySQL 恢复栅栏，不能直接回滚 Redis。
		recoveryCtx, cancel := detachedReservationContext(ctx)
		defer cancel()
		var existing models.TicketOrder
		lookupErr := s.db.WithContext(recoveryCtx).
			Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
			First(&existing).Error
		if lookupErr == nil {
			receipt := ticketOrderReceipt(&existing)
			if !event.SaleMode.IsSeated() {
				s.confirmStockReservation(recoveryCtx, reservation)
			}
			s.rememberIdempotentOrder(recoveryCtx, userID, idempotencyKey, receipt)
			return receipt, nil
		}
		if event.SaleMode.IsSeated() {
			return nil, err
		}
		if errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			outcome, recoveryErr := s.resolveMissingOrderStockReservation(recoveryCtx, reservation)
			if recoveryErr != nil {
				return nil, errors.Join(err, fmt.Errorf("恢复 Redis 预扣: %w", recoveryErr))
			}
			if outcome == stockReservationRecoveryConfirmed {
				if lookupErr := s.db.WithContext(recoveryCtx).
					Where("id = ? AND user_id = ?", reservation.OrderID, userID).
					First(&existing).Error; lookupErr != nil {
					return nil, errors.Join(err, fmt.Errorf("恢复后读取订单: %w", lookupErr))
				}
				receipt := ticketOrderReceipt(&existing)
				s.rememberIdempotentOrder(recoveryCtx, userID, idempotencyKey, receipt)
				return receipt, nil
			}
		}
		return nil, err
	}
	if err := s.injectTicketFault(ctx, FaultAfterOrderCommit, orderID); err != nil {
		return nil, err
	}
	if !event.SaleMode.IsSeated() {
		s.confirmStockReservation(ctx, reservation)
	}
	s.notifyOutboxPublisher()
	s.invalidateUserOrderQueryCache(ctx, userID)
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
		enteredPending    bool
		pendingOrderID    int64
		pendingUserID     int64
		pendingAcceptedAt time.Time
	)
	var err error
	for attempt := 1; attempt <= ticketOrderTxMaxAttempts; attempt++ {
		enteredPending = false
		pendingOrderID = 0
		pendingUserID = 0
		pendingAcceptedAt = time.Time{}
		txStarted := time.Now()
		err = s.processOrderTaskTx(
			ctx, message, &enteredPending, &pendingOrderID, &pendingUserID, &pendingAcceptedAt,
		)
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
		if !pendingAcceptedAt.IsZero() {
			latency := time.Since(pendingAcceptedAt).Seconds()
			if latency >= 0 {
				metrics.TicketOrderAcceptedToPendingPaymentDuration.Observe(latency)
			}
		}
		if pubErr := s.PublishPaymentTimeout(ctx, pendingOrderID, pendingUserID); pubErr != nil {
			log.Printf("ticket payment timeout publish order %d: %v", pendingOrderID, pubErr)
		}
		s.publishOrderEvent(
			pendingUserID,
			pendingOrderID,
			"pending_payment",
			models.TicketOrderStatusPendingPayment,
			"订单已确认，请尽快完成支付",
		)
	}
	return nil
}

// processOrderTaskTx 用显式 Begin/Commit，把 COMMIT 与 SQL 阶段分开计时。
func (s *TicketOrderService) processOrderTaskTx(
	ctx context.Context,
	message TicketOrderMessage,
	enteredPending *bool,
	pendingOrderID, pendingUserID *int64,
	pendingAcceptedAt *time.Time,
) (err error) {
	tx := s.asyncDB().WithContext(ctx).Begin()
	if tx.Error != nil {
		return tx.Error
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var order models.TicketOrder
	stageStarted := time.Now()
	orderQuery := tx.Select("id, user_id, status, rush_sale_campaign_id, stock_bucket_no, rush_bucket_no, create_time")
	orderQuery = orderQuery.Clauses(clause.Locking{Strength: "UPDATE"})
	if err = orderQuery.First(&order, message.OrderID).Error; err != nil {
		metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_lock").Observe(time.Since(stageStarted).Seconds())
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: 订单凭据不存在", ErrTicketOrderNonRetryable)
		}
		return err
	}
	metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_lock").Observe(time.Since(stageStarted).Seconds())
	if order.Status != models.TicketOrderStatusQueued {
		commitStarted := time.Now()
		err = tx.Commit().Error
		metrics.TicketOrderConsumerStageDuration.WithLabelValues("commit").Observe(time.Since(commitStarted).Seconds())
		return err
	}

	var orderItems []models.TicketOrderItem
	stageStarted = time.Now()
	if err = tx.Select("ticket_tier_id, quantity").
		Where("order_id = ?", order.ID).
		Find(&orderItems).Error; err != nil {
		metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_items_read").Observe(time.Since(stageStarted).Seconds())
		return err
	}
	metrics.TicketOrderConsumerStageDuration.WithLabelValues("order_items_read").Observe(time.Since(stageStarted).Seconds())
	if order.UserID != message.UserID || len(orderItems) == 0 {
		return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
	}
	itemQty := 0
	for _, item := range orderItems {
		itemQty += item.Quantity
	}
	if itemQty != message.Quantity {
		return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
	}
	if len(message.SeatIDs) == 0 && (len(orderItems) != 1 ||
		orderItems[0].TicketTierID != message.TicketTierID) {
		return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
	}
	if (order.RushSaleCampaignID == nil) != (message.RushSaleCampaignID == nil) {
		return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
	}
	if order.RushSaleCampaignID != nil &&
		*order.RushSaleCampaignID != *message.RushSaleCampaignID {
		return fmt.Errorf("%w: 消息与订单凭据不一致", ErrTicketOrderNonRetryable)
	}

	seatedCount, seatedErr := sessionSeatsHeldOrSold(tx, order.ID)
	if seatedErr != nil {
		return seatedErr
	}
	if seatedCount > 0 {
		if seatedCount != int64(message.Quantity) {
			return fmt.Errorf("%w: 座位占用与订单数量不一致", ErrTicketOrderNonRetryable)
		}
	} else if s.inventory.Enabled {
		stockBucket, _ := resolveStockBucketForOrder(&order, message.StockBucketNo, s.inventory, 0)
		rushBucket := 0
		if order.RushSaleCampaignID != nil {
			rushBucket, _ = resolveRushBucketForOrder(&order, message.RushBucketNo, s.inventory, 0)
		}
		if order.RushSaleCampaignID != nil {
			bucketLabel := strconv.Itoa(rushBucket)
			stageStarted = time.Now()
			err = deductRushBucket(tx, *order.RushSaleCampaignID, message.TicketTierID, rushBucket, message.Quantity)
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
		err = deductTierBucket(tx, message.TicketTierID, stockBucket, message.Quantity)
		// deductTierBucket 内部还会记录 remaining_read / sold_out_refresh。
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
		*enteredPending = true
		*pendingOrderID = order.ID
		*pendingUserID = order.UserID
		*pendingAcceptedAt = order.CreateTime
	}

	commitStarted := time.Now()
	err = tx.Commit().Error
	metrics.TicketOrderConsumerStageDuration.WithLabelValues("commit").Observe(time.Since(commitStarted).Seconds())
	return err
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
	released := 0
	updated := false
	err := s.asyncDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := tx.Model(&models.TicketOrder{}).
			Where("id = ? AND status = ?", message.OrderID, models.TicketOrderStatusQueued).
			Updates(map[string]interface{}{
				"status":        models.TicketOrderStatusFailed,
				"cancel_reason": reason,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		updated = true
		count, releaseErr := releaseSessionSeats(tx, message.OrderID)
		if releaseErr != nil {
			return releaseErr
		}
		released = count
		return nil
	})
	if err != nil {
		log.Printf("ticket order finalize failed message: order=%d err=%v", message.OrderID, err)
		return
	}
	if !updated {
		return
	}
	if released == 0 {
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
	s.publishOrderEvent(
		message.UserID,
		message.OrderID,
		"failed",
		models.TicketOrderStatusFailed,
		"订单未能确认，门票已退回",
	)
}

func (s *TicketOrderService) GetOrder(
	ctx context.Context,
	userID, orderID int64,
) (*models.TicketOrder, error) {
	version := s.orderListVersion(ctx, userID)
	cacheKey := orderDetailCacheKey(userID, orderID, version)
	var cached models.TicketOrder
	ok, err := redisCacheGetJSON(ctx, s.rdb, cacheKey, &cached)
	if err == errCacheEmpty {
		return nil, ErrTicketOrderNotFound
	}
	if err == nil && ok && !isVolatileOrderStatus(cached.Status) {
		s.attachTicketCredentials(cached.Tickets)
		s.stampOrderExpiry(&cached)
		return &cached, nil
	}

	v, loadErr, _ := s.queryCacheSF.Do(cacheKey, func() (interface{}, error) {
		var again models.TicketOrder
		if hit, e := redisCacheGetJSON(ctx, s.rdb, cacheKey, &again); e == nil && hit &&
			!isVolatileOrderStatus(again.Status) {
			return &again, nil
		} else if e == errCacheEmpty {
			return nil, ErrTicketOrderNotFound
		}
		order, e := s.loadOrderDetail(ctx, userID, orderID)
		if e != nil {
			if errors.Is(e, ErrTicketOrderNotFound) {
				redisCacheSetEmpty(ctx, s.rdb, cacheKey, catalogEmptyTTL)
			}
			return nil, e
		}
		if !isVolatileOrderStatus(order.Status) {
			redisCacheSetJSON(ctx, s.rdb, cacheKey, *order, orderDetailTTL)
		}
		return order, nil
	})
	if loadErr != nil {
		return nil, loadErr
	}
	order := *v.(*models.TicketOrder)
	s.attachTicketCredentials(order.Tickets)
	s.stampOrderExpiry(&order)
	return &order, nil
}

func (s *TicketOrderService) loadOrderDetail(
	ctx context.Context,
	userID, orderID int64,
) (*models.TicketOrder, error) {
	var order models.TicketOrder
	err := s.db.WithContext(ctx).
		Preload("Items").
		Preload("Attendees").
		Where("id = ? AND user_id = ?", orderID, userID).
		First(&order).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTicketOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	var seats []models.SessionSeat
	if err := s.db.WithContext(ctx).Preload("Seat").
		Where("order_id = ?", order.ID).
		Order("id ASC").Find(&seats).Error; err != nil {
		return nil, err
	}
	order.SessionSeats = seats
	// Tickets only exist after payment; skip the join on unpaid detail views.
	if order.Status.HasBeenPaid() {
		var tickets []models.AdmissionTicket
		if err := s.db.WithContext(ctx).
			Preload("OrderItem").
			Where("order_id = ?", order.ID).
			Find(&tickets).Error; err != nil {
			return nil, err
		}
		order.Tickets = tickets
	}
	return &order, nil
}

// ListOrders returns a lightweight page for the order list UI: order summary
// fields plus a single item brief (title/tier/qty/venue). Full Items/Attendees/
// Tickets stay on GetOrder. COUNT is avoided when the page is short; otherwise
// the count is cached briefly with the list payload.
func (s *TicketOrderService) ListOrders(
	ctx context.Context,
	userID int64,
	page, pageSize int,
	filter OrderListFilter,
) ([]models.TicketOrder, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}
	version := s.orderListVersion(ctx, userID)
	cacheKey := orderListCacheKey(userID, version, page, pageSize, filter)
	var cached cachedOrderList
	if ok, err := redisCacheGetJSON(ctx, s.rdb, cacheKey, &cached); err == nil && ok {
		return listJSONToOrders(cached.Orders), cached.Total, nil
	}

	v, loadErr, _ := s.queryCacheSF.Do(cacheKey, func() (interface{}, error) {
		var again cachedOrderList
		if hit, e := redisCacheGetJSON(ctx, s.rdb, cacheKey, &again); e == nil && hit {
			return again, nil
		}
		payload, e := s.loadOrderListPage(ctx, userID, version, page, pageSize, filter)
		if e != nil {
			return nil, e
		}
		redisCacheSetJSON(ctx, s.rdb, cacheKey, payload, orderListTTL)
		return payload, nil
	})
	if loadErr != nil {
		return nil, 0, loadErr
	}
	payload := v.(cachedOrderList)
	return listJSONToOrders(payload.Orders), payload.Total, nil
}

type UserTicketView struct {
	models.AdmissionTicket
	Attendee *models.TicketOrderAttendee `json:"attendee,omitempty"`
}

func (s *TicketOrderService) ListUserTickets(
	ctx context.Context,
	userID int64,
	page, pageSize int,
) ([]UserTicketView, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	query := s.db.WithContext(ctx).Model(&models.AdmissionTicket{}).Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var tickets []models.AdmissionTicket
	err := s.db.WithContext(ctx).
		Preload("OrderItem").
		Where("user_id = ?", userID).
		Order("issued_at DESC, id DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&tickets).Error
	if err != nil {
		return nil, 0, err
	}
	s.attachTicketCredentials(tickets)

	orderIDs := make([]int64, 0, len(tickets))
	seen := make(map[int64]struct{}, len(tickets))
	for _, ticket := range tickets {
		if _, ok := seen[ticket.OrderID]; ok {
			continue
		}
		seen[ticket.OrderID] = struct{}{}
		orderIDs = append(orderIDs, ticket.OrderID)
	}
	attendeesByOrder := map[int64][]models.TicketOrderAttendee{}
	if len(orderIDs) > 0 {
		var attendees []models.TicketOrderAttendee
		if err := s.db.WithContext(ctx).
			Where("order_id IN ?", orderIDs).
			Order("sequence_no ASC").
			Find(&attendees).Error; err != nil {
			return nil, 0, err
		}
		for index := range attendees {
			orderID := attendees[index].OrderID
			attendeesByOrder[orderID] = append(attendeesByOrder[orderID], attendees[index])
		}
	}

	views := make([]UserTicketView, 0, len(tickets))
	for _, ticket := range tickets {
		view := UserTicketView{AdmissionTicket: ticket}
		for index := range attendeesByOrder[ticket.OrderID] {
			attendee := attendeesByOrder[ticket.OrderID][index]
			if attendee.SequenceNo == ticket.SequenceNo {
				copied := attendee
				view.Attendee = &copied
				break
			}
		}
		views = append(views, view)
	}
	return views, total, nil
}

func (s *TicketOrderService) loadOrderListPage(
	ctx context.Context,
	userID int64,
	version string,
	page, pageSize int,
	filter OrderListFilter,
) (cachedOrderList, error) {
	offset := (page - 1) * pageSize
	var orders []models.TicketOrder
	err := s.applyOrderListFilter(s.db.WithContext(ctx), userID, filter).
		Select(
			"ticket_order.id", "ticket_order.order_no", "ticket_order.user_id", "ticket_order.organizer_id",
			"ticket_order.event_id", "ticket_order.session_id",
			"ticket_order.status", "ticket_order.payment_status", "ticket_order.total_amount_cents",
			"ticket_order.order_source", "ticket_order.expires_at", "ticket_order.paid_at",
			"ticket_order.cancelled_at", "ticket_order.create_time", "ticket_order.update_time",
			"ticket_order.delete_time",
		).
		Order("ticket_order.create_time DESC").
		Limit(pageSize + 1).
		Offset(offset).
		Find(&orders).Error
	if err != nil {
		return cachedOrderList{}, err
	}
	hasMore := len(orders) > pageSize
	if hasMore {
		orders = orders[:pageSize]
	}
	if err := s.attachListItemBriefs(ctx, orders); err != nil {
		return cachedOrderList{}, err
	}

	var total int64
	switch {
	case !hasMore:
		total = int64(offset + len(orders))
	default:
		if n, ok := s.cachedOrderCount(ctx, userID, version, filter); ok {
			total = n
		} else {
			if err := s.applyOrderListFilter(s.db.WithContext(ctx), userID, filter).
				Count(&total).Error; err != nil {
				return cachedOrderList{}, err
			}
			s.storeOrderCount(ctx, userID, version, filter, total)
		}
	}
	return cachedOrderList{Orders: ordersToListJSON(orders), Total: total}, nil
}

func (s *TicketOrderService) attachListItemBriefs(ctx context.Context, orders []models.TicketOrder) error {
	if len(orders) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(orders))
	for i := range orders {
		ids = append(ids, orders[i].ID)
	}
	var items []models.TicketOrderItem
	// One row per order: the earliest item id (orders currently have a single line item).
	err := s.db.WithContext(ctx).Raw(`
		SELECT i.id, i.order_id, i.ticket_tier_id, i.quantity,
		       i.event_title_snapshot, i.tier_name_snapshot, i.venue_name_snapshot
		FROM ticket_order_item i
		INNER JOIN (
			SELECT order_id, MIN(id) AS min_id
			FROM ticket_order_item
			WHERE order_id IN ? AND delete_time IS NULL
			GROUP BY order_id
		) first_item ON first_item.min_id = i.id
	`, ids).Scan(&items).Error
	if err != nil {
		return err
	}
	byOrder := make(map[int64]models.TicketOrderItem, len(items))
	for _, item := range items {
		byOrder[item.OrderID] = item
	}
	for i := range orders {
		if item, ok := byOrder[orders[i].ID]; ok {
			orders[i].Items = []models.TicketOrderItem{item}
		}
	}
	return nil
}

func ordersToListJSON(orders []models.TicketOrder) []ticketOrderListJSON {
	out := make([]ticketOrderListJSON, 0, len(orders))
	for _, order := range orders {
		row := ticketOrderListJSON{
			ID:               order.ID,
			OrderNo:          order.OrderNo,
			UserID:           order.UserID,
			OrganizerID:      order.OrganizerID,
			EventID:          order.EventID,
			SessionID:        order.SessionID,
			Status:           string(order.Status),
			PaymentStatus:    string(order.PaymentStatus),
			TotalAmountCents: order.TotalAmountCents,
			OrderSource:      string(order.OrderSource),
			ExpiresAt:        order.ExpiresAt,
			PaidAt:           order.PaidAt,
			CancelledAt:      order.CancelledAt,
			CreateTime:       order.CreateTime,
			UpdateTime:       order.UpdateTime,
		}
		if len(order.Items) > 0 {
			item := order.Items[0]
			row.Items = []ticketOrderItemBrief{{
				TicketTierID:       item.TicketTierID,
				Quantity:           item.Quantity,
				EventTitleSnapshot: item.EventTitleSnapshot,
				TierNameSnapshot:   item.TierNameSnapshot,
				VenueNameSnapshot:  item.VenueNameSnapshot,
			}}
		}
		out = append(out, row)
	}
	return out
}

func listJSONToOrders(rows []ticketOrderListJSON) []models.TicketOrder {
	out := make([]models.TicketOrder, 0, len(rows))
	for _, row := range rows {
		order := models.TicketOrder{
			Base: models.Base{
				ID:         row.ID,
				CreateTime: row.CreateTime,
				UpdateTime: row.UpdateTime,
			},
			OrderNo:          row.OrderNo,
			UserID:           row.UserID,
			OrganizerID:      row.OrganizerID,
			EventID:          row.EventID,
			SessionID:        row.SessionID,
			Status:           models.TicketOrderStatus(row.Status),
			PaymentStatus:    models.PaymentStatus(row.PaymentStatus),
			TotalAmountCents: row.TotalAmountCents,
			OrderSource:      models.TicketOrderSource(row.OrderSource),
			ExpiresAt:        row.ExpiresAt,
			PaidAt:           row.PaidAt,
			CancelledAt:      row.CancelledAt,
		}
		if len(row.Items) > 0 {
			item := row.Items[0]
			order.Items = []models.TicketOrderItem{{
				TicketTierID:       item.TicketTierID,
				Quantity:           item.Quantity,
				EventTitleSnapshot: item.EventTitleSnapshot,
				TierNameSnapshot:   item.TierNameSnapshot,
				VenueNameSnapshot:  item.VenueNameSnapshot,
			}}
		}
		out = append(out, order)
	}
	return out
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
	if order.Status != models.TicketOrderStatusPendingPayment || !s.paymentWindowOpen(&order) {
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

	deadline := s.paymentDeadline(&order)
	intent, err := s.payment.CreatePayment(ctx, PaymentCreateRequest{
		OrderID: order.ID, UserID: userID, AmountCents: order.TotalAmountCents,
		ExpiresAt: deadline, Scenario: scenario,
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
		if locked.Status != models.TicketOrderStatusPendingPayment || !s.paymentWindowOpen(&locked) {
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
	var eventUserID, eventOrderID, funnelOrganizerID int64
	var eventName, eventMessage string
	var waitlistAllocateTierID int64
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
		if payment.WaitlistID > 0 {
			queued, userID, waitlistID, name, message, tierID, organizerID, waitlistErr := s.applyWaitlistPaymentInTx(
				tx, payment, notification, callback.ID,
			)
			if waitlistErr != nil {
				return waitlistErr
			}
			if queued {
				paid = true
				waitlistAllocateTierID = tierID
				funnelOrganizerID = organizerID
			}
			eventUserID, eventOrderID = userID, waitlistID
			eventName, eventMessage = name, message
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
				reason = "支付未完成"
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
			eventName, eventMessage = "payment_failed", "支付未完成，请重新支付"
			now := time.Now()
			return tx.Model(&models.PaymentCallback{}).Where("id = ?", callback.ID).
				Update("processed_at", &now).Error
		}

		if !s.paymentWindowOpen(&order) {
			if order.Status == models.TicketOrderStatusPendingPayment {
				log.Printf(
					"payment callback skipped expired window order=%d expires_at=%s deadline=%s",
					order.ID, order.ExpiresAt.Format(time.RFC3339), s.paymentDeadline(&order).Format(time.RFC3339),
				)
			}
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
		if order.OrderSource != models.TicketOrderSourceWaitlist {
			if err := bumpFunnelOrderDaily(
				tx, order.EventID, order.OrganizerID, string(order.OrderSource),
				0, 1, 0, now,
			); err != nil {
				return err
			}
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
		funnelOrganizerID = order.OrganizerID
		eventName, eventMessage = "paid", "支付成功，电子票已生成"
		return nil
	})
	if err == nil && paid {
		bumpFunnelCacheVersion(ctx, s.rdb, funnelOrganizerID)
	}
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
	if err == nil && waitlistAllocateTierID > 0 {
		_, _ = s.AllocateWaitlist(ctx, waitlistAllocateTierID)
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
	var skipRedis bool
	var divertedTierID int64
	var organizerID int64
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
		if len(order.Items) == 0 {
			return fmt.Errorf("订单明细异常")
		}
		if len(order.Items) == 1 {
			tierID, quantity = order.Items[0].TicketTierID, order.Items[0].Quantity
		}
		rushCampaignID = order.RushSaleCampaignID
		organizerID = order.OrganizerID
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
		var restoreErr error
		stockBucketNo, rushBucketNo, skipRedis, restoreErr = s.restoreOrderInventory(tx, &order)
		if restoreErr != nil {
			return restoreErr
		}
		var divertErr error
		skipRedis, divertedTierID, divertErr = s.maybeDivertRestoredQuota(tx, &order, skipRedis)
		if divertErr != nil {
			return divertErr
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
			var restoreErr error
			stockBucketNo, rushBucketNo, skipRedis, restoreErr = s.restoreOrderInventory(tx, &order)
			if restoreErr != nil {
				return restoreErr
			}
			var divertErr error
			skipRedis, divertedTierID, divertErr = s.maybeDivertRestoredQuota(tx, &order, skipRedis)
			if divertErr != nil {
				return divertErr
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
			if err := tx.Model(&models.TicketOrder{}).
				Where("id = ? AND status = ?", order.ID, models.TicketOrderStatusPaid).
				Updates(map[string]interface{}{
					"status":         models.TicketOrderStatusCancelled,
					"payment_status": models.PaymentStatusRefunded,
					"cancelled_at":   &now,
					"cancel_reason":  strings.TrimSpace(reason),
				}).Error; err != nil {
				return err
			}
			if order.OrderSource != models.TicketOrderSourceWaitlist {
				if err := bumpFunnelOrderDaily(
					tx, order.EventID, order.OrganizerID, string(order.OrderSource),
					0, 0, 1, now,
				); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
		bumpFunnelCacheVersion(ctx, s.rdb, organizerID)
	}

	if err == nil && !skipRedis {
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
		message := "订单已取消"
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
	if err == nil && divertedTierID > 0 {
		_, _ = s.AllocateWaitlist(ctx, divertedTierID)
	}
	return err
}

func (s *TicketOrderService) issueAdmissionTickets(
	tx *gorm.DB,
	order *models.TicketOrder,
	issuedAt time.Time,
) error {
	var event models.Event
	if err := tx.Select("id", "sale_mode").First(&event, order.EventID).Error; err != nil {
		return err
	}
	var labels map[string]string
	var err error
	if event.SaleMode.IsSeated() {
		labels, err = seatedPlaceLabels(tx, order)
		if err != nil {
			return err
		}
	} else {
		labels, err = s.nextPlaceLabels(tx, order)
		if err != nil {
			return err
		}
	}
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
				PlaceLabel:   labels[placeLabelKey(item.ID, sequence)],
				Status:       models.AdmissionTicketStatusValid,
				IssuedAt:     issuedAt,
			}
			if err := tx.Create(&ticket).Error; err != nil {
				return err
			}
		}
	}
	if event.SaleMode.IsSeated() {
		return markSessionSeatsSold(tx, order.ID)
	}
	return nil
}

func placeLabelKey(orderItemID int64, sequence int) string {
	return strconv.FormatInt(orderItemID, 10) + ":" + strconv.Itoa(sequence)
}

func (s *TicketOrderService) nextPlaceLabels(tx *gorm.DB, order *models.TicketOrder) (map[string]string, error) {
	labels := map[string]string{}
	var event models.Event
	if err := tx.Select("id", "sale_mode").First(&event, order.EventID).Error; err != nil {
		return nil, err
	}
	if event.SaleMode.IsSeated() {
		return labels, nil
	}
	tierIDs := make([]int64, 0, len(order.Items))
	seen := map[int64]struct{}{}
	for _, item := range order.Items {
		if _, ok := seen[item.TicketTierID]; ok {
			continue
		}
		seen[item.TicketTierID] = struct{}{}
		tierIDs = append(tierIDs, item.TicketTierID)
	}
	sort.Slice(tierIDs, func(i, j int) bool { return tierIDs[i] < tierIDs[j] })
	if len(tierIDs) == 0 {
		return labels, nil
	}
	var tiers []models.TicketTier
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id IN ?", tierIDs).Find(&tiers).Error; err != nil {
		return nil, err
	}
	byID := make(map[int64]models.TicketTier, len(tiers))
	for _, tier := range tiers {
		byID[tier.ID] = tier
	}
	nextSeq := map[int64]int{}
	for _, item := range order.Items {
		tier, ok := byID[item.TicketTierID]
		if !ok || !tier.AssignPlaceNo {
			continue
		}
		seq := nextSeq[tier.ID]
		if seq == 0 {
			seq = tier.PlaceSeq
		}
		prefix := placePrefix(tier.Name)
		for sequence := 1; sequence <= item.Quantity; sequence++ {
			seq++
			labels[placeLabelKey(item.ID, sequence)] = formatPlaceLabel(prefix, seq)
		}
		nextSeq[tier.ID] = seq
	}
	for tierID, seq := range nextSeq {
		if err := tx.Model(&models.TicketTier{}).Where("id = ?", tierID).
			Update("place_seq", seq).Error; err != nil {
			return nil, err
		}
	}
	return labels, nil
}

func (s *TicketOrderService) takePlaceLabel(tx *gorm.DB, eventID, tierID int64) (string, error) {
	var event models.Event
	if err := tx.Select("id", "sale_mode").First(&event, eventID).Error; err != nil {
		return "", err
	}
	if event.SaleMode.IsSeated() {
		return "", nil
	}
	var tier models.TicketTier
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&tier, tierID).Error; err != nil {
		return "", err
	}
	if !tier.AssignPlaceNo {
		return "", nil
	}
	seq := tier.PlaceSeq + 1
	if err := tx.Model(&models.TicketTier{}).Where("id = ?", tierID).
		Update("place_seq", seq).Error; err != nil {
		return "", err
	}
	return formatPlaceLabel(placePrefix(tier.Name), seq), nil
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
	db := s.asyncDB()
	var orderIDs []int64
	if err := db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("status = ?", models.TicketOrderStatusPaid).
		Pluck("id", &orderIDs).Error; err != nil {
		return err
	}
	for _, orderID := range orderIDs {
		err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
					label, err := s.takePlaceLabel(tx, order.EventID, item.TicketTierID)
					if err != nil {
						return err
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
						PlaceLabel:   label,
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
	db := s.asyncDB()
	var orders []models.TicketOrder
	if err := db.WithContext(ctx).Preload("Items").
		Where("status = ? AND expires_at <= ?",
			models.TicketOrderStatusPendingPayment, time.Now()).
		Limit(100).Find(&orders).Error; err != nil {
		return err
	}
	for _, order := range orders {
		if s.paymentWindowOpen(&order) {
			continue
		}
		err := s.cancelPendingPaymentOnlyDB(ctx, db, order.UserID, order.ID, "支付超时自动取消")
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
	return s.cancelPendingPaymentOnlyDB(ctx, s.db, userID, orderID, reason)
}

func (s *TicketOrderService) cancelPendingPaymentOnlyDB(
	ctx context.Context,
	db *gorm.DB,
	userID, orderID int64,
	reason string,
) error {
	var tierID int64
	var quantity int
	var rushCampaignID *int64
	var stockBucketNo *int
	var rushBucketNo *int
	var skipRedis bool
	var divertedTierID int64
	txStarted := time.Now()
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
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
		if len(order.Items) == 0 {
			return fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
		}
		if len(order.Items) == 1 {
			tierID, quantity = order.Items[0].TicketTierID, order.Items[0].Quantity
		}
		rushCampaignID = order.RushSaleCampaignID
		var restoreErr error
		stockBucketNo, rushBucketNo, skipRedis, restoreErr = s.restoreOrderInventory(tx, &order)
		if restoreErr != nil {
			return restoreErr
		}
		var divertErr error
		skipRedis, divertedTierID, divertErr = s.maybeDivertRestoredQuota(tx, &order, skipRedis)
		if divertErr != nil {
			return divertErr
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
	if err == nil && !skipRedis {
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
			"订单支付超时，已自动取消",
		)
	}
	if err == nil && divertedTierID > 0 {
		_, _ = s.AllocateWaitlist(ctx, divertedTierID)
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
	idempotencyKey string,
	proposedOrderID int64,
) (stockReservationResult, int64, error) {
	reservationKey := stockReservationKey(userID, idempotencyKey)
	nowMS := time.Now().UnixMilli()
	run := func(stockKey string, bucketNo int) (stockReservationResult, int64, error) {
		raw, err := reserveNormalStockScript.Run(
			ctx,
			s.rdb,
			[]string{stockKey, reservationKey, stockReservationPendingKey},
			quantity,
			strconv.FormatInt(proposedOrderID, 10),
			strconv.FormatInt(userID, 10),
			strconv.FormatInt(tier.ID, 10),
			stockReservationKindNormal,
			bucketNo,
			idempotencyKey,
			nowMS,
			stockReservationTTLMillis(),
		).Result()
		if err != nil {
			return stockReservationResult{}, 0, err
		}
		return decodeStockReservationResult(raw, reservationKey)
	}
	if !s.inventory.Enabled {
		return run(ticketStockKey(tier.ID), -1)
	}
	n := s.inventory.EffectiveBucketCount(tier.TotalQuota)
	maxAttempts := 1 + s.inventory.BucketRetry
	if maxAttempts > n {
		maxAttempts = n
	}
	var last int64 = -1
	for attempt := 0; attempt < maxAttempts; attempt++ {
		b := SelectBucketNo(userID, n, attempt)
		reservation, code, runErr := run(TicketStockBucketKey(tier.ID, b), b)
		if runErr != nil {
			return stockReservationResult{}, 0, runErr
		}
		if code > 0 || code == stockReservationCodeConflict || code == -2 {
			return reservation, code, nil
		}
		last = code
	}
	return stockReservationResult{}, last, nil
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

func (s *TicketOrderService) buildSeatedItems(
	ctx context.Context,
	session *models.EventSession,
	event *models.Event,
	venue *models.Venue,
	seatIDs []int64,
) ([]models.TicketOrderItem, error) {
	var seats []models.SessionSeat
	if err := s.db.WithContext(ctx).Where("id IN ?", seatIDs).Find(&seats).Error; err != nil {
		return nil, err
	}
	if len(seats) != len(seatIDs) {
		return nil, fmt.Errorf("%w: 座位不存在", ErrInvalidTicketCatalog)
	}
	byID := map[int64]models.SessionSeat{}
	for _, seat := range seats {
		if seat.SessionID != session.ID {
			return nil, fmt.Errorf("%w: 座位必须属于当前场次", ErrInvalidTicketCatalog)
		}
		byID[seat.ID] = seat
	}
	tierOrder := make([]int64, 0)
	qty := map[int64]int{}
	for _, id := range seatIDs {
		seat := byID[id]
		if qty[seat.TicketTierID] == 0 {
			tierOrder = append(tierOrder, seat.TicketTierID)
		}
		qty[seat.TicketTierID]++
	}
	var tiers []models.TicketTier
	if err := s.db.WithContext(ctx).Where("id IN ?", tierOrder).Find(&tiers).Error; err != nil {
		return nil, err
	}
	tierByID := map[int64]models.TicketTier{}
	for _, row := range tiers {
		tierByID[row.ID] = row
	}
	items := make([]models.TicketOrderItem, 0, len(tierOrder))
	for _, tierID := range tierOrder {
		row, ok := tierByID[tierID]
		if !ok {
			return nil, fmt.Errorf("%w: 票档不存在", ErrInvalidTicketCatalog)
		}
		items = append(items, models.TicketOrderItem{
			TicketTierID:            row.ID,
			Quantity:                qty[tierID],
			UnitPriceCents:          row.PriceCents,
			EventTitleSnapshot:      event.Title,
			SessionStartsAtSnapshot: session.StartsAt,
			VenueNameSnapshot:       venue.Name,
			VenueAddressSnapshot:    venue.Address,
			TierNameSnapshot:        row.Name,
		})
	}
	return items, nil
}

func (s *TicketOrderService) countUserEventTickets(ctx context.Context, userID, eventID int64) (int, error) {
	var total int
	err := s.db.WithContext(ctx).Model(&models.TicketOrderItem{}).
		Joins("JOIN ticket_order ON ticket_order.id = ticket_order_item.order_id AND ticket_order.delete_time IS NULL").
		Where("ticket_order.user_id = ? AND ticket_order.event_id = ?", userID, eventID).
		Where("ticket_order.status IN ?", []models.TicketOrderStatus{
			models.TicketOrderStatusQueued,
			models.TicketOrderStatusPendingPayment,
			models.TicketOrderStatusPaid,
		}).
		Select("COALESCE(SUM(ticket_order_item.quantity), 0)").
		Scan(&total).Error
	return total, err
}

func (s *TicketOrderService) expandAttendeeProfiles(
	ctx context.Context,
	userID int64,
	input *CreateTicketOrderInput,
) error {
	ids := make([]int64, 0, len(input.AttendeeProfileIDs))
	for _, raw := range input.AttendeeProfileIDs {
		if int64(raw) > 0 {
			ids = append(ids, int64(raw))
		}
	}
	for _, attendee := range input.Attendees {
		if int64(attendee.ProfileID) > 0 {
			ids = append(ids, int64(attendee.ProfileID))
		}
	}
	if len(ids) == 0 {
		return nil
	}
	var rows []models.UserAttendee
	if err := s.db.WithContext(ctx).Where("user_id = ? AND id IN ?", userID, ids).Find(&rows).Error; err != nil {
		return err
	}
	byID := map[int64]models.UserAttendee{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	expanded := make([]TicketAttendeeInput, 0, len(ids))
	seen := map[int64]struct{}{}
	for _, id := range ids {
		if _, dup := seen[id]; dup {
			return fmt.Errorf("%w: 不能重复选择同一位观演人", ErrInvalidTicketCatalog)
		}
		seen[id] = struct{}{}
		row, ok := byID[id]
		if !ok {
			return fmt.Errorf("%w: 观演人档案不存在", ErrInvalidTicketCatalog)
		}
		plain, err := decryptIdentity(s.identityHashKey, row.IDNumberCipher)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrInvalidTicketCatalog, err.Error())
		}
		expanded = append(expanded, TicketAttendeeInput{
			Name:     row.Name,
			IDType:   row.IDType,
			IDNumber: plain,
		})
	}
	input.Attendees = expanded
	return nil
}

func (s *TicketOrderService) assertEventIdentitiesFree(
	ctx context.Context,
	eventID int64,
	attendees []TicketAttendeeInput,
) error {
	if len(attendees) == 0 {
		return nil
	}
	keys := make([]string, 0, len(attendees))
	for _, attendee := range attendees {
		keys = append(keys, stableIdentityKey(s.identityHashKey, attendee.IDNumber))
	}
	var count int64
	err := s.db.WithContext(ctx).Model(&models.TicketOrderAttendee{}).
		Joins("JOIN ticket_order ON ticket_order.id = ticket_order_attendee.order_id AND ticket_order.delete_time IS NULL").
		Where("ticket_order.event_id = ?", eventID).
		Where("ticket_order.status IN ?", []models.TicketOrderStatus{
			models.TicketOrderStatusQueued,
			models.TicketOrderStatusPendingPayment,
			models.TicketOrderStatusPaid,
		}).
		Where("ticket_order_attendee.identity_key IN ?", keys).
		Count(&count).Error
	if err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("%w: 所选证件已购买本场门票", ErrTicketOrderUnavailable)
	}
	var waitlistCount int64
	err = s.db.WithContext(ctx).Model(&models.WaitlistAttendee{}).
		Joins("JOIN waitlist_entry ON waitlist_entry.id = waitlist_attendee.waitlist_id AND waitlist_entry.delete_time IS NULL").
		Where("waitlist_entry.event_id = ?", eventID).
		Where("waitlist_entry.status IN ?", []models.WaitlistStatus{
			models.WaitlistStatusPendingPayment,
			models.WaitlistStatusQueued,
			models.WaitlistStatusFulfilled,
		}).
		Where("waitlist_attendee.identity_key IN ?", keys).
		Count(&waitlistCount).Error
	if err != nil {
		return err
	}
	if waitlistCount > 0 {
		return fmt.Errorf("%w: 所选证件已购买本场门票", ErrTicketOrderUnavailable)
	}
	return nil
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
		attendees = append(attendees, models.TicketOrderAttendee{
			Base:           models.Base{ID: s.node.Generate().Int64()},
			OrderID:        orderID,
			SequenceNo:     index + 1,
			Name:           strings.TrimSpace(input.Name),
			IDType:         "id_card",
			IDNumberMasked: maskIDNumber(idNumber),
			IDNumberHash:   orderAttendeeHash(s.identityHashKey, orderID, idNumber),
			IdentityKey:    stableIdentityKey(s.identityHashKey, idNumber),
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

func isVolatileOrderStatus(status models.TicketOrderStatus) bool {
	return status == models.TicketOrderStatusQueued || status == models.TicketOrderStatusPendingPayment
}

func snowflakeCreatedAt(id int64) time.Time {
	if id <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(snowflake.ID(id).Time())
}

func (s *TicketOrderService) paymentDeadline(order *models.TicketOrder) time.Time {
	if order == nil {
		return time.Time{}
	}
	deadline := order.ExpiresAt
	if created := snowflakeCreatedAt(order.ID); !created.IsZero() {
		if byID := created.Add(s.paymentTimeout); byID.After(deadline) {
			deadline = byID
		}
	}
	if !order.CreateTime.IsZero() {
		if byCreate := order.CreateTime.Add(s.paymentTimeout); byCreate.After(deadline) {
			deadline = byCreate
		}
	}
	return deadline
}

func (s *TicketOrderService) paymentWindowOpen(order *models.TicketOrder) bool {
	if order == nil || order.Status != models.TicketOrderStatusPendingPayment {
		return false
	}
	deadline := s.paymentDeadline(order)
	return !deadline.IsZero() && time.Now().Before(deadline)
}

func (s *TicketOrderService) stampOrderExpiry(order *models.TicketOrder) {
	if order == nil {
		return
	}
	deadline := s.paymentDeadline(order)
	if deadline.IsZero() {
		return
	}
	order.ExpiresAtUnix = deadline.Unix()
}
