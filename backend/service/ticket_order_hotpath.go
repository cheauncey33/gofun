package service

import (
	"WHU_Snack_GO/models"
	apptelemetry "WHU_Snack_GO/pkg/telemetry"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const (
	ticketIdempotencyTTL      = 10 * time.Minute
	purchasableContextLocalTTL = 3 * time.Second
)

func ticketIdempotencyRedisKey(userID int64, idempotencyKey string) string {
	return "fuchang:ticket:idem:" + strconv.FormatInt(userID, 10) + ":" + idempotencyKey
}

func purchasableContextLocalKey(tierID int64) string {
	return "local:ticket:purchasable:" + strconv.FormatInt(tierID, 10)
}

type purchasableContext struct {
	Tier    models.TicketTier
	Session models.EventSession
	Event   models.Event
	Venue   models.Venue
}

// lookupIdempotentOrder 先查 Redis，再回源 MySQL；命中后回填 Redis。
func (s *TicketOrderService) lookupIdempotentOrder(
	ctx context.Context,
	userID int64,
	idempotencyKey string,
) (*TicketOrderReceipt, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if userID <= 0 || len(idempotencyKey) < 8 {
		return nil, nil
	}
	redisKey := ticketIdempotencyRedisKey(userID, idempotencyKey)
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, redisKey).Result(); err == nil && raw != "" {
			orderID, status, ok := parseIdempotencyCache(raw)
			if ok {
				return &TicketOrderReceipt{
					OrderID: orderID,
					OrderNo: "FC" + strconv.FormatInt(orderID, 10),
					Status:  status,
				}, nil
			}
		} else if err != nil && !errors.Is(err, redis.Nil) {
			// Redis 故障时降级 MySQL，不阻断下单。
		}
	}

	var existing models.TicketOrder
	err := s.db.WithContext(ctx).
		Select("id", "order_no", "status").
		Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	receipt := ticketOrderReceipt(&existing)
	s.rememberIdempotentOrder(ctx, userID, idempotencyKey, receipt)
	return receipt, nil
}

func parseIdempotencyCache(raw string) (int64, models.TicketOrderStatus, bool) {
	parts := strings.SplitN(raw, "|", 2)
	if len(parts) != 2 {
		return 0, "", false
	}
	orderID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || orderID <= 0 {
		return 0, "", false
	}
	status := models.TicketOrderStatus(parts[1])
	if status == "" {
		status = models.TicketOrderStatusQueued
	}
	return orderID, status, true
}

func (s *TicketOrderService) rememberIdempotentOrder(
	ctx context.Context,
	userID int64,
	idempotencyKey string,
	receipt *TicketOrderReceipt,
) {
	if s.rdb == nil || receipt == nil || receipt.OrderID <= 0 {
		return
	}
	_ = s.rdb.Set(
		ctx,
		ticketIdempotencyRedisKey(userID, strings.TrimSpace(idempotencyKey)),
		fmt.Sprintf("%d|%s", receipt.OrderID, receipt.Status),
		ticketIdempotencyTTL,
	).Err()
}

func (s *TicketOrderService) enqueueOutboxInTx(
	ctx context.Context,
	tx *gorm.DB,
	orderID int64,
	message TicketOrderMessage,
) error {
	ctx, span := otel.Tracer("fuchang-ticketing/order").Start(
		ctx,
		"ticket.outbox.enqueue",
		trace.WithAttributes(attribute.Int64("ticket.order.id", orderID)),
	)
	defer span.End()
	message.TraceContext = apptelemetry.InjectMap(ctx)
	payload, err := json.Marshal(message)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	row := models.TicketOrderOutbox{
		ID:      s.node.Generate().Int64(),
		OrderID: orderID,
		Payload: string(payload),
		Status:  models.TicketOrderOutboxPending,
	}
	if err := tx.WithContext(ctx).Create(&row).Error; err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	return nil
}

func (s *TicketOrderService) createOrderAndOutbox(
	ctx context.Context,
	order *models.TicketOrder,
	message TicketOrderMessage,
) error {
	if s.outboxWriteMode == "batch" {
		if err := s.db.WithContext(ctx).Create(order).Error; err != nil {
			return err
		}
		// 订单已落库：outbox 失败交由 recover 扫描补写，避免误回滚 Redis 预扣。
		if err := s.enqueueOutboxDraft(ctx, order.ID, message); err != nil {
			log.Printf("outbox batch enqueue after order %d: %v (recover will backfill)", order.ID, err)
		}
		return nil
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		return s.enqueueOutboxInTx(ctx, tx, order.ID, message)
	})
}
