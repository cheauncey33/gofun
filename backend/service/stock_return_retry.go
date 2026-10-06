package service

import (
	"context"
	"gofun/config"
	"math"
	"math/rand"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// 库存归还执行器。
const (
	stockReturnTaskPrefix = "fuchang:ticket:stock-return:task:"

	stockReturnKindTicket        = "ticket"
	stockReturnKindRushStock     = "rush-stock"
	stockReturnKindRushUserCount = "rush-usercount"

	stockReturnBatch = 200
)

// applyStockReturnScript 幂等归还库存。
// KEYS[1] 保存归还标记，HSETNX 保证同一 token 只执行一次 INCRBY。
var applyStockReturnScript = redis.NewScript(`
if redis.call('EXISTS', KEYS[2]) == 0 then
  return redis.error_reply('stock key missing')
end
if redis.call('HSETNX', KEYS[1], 'applied', '1') == 0 then
  return 0
end
local result = redis.pcall('INCRBY', KEYS[2], ARGV[1])
if type(result) == 'table' and result.err then
  redis.call('HDEL', KEYS[1], 'applied')
  return redis.error_reply(result.err)
end
return 1
`)

// StockReturnRetrySettings 归还重试运行时参数。
type StockReturnRetrySettings struct {
	MaxAttempts int
	BaseDelayMS int
	MaxDelayMS  int
	DedupTTLSec int
}

func NewStockReturnRetrySettings(cfg config.InventoryConfig) StockReturnRetrySettings {
	s := StockReturnRetrySettings{
		MaxAttempts: cfg.ReturnRetryMaxAttempts,
		BaseDelayMS: cfg.ReturnRetryBaseDelayMS,
		MaxDelayMS:  cfg.ReturnRetryMaxDelayMS,
		DedupTTLSec: cfg.ReturnDedupTTLSec,
	}
	if s.MaxAttempts <= 0 {
		s.MaxAttempts = 12
	}
	if s.BaseDelayMS <= 0 {
		s.BaseDelayMS = 1000
	}
	if s.MaxDelayMS < s.BaseDelayMS {
		s.MaxDelayMS = s.BaseDelayMS
	}
	if s.DedupTTLSec <= 0 {
		s.DedupTTLSec = 7 * 24 * 3600
	}
	return s
}

// delayMillis 指数退避 + 抖动，避免 Redis 抖动恢复瞬间所有任务同时重放。
func (s StockReturnRetrySettings) delayMillis(attempts int) int64 {
	if attempts < 1 {
		attempts = 1
	}
	delay := float64(s.BaseDelayMS) * math.Pow(2, float64(attempts-1))
	if delay >= float64(s.MaxDelayMS) {
		return int64(s.MaxDelayMS)
	}
	jitter := float64(s.BaseDelayMS) * rand.Float64()
	return int64(delay) + int64(jitter)
}

type stockReturnRetry struct {
	rdb *redis.Client
	cfg StockReturnRetrySettings
}

type stockReturnTask struct {
	Token    string
	StockKey string
	Quantity int
	OrderID  int64
	UserID   int64
	Reason   string
	Kind     string
}

func stockReturnTaskKey(token string) string {
	return stockReturnTaskPrefix + token
}

// stockReturnToken 以「订单 + 归还类型」作为幂等键。
// 同一订单的同一类归还最多发生一次，因此该 token 天然唯一且可安全重放。
func stockReturnToken(orderID int64, kind string) string {
	return strconv.FormatInt(orderID, 10) + ":" + kind
}

// execute 幂等归还一次库存。changed=0 表示该 token 此前已补偿过。
func (r *stockReturnRetry) execute(ctx context.Context, task stockReturnTask) (changed bool, err error) {
	result, err := applyStockReturnScript.Run(
		ctx,
		r.rdb,
		[]string{stockReturnTaskKey(task.Token), task.StockKey},
		strconv.Itoa(task.Quantity),
	).Int64()
	if err != nil {
		return false, err
	}
	return result == 1, nil
}
