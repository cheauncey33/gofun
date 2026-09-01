package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"gofun/models"
	apptelemetry "gofun/pkg/telemetry"
	"log"
	"strconv"
	"strings"
	"time"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"
)

const (
	ticketIdempotencyTTL       = 10 * time.Minute
	purchasableContextLocalTTL = 3 * time.Second
	ticketOrderTxMaxAttempts   = 3
)

func ticketIdempotencyRedisKey(userID int64, idempotencyKey string) string {
	return "fuchang:ticket:idem-result:" + strconv.FormatInt(userID, 10) + ":" + idempotencyKey
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
	idempotencyKey, requestHash string,
) (*TicketOrderReceipt, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if userID <= 0 || len(idempotencyKey) < 8 {
		return nil, nil
	}
	redisKey := ticketIdempotencyRedisKey(userID, idempotencyKey)
	if s.rdb != nil {
		if raw, err := s.rdb.Get(ctx, redisKey).Result(); err == nil && raw != "" {
			orderID, cachedStatus, ok := parseIdempotencyCache(raw)
			if ok {
				// 缓存只存下单瞬间的快照状态，可能滞后于真实状态（如订单已进入待支付/已支付），
				// 回源 MySQL 刷新后再返回，避免客户端重试拿到过期的 queued。
				var existing models.TicketOrder
				queryErr := s.db.WithContext(ctx).
					Select("id", "order_no", "status", "request_hash").
					Where("id = ? AND user_id = ?", orderID, userID).
					First(&existing).Error
				if queryErr == nil {
					if requestHash != "" && existing.RequestHash != "" && existing.RequestHash != requestHash {
						return nil, ErrInvalidTicketCatalog
					}
					receipt := ticketOrderReceipt(&existing)
					s.rememberIdempotentOrder(ctx, userID, idempotencyKey, receipt)
					return receipt, nil
				}
				if errors.Is(queryErr, gorm.ErrRecordNotFound) {
					// 缓存与库不一致（订单行不存在）：清除缓存，走正常下单流程。
					_ = s.rdb.Del(ctx, redisKey).Err()
				} else {
					// MySQL 故障时降级缓存快照（状态可能滞后），不阻断幂等识别。
					return &TicketOrderReceipt{
						OrderID: orderID,
						OrderNo: "FC" + strconv.FormatInt(orderID, 10),
						Status:  cachedStatus,
					}, nil
				}
			}
		} else if err != nil && !errors.Is(err, redis.Nil) {
			// Redis 故障时降级 MySQL，不阻断下单。
		}
	}

	var existing models.TicketOrder
	err := s.db.WithContext(ctx).
		Select("id", "order_no", "status", "request_hash").
		Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
		First(&existing).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if requestHash != "" && existing.RequestHash != "" && existing.RequestHash != requestHash {
		return nil, ErrInvalidTicketCatalog
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
	ctx, span := otel.Tracer("gofun-ticketing/order").Start(
		ctx,
		"ticket.outbox.enqueue",
		trace.WithAttributes(attribute.Int64("ticket.order.id", orderID)),
	)
	defer span.End()
	message.EventID = s.node.Generate().Int64()
	message.EventType = ticketOrderFinalizeEventType
	message.TraceContext = apptelemetry.InjectMap(ctx)
	payload, err := json.Marshal(message)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return err
	}
	row := models.TicketOrderOutbox{
		ID:        message.EventID,
		OrderID:   orderID,
		EventType: message.EventType,
		Payload:   string(payload),
		Status:    models.TicketOrderOutboxPending,
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
	seated := len(message.SeatIDs) > 0
	for attempt := 1; attempt <= ticketOrderTxMaxAttempts; attempt++ {
		err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if !seated {
				// 恢复任务也会尝试插入同一个 order_id。唯一键冲突会等待对方事务结束，
				// 从而保证“订单提交”和“Redis 回滚”只能有一方取得执行权。
				if err := tx.Create(&models.TicketStockRecoveryFence{
					OrderID: order.ID,
					Owner:   models.TicketStockRecoveryFenceOrder,
				}).Error; err != nil {
					return err
				}
			}
			if err := tx.Create(order).Error; err != nil {
				return err
			}
			if order.OrderSource != models.TicketOrderSourceWaitlist {
				if err := upsertFunnelVisitorStage(
					tx, order.EventID, order.OrganizerID, order.FunnelVisitorKey,
					models.FunnelStageSubmitted, time.Now(),
				); err != nil {
					return err
				}
				if err := bumpFunnelOrderDaily(
					tx, order.EventID, order.OrganizerID, string(order.OrderSource),
					1, 0, 0, time.Now(),
				); err != nil {
					return err
				}
			}
			if seated {
				if err := holdSessionSeats(tx, order, message.SeatIDs); err != nil {
					return err
				}
			}
			return s.enqueueOutboxInTx(ctx, tx, order.ID, message)
		})
		if err == nil || !isRetryableMySQLTransactionError(err) ||
			attempt == ticketOrderTxMaxAttempts {
			if err == nil && order.OrderSource != models.TicketOrderSourceWaitlist {
				bumpFunnelCacheVersion(ctx, s.rdb, order.OrganizerID)
			}
			return err
		}
		delay := time.Duration(attempt*10+int(order.ID%7)) * time.Millisecond
		log.Printf(
			"ticket order transaction retry: order=%d attempt=%d delay=%s err=%v",
			order.ID,
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
	return nil
}

func isRetryableMySQLTransactionError(err error) bool {
	var mysqlErr *mysqlDriver.MySQLError
	if !errors.As(err, &mysqlErr) {
		return false
	}
	return mysqlErr.Number == 1213 || mysqlErr.Number == 1205
}

func isDuplicateStorageKeyError(err error) bool {
	if errors.Is(err, gorm.ErrDuplicatedKey) {
		return true
	}
	var mysqlErr *mysqlDriver.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}
