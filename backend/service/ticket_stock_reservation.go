package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"gofun/metrics"
	"gofun/models"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	stockReservationPrefix           = "fuchang:ticket:reservation:"
	stockReservationPendingKey       = stockReservationPrefix + "pending"
	stockReservationStatePending     = "pending"
	stockReservationStateCommitted   = "committed"
	stockReservationKindNormal       = "normal"
	stockReservationKindRush         = "rush"
	stockReservationTTL              = 24 * time.Hour
	stockReservationRecoveryInterval = 30 * time.Second
	stockReservationRecoveryGrace    = 2 * time.Minute
	stockReservationRecoveryBatch    = 100
)

// Redis Lua 返回码。库存不足/未预热沿用原下单协议；1 表示本次新建预扣，2 表示幂等命中。
const (
	stockReservationCodeConflict = int64(-5)
	stockReservationCodeCreated  = int64(1)
	stockReservationCodeExisting = int64(2)
)

// reserveNormalStockScript 原子完成普通购票库存预扣与预扣凭证写入。
// order_id 作为字符串保存和返回，避免 Redis Lua number 对 Snowflake int64 丢精度。
var reserveNormalStockScript = redis.NewScript(`
local existingOrderID = redis.call('HGET', KEYS[2], 'order_id')
if existingOrderID then
  if redis.call('HGET', KEYS[2], 'kind') ~= ARGV[5]
    or redis.call('HGET', KEYS[2], 'tier_id') ~= ARGV[4]
    or redis.call('HGET', KEYS[2], 'quantity') ~= ARGV[1] then
    return {-5, '', 0, -1}
  end
  return {2, existingOrderID, redis.call('HGET', KEYS[2], 'remaining') or '0', redis.call('HGET', KEYS[2], 'stock_bucket_no') or '-1'}
end

local stock = tonumber(redis.call('GET', KEYS[1]))
if stock == nil then
  return {-2, '', 0, -1}
end
local quantity = tonumber(ARGV[1])
if stock < quantity then
  return {-1, '', 0, -1}
end
local remaining = stock - quantity
redis.call('DECRBY', KEYS[1], quantity)
redis.call('HSET', KEYS[2],
  'order_id', ARGV[2],
  'user_id', ARGV[3],
  'tier_id', ARGV[4],
  'kind', ARGV[5],
  'campaign_id', '0',
  'quantity', ARGV[1],
  'stock_bucket_no', ARGV[6],
  'rush_bucket_no', '-1',
  'idempotency_key', ARGV[7],
  'state', 'pending',
  'remaining', tostring(remaining),
  'created_at_ms', ARGV[8])
redis.call('PEXPIRE', KEYS[2], ARGV[9])
redis.call('ZADD', KEYS[3], ARGV[8], KEYS[2])
return {1, ARGV[2], remaining, ARGV[6]}
`)

// reserveRushStockScript 将秒杀活动库存、票档库存、用户限购计数与预扣凭证放在同一 Lua 中。
var reserveRushStockScript = redis.NewScript(`
local existingOrderID = redis.call('HGET', KEYS[4], 'order_id')
if existingOrderID then
  if redis.call('HGET', KEYS[4], 'kind') ~= ARGV[6]
    or redis.call('HGET', KEYS[4], 'tier_id') ~= ARGV[5]
    or redis.call('HGET', KEYS[4], 'campaign_id') ~= ARGV[4]
    or redis.call('HGET', KEYS[4], 'quantity') ~= ARGV[1] then
    return {-5, '', 0, -1}
  end
  return {2, existingOrderID, redis.call('HGET', KEYS[4], 'remaining') or '0', redis.call('HGET', KEYS[4], 'stock_bucket_no') or '-1'}
end

local rushStock = tonumber(redis.call('GET', KEYS[1]))
local ticketStock = tonumber(redis.call('GET', KEYS[2]))
if rushStock == nil or ticketStock == nil then
  return {-2, '', 0, -1}
end
local quantity = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
local bought = tonumber(redis.call('GET', KEYS[3]) or '0')
if bought + quantity > limit then
  return {-4, '', 0, -1}
end
if rushStock < quantity or ticketStock < quantity then
  return {-1, '', 0, -1}
end
local remaining = rushStock - quantity
redis.call('DECRBY', KEYS[1], quantity)
redis.call('DECRBY', KEYS[2], quantity)
redis.call('INCRBY', KEYS[3], quantity)
redis.call('EXPIRE', KEYS[3], ARGV[3])
redis.call('HSET', KEYS[4],
  'order_id', ARGV[7],
  'user_id', ARGV[8],
  'tier_id', ARGV[5],
  'kind', ARGV[6],
  'campaign_id', ARGV[4],
  'quantity', ARGV[1],
  'stock_bucket_no', ARGV[9],
  'rush_bucket_no', ARGV[9],
  'idempotency_key', ARGV[10],
  'state', 'pending',
  'remaining', tostring(remaining),
  'created_at_ms', ARGV[11])
redis.call('PEXPIRE', KEYS[4], ARGV[12])
redis.call('ZADD', KEYS[5], ARGV[11], KEYS[4])
return {1, ARGV[7], remaining, ARGV[9]}
`)

var confirmStockReservationScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[1]) == 0 then
  redis.call('ZREM', KEYS[2], KEYS[1])
  return 0
end
if redis.call('HGET', KEYS[1], 'order_id') ~= ARGV[1] then
  return -1
end
redis.call('HSET', KEYS[1], 'state', 'committed')
redis.call('PEXPIRE', KEYS[1], ARGV[2])
redis.call('ZREM', KEYS[2], KEYS[1])
return 1
`)

var rollbackNormalStockReservationScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 0 then
  redis.call('ZREM', KEYS[3], KEYS[2])
  return 0
end
if redis.call('HGET', KEYS[2], 'order_id') ~= ARGV[1]
  or redis.call('HGET', KEYS[2], 'state') ~= 'pending' then
  return -1
end
if redis.call('EXISTS', KEYS[1]) == 1 then
  redis.call('INCRBY', KEYS[1], tonumber(redis.call('HGET', KEYS[2], 'quantity')))
end
redis.call('DEL', KEYS[2])
redis.call('ZREM', KEYS[3], KEYS[2])
return 1
`)

var rollbackRushStockReservationScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[4]) == 0 then
  redis.call('ZREM', KEYS[5], KEYS[4])
  return 0
end
if redis.call('HGET', KEYS[4], 'order_id') ~= ARGV[1]
  or redis.call('HGET', KEYS[4], 'state') ~= 'pending' then
  return -1
end
local quantity = tonumber(redis.call('HGET', KEYS[4], 'quantity'))
if redis.call('EXISTS', KEYS[1]) == 1 then
  redis.call('INCRBY', KEYS[1], quantity)
end
if redis.call('EXISTS', KEYS[2]) == 1 then
  redis.call('INCRBY', KEYS[2], quantity)
end
local bought = redis.call('DECRBY', KEYS[3], quantity)
if bought <= 0 then
  redis.call('DEL', KEYS[3])
end
redis.call('DEL', KEYS[4])
redis.call('ZREM', KEYS[5], KEYS[4])
return 1
`)

type stockReservationResult struct {
	Key       string
	OrderID   int64
	BucketNo  int
	Remaining int64
	Created   bool
}

type stockReservation struct {
	Key            string
	OrderID        int64
	UserID         int64
	TierID         int64
	CampaignID     int64
	Quantity       int
	StockBucketNo  int
	RushBucketNo   int
	IdempotencyKey string
	Kind           string
	State          string
}

type stockReservationRecoveryOutcome string

const (
	stockReservationRecoveryConfirmed  stockReservationRecoveryOutcome = "confirmed"
	stockReservationRecoveryRolledBack stockReservationRecoveryOutcome = "rolled_back"
)

func stockReservationKey(userID int64, idempotencyKey string) string {
	sum := sha256.Sum256([]byte(idempotencyKey))
	return fmt.Sprintf("%s%d:%x", stockReservationPrefix, userID, sum[:16])
}

func stockReservationTTLMillis() int64 {
	return stockReservationTTL.Milliseconds()
}

func detachedReservationContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.Background()
	if parent != nil {
		base = context.WithoutCancel(parent)
	}
	return context.WithTimeout(base, 3*time.Second)
}

func decodeStockReservationResult(raw interface{}, key string) (stockReservationResult, int64, error) {
	values, ok := raw.([]interface{})
	if !ok || len(values) != 4 {
		return stockReservationResult{}, 0, fmt.Errorf("解析 Redis 预扣结果: %#v", raw)
	}
	code, err := redisInt64(values[0])
	if err != nil {
		return stockReservationResult{}, 0, err
	}
	if code < 0 {
		return stockReservationResult{}, code, nil
	}
	orderID, err := redisInt64(values[1])
	if err != nil {
		return stockReservationResult{}, 0, err
	}
	remaining, err := redisInt64(values[2])
	if err != nil {
		return stockReservationResult{}, 0, err
	}
	bucket, err := redisInt64(values[3])
	if err != nil {
		return stockReservationResult{}, 0, err
	}
	return stockReservationResult{
		Key: key, OrderID: orderID, BucketNo: int(bucket), Remaining: remaining,
		Created: code == stockReservationCodeCreated,
	}, code, nil
}

func redisInt64(value interface{}) (int64, error) {
	switch typed := value.(type) {
	case int64:
		return typed, nil
	case string:
		return strconv.ParseInt(typed, 10, 64)
	case []byte:
		return strconv.ParseInt(string(typed), 10, 64)
	default:
		return 0, fmt.Errorf("解析 Redis 整数: %T(%v)", value, value)
	}
}

func reservationMutationResult(cmd *redis.Cmd) error {
	result, err := cmd.Int64()
	if err != nil {
		return err
	}
	if result < 0 {
		return fmt.Errorf("Redis 预扣凭证条件不匹配")
	}
	return nil
}

func (s *TicketOrderService) confirmStockReservation(parent context.Context, reservation stockReservationResult) {
	if reservation.Key == "" || reservation.OrderID <= 0 {
		return
	}
	ctx, cancel := detachedReservationContext(parent)
	defer cancel()
	if err := reservationMutationResult(confirmStockReservationScript.Run(
		ctx,
		s.rdb,
		[]string{reservation.Key, stockReservationPendingKey},
		strconv.FormatInt(reservation.OrderID, 10),
		stockReservationTTLMillis(),
	)); err != nil {
		log.Printf("confirm stock reservation order=%d: %v", reservation.OrderID, err)
	}
}

func (s *TicketOrderService) rollbackStockReservation(parent context.Context, reservation stockReservationResult) {
	if reservation.Key == "" || reservation.OrderID <= 0 {
		return
	}
	ctx, cancel := detachedReservationContext(parent)
	defer cancel()
	record, err := s.loadStockReservation(ctx, reservation.Key)
	if err != nil {
		log.Printf("load stock reservation for rollback order=%d: %v", reservation.OrderID, err)
		return
	}
	if record == nil {
		return
	}
	if err := s.rollbackLoadedStockReservation(ctx, *record); err != nil {
		log.Printf("rollback stock reservation order=%d: %v", reservation.OrderID, err)
	}
}

func (s *TicketOrderService) loadStockReservation(ctx context.Context, key string) (*stockReservation, error) {
	values, err := s.rdb.HGetAll(ctx, key).Result()
	if err != nil {
		return nil, err
	}
	if len(values) == 0 {
		return nil, nil
	}
	parse := func(field string) (int64, error) {
		value, ok := values[field]
		if !ok {
			return 0, fmt.Errorf("预扣凭证缺少字段 %s", field)
		}
		return strconv.ParseInt(value, 10, 64)
	}
	orderID, err := parse("order_id")
	if err != nil {
		return nil, err
	}
	userID, err := parse("user_id")
	if err != nil {
		return nil, err
	}
	tierID, err := parse("tier_id")
	if err != nil {
		return nil, err
	}
	campaignID, err := parse("campaign_id")
	if err != nil {
		return nil, err
	}
	quantity, err := parse("quantity")
	if err != nil {
		return nil, err
	}
	stockBucketNo, err := parse("stock_bucket_no")
	if err != nil {
		return nil, err
	}
	rushBucketNo, err := parse("rush_bucket_no")
	if err != nil {
		return nil, err
	}
	return &stockReservation{
		Key: key, OrderID: orderID, UserID: userID, TierID: tierID,
		CampaignID: campaignID, Quantity: int(quantity), StockBucketNo: int(stockBucketNo),
		RushBucketNo: int(rushBucketNo), IdempotencyKey: values["idempotency_key"],
		Kind: values["kind"], State: values["state"],
	}, nil
}

func (s *TicketOrderService) rollbackLoadedStockReservation(ctx context.Context, reservation stockReservation) error {
	if reservation.State != stockReservationStatePending {
		return nil
	}
	orderID := strconv.FormatInt(reservation.OrderID, 10)
	if reservation.Kind == stockReservationKindRush {
		stockKey := ticketStockKey(reservation.TierID)
		rushKey := rushStockKey(reservation.CampaignID)
		if s.inventory.Enabled {
			stockKey = TicketStockBucketKey(reservation.TierID, reservation.StockBucketNo)
			rushKey = RushStockBucketKey(reservation.CampaignID, reservation.RushBucketNo)
		}
		return reservationMutationResult(rollbackRushStockReservationScript.Run(
			ctx,
			s.rdb,
			[]string{
				rushKey,
				stockKey,
				rushUserCountKey(reservation.CampaignID, reservation.UserID),
				reservation.Key,
				stockReservationPendingKey,
			},
			orderID,
		))
	}
	if reservation.Kind != stockReservationKindNormal {
		return fmt.Errorf("未知的预扣凭证类型 %q", reservation.Kind)
	}
	stockKey := ticketStockKey(reservation.TierID)
	if s.inventory.Enabled {
		stockKey = TicketStockBucketKey(reservation.TierID, reservation.StockBucketNo)
	}
	return reservationMutationResult(rollbackNormalStockReservationScript.Run(
		ctx,
		s.rdb,
		[]string{stockKey, reservation.Key, stockReservationPendingKey},
		orderID,
	))
}

// claimStockReservationRecovery 只在一次查库未命中后调用。
// 恢复任务插入 recovery 栅栏；订单事务插入 order 栅栏。order_id 唯一键会让后到者
// 等待先到事务提交，从而消除“扫描未命中后原事务又提交”的回滚竞态。
func (s *TicketOrderService) claimStockReservationRecovery(
	ctx context.Context,
	reservation stockReservation,
) (stockReservationRecoveryOutcome, error) {
	fence := models.TicketStockRecoveryFence{
		OrderID: reservation.OrderID,
		Owner:   models.TicketStockRecoveryFenceRecovery,
	}
	err := s.asyncDB().WithContext(ctx).Create(&fence).Error
	if err == nil {
		if err := s.rollbackLoadedStockReservation(ctx, reservation); err != nil {
			return "", err
		}
		return stockReservationRecoveryRolledBack, nil
	}
	if !isDuplicateStorageKeyError(err) {
		return "", err
	}

	if err := s.asyncDB().WithContext(ctx).
		Select("order_id", "owner").First(&fence, "order_id = ?", reservation.OrderID).Error; err != nil {
		return "", err
	}
	switch fence.Owner {
	case models.TicketStockRecoveryFenceOrder:
		// order 栅栏与订单、Outbox 同事务提交。再次查单是数据完整性校验，
		// 若人工改坏了栅栏，不允许猜测性归还库存。
		var order models.TicketOrder
		if err := s.asyncDB().WithContext(ctx).
			Select("id").Where("id = ? AND user_id = ?", reservation.OrderID, reservation.UserID).
			First(&order).Error; err != nil {
			return "", fmt.Errorf("order 栅栏存在但订单不可见: %w", err)
		}
		if err := reservationMutationResult(confirmStockReservationScript.Run(
			ctx, s.rdb, []string{reservation.Key, stockReservationPendingKey},
			strconv.FormatInt(reservation.OrderID, 10), stockReservationTTLMillis(),
		)); err != nil {
			return "", err
		}
		return stockReservationRecoveryConfirmed, nil
	case models.TicketStockRecoveryFenceRecovery:
		// 上一次恢复可能在写入栅栏后、修改 Redis 前中断；重复执行 Lua 是幂等的。
		if err := s.rollbackLoadedStockReservation(ctx, reservation); err != nil {
			return "", err
		}
		return stockReservationRecoveryRolledBack, nil
	default:
		return "", fmt.Errorf("未知的库存恢复栅栏 owner %q", fence.Owner)
	}
}

func (s *TicketOrderService) resolveMissingOrderStockReservation(
	ctx context.Context,
	reservation stockReservationResult,
) (stockReservationRecoveryOutcome, error) {
	record, err := s.loadStockReservation(ctx, reservation.Key)
	if err != nil {
		return "", err
	}
	if record == nil {
		return stockReservationRecoveryRolledBack, nil
	}
	if record.OrderID != reservation.OrderID {
		return "", fmt.Errorf(
			"Redis 预扣订单不匹配: key=%s got=%d want=%d",
			reservation.Key, record.OrderID, reservation.OrderID,
		)
	}
	return s.claimStockReservationRecovery(ctx, *record)
}

// pendingStockReservationKeys 返回仍有在途预扣影响的库存 key。
// 全量对账遇到这些 key 时不做缺失重建或偏少上调，避免覆盖正在执行的下单。
func (s *TicketOrderService) pendingStockReservationKeys(ctx context.Context) (map[string]struct{}, error) {
	reservationKeys, err := s.rdb.ZRange(ctx, stockReservationPendingKey, 0, -1).Result()
	if err != nil {
		return nil, err
	}
	stockKeys := make(map[string]struct{})
	for _, reservationKey := range reservationKeys {
		reservation, err := s.loadStockReservation(ctx, reservationKey)
		if err != nil {
			return nil, err
		}
		if reservation == nil || reservation.State != stockReservationStatePending {
			continue
		}
		ticketKey := ticketStockKey(reservation.TierID)
		if s.inventory.Enabled {
			ticketKey = TicketStockBucketKey(reservation.TierID, reservation.StockBucketNo)
		}
		stockKeys[ticketKey] = struct{}{}
		if reservation.Kind == stockReservationKindRush {
			rushKey := rushStockKey(reservation.CampaignID)
			if s.inventory.Enabled {
				rushKey = RushStockBucketKey(reservation.CampaignID, reservation.RushBucketNo)
			}
			stockKeys[rushKey] = struct{}{}
		}
	}
	return stockKeys, nil
}

func (s *TicketOrderService) observePendingStockReservationMetrics(
	ctx context.Context,
	now time.Time,
) error {
	count, err := s.rdb.ZCard(ctx, stockReservationPendingKey).Result()
	if err != nil {
		return err
	}
	metrics.StockReservationPending.Set(float64(count))
	if count == 0 {
		metrics.StockReservationOldestAgeSeconds.Set(0)
		return nil
	}
	oldest, err := s.rdb.ZRangeWithScores(ctx, stockReservationPendingKey, 0, 0).Result()
	if err != nil {
		return err
	}
	if len(oldest) == 0 {
		metrics.StockReservationOldestAgeSeconds.Set(0)
		return nil
	}
	age := now.Sub(time.UnixMilli(int64(oldest[0].Score))).Seconds()
	if age < 0 {
		age = 0
	}
	metrics.StockReservationOldestAgeSeconds.Set(age)
	return nil
}

func (s *TicketOrderService) RecoverStaleStockReservations(ctx context.Context, now time.Time) (int, error) {
	cutoff := now.Add(-stockReservationRecoveryGrace).UnixMilli()
	keys, err := s.rdb.ZRangeByScore(ctx, stockReservationPendingKey, &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(cutoff, 10), Count: stockReservationRecoveryBatch,
	}).Result()
	if err != nil {
		return 0, err
	}
	recovered := 0
	var recoveryErr error
	for _, key := range keys {
		reservation, loadErr := s.loadStockReservation(ctx, key)
		if loadErr != nil {
			log.Printf("load stale stock reservation key=%s: %v", key, loadErr)
			metrics.StockReservationRecoveryTotal.WithLabelValues("error").Inc()
			recoveryErr = errors.Join(recoveryErr, loadErr)
			continue
		}
		if reservation == nil {
			if err := s.rdb.ZRem(ctx, stockReservationPendingKey, key).Err(); err != nil {
				recoveryErr = errors.Join(recoveryErr, err)
			}
			continue
		}
		if reservation.State != stockReservationStatePending {
			if err := s.rdb.ZRem(ctx, stockReservationPendingKey, key).Err(); err != nil {
				recoveryErr = errors.Join(recoveryErr, err)
			}
			continue
		}

		var order models.TicketOrder
		dbErr := s.asyncDB().WithContext(ctx).
			Select("id").Where("id = ? AND user_id = ?", reservation.OrderID, reservation.UserID).
			First(&order).Error
		switch {
		case dbErr == nil:
			result := stockReservationResult{Key: reservation.Key, OrderID: reservation.OrderID}
			confirmCtx, cancel := detachedReservationContext(ctx)
			confirmErr := reservationMutationResult(confirmStockReservationScript.Run(
				confirmCtx, s.rdb, []string{result.Key, stockReservationPendingKey},
				strconv.FormatInt(result.OrderID, 10), stockReservationTTLMillis(),
			))
			cancel()
			if confirmErr == nil {
				metrics.StockReservationRecoveryTotal.WithLabelValues("confirmed").Inc()
				recovered++
			} else {
				metrics.StockReservationRecoveryTotal.WithLabelValues("error").Inc()
				recoveryErr = errors.Join(recoveryErr, confirmErr)
			}
		case errors.Is(dbErr, gorm.ErrRecordNotFound):
			rollbackCtx, cancel := detachedReservationContext(ctx)
			outcome, rollbackErr := s.claimStockReservationRecovery(rollbackCtx, *reservation)
			cancel()
			if rollbackErr == nil {
				metrics.StockReservationRecoveryTotal.WithLabelValues(string(outcome)).Inc()
				recovered++
			} else {
				metrics.StockReservationRecoveryTotal.WithLabelValues("error").Inc()
				recoveryErr = errors.Join(recoveryErr, rollbackErr)
			}
		default:
			log.Printf("query stale stock reservation order=%d: %v", reservation.OrderID, dbErr)
			metrics.StockReservationRecoveryTotal.WithLabelValues("error").Inc()
			recoveryErr = errors.Join(recoveryErr, dbErr)
		}
	}
	return recovered, recoveryErr
}

// RecoverAllStockReservations 在启动库存预热前处理上次进程遗留的全部 pending 凭证。
// 这样 WarmTicketQuota 随后按 MySQL 重建 Redis 时，不会留下一个稍后再次归还的旧凭证。
func (s *TicketOrderService) RecoverAllStockReservations(ctx context.Context) (int, error) {
	total := 0
	for {
		before, err := s.rdb.ZCard(ctx, stockReservationPendingKey).Result()
		if err != nil {
			return total, err
		}
		if before == 0 {
			return total, nil
		}
		recovered, err := s.RecoverStaleStockReservations(ctx, time.Now().Add(stockReservationRecoveryGrace))
		total += recovered
		if err != nil {
			return total, err
		}
		remaining, err := s.rdb.ZCard(ctx, stockReservationPendingKey).Result()
		if err != nil {
			return total, err
		}
		if remaining == 0 {
			return total, nil
		}
		if remaining >= before {
			return total, fmt.Errorf("仍有 %d 条 Redis pending 预扣无法恢复", remaining)
		}
	}
}
