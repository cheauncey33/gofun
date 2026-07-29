package common

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"WHU_Snack_GO/metrics"
	"WHU_Snack_GO/pkg/response"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

var slidingWindowScript = redis.NewScript(`
local now_parts = redis.call("TIME")
local now_ms = tonumber(now_parts[1]) * 1000 + math.floor(tonumber(now_parts[2]) / 1000)
local window_ms = tonumber(ARGV[1])
local limit = tonumber(ARGV[2])
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now_ms - window_ms)
local current = redis.call("ZCARD", KEYS[1])
if current >= limit then
    redis.call("PEXPIRE", KEYS[1], window_ms + 1000)
    return {0, current}
end
redis.call("ZADD", KEYS[1], now_ms, ARGV[3])
redis.call("PEXPIRE", KEYS[1], window_ms + 1000)
return {1, current + 1}
`)

type RedisSlidingWindowLimiter struct {
	client   redis.Scripter
	window   time.Duration
	limit    int64
	failOpen bool
}

func NewRedisSlidingWindowLimiter(
	client redis.Scripter,
	window time.Duration,
	limit int64,
	failOpen bool,
) *RedisSlidingWindowLimiter {
	return &RedisSlidingWindowLimiter{
		client:   client,
		window:   window,
		limit:    limit,
		failOpen: failOpen,
	}
}

func (l *RedisSlidingWindowLimiter) Allow(ctx context.Context, key string) (bool, error) {
	if l == nil || l.client == nil {
		return false, fmt.Errorf("distributed rate limiter is not initialized")
	}
	if l.window <= 0 || l.limit <= 0 {
		return false, fmt.Errorf("invalid distributed rate limiter configuration")
	}
	result, err := slidingWindowScript.Run(
		ctx,
		l.client,
		[]string{key},
		l.window.Milliseconds(),
		l.limit,
		uuid.NewString(),
	).Slice()
	if err != nil {
		return false, err
	}
	if len(result) < 1 {
		return false, fmt.Errorf("invalid distributed rate limiter response")
	}
	allowed, err := strconv.ParseInt(fmt.Sprint(result[0]), 10, 64)
	if err != nil {
		return false, fmt.Errorf("parse distributed rate limiter response: %w", err)
	}
	return allowed == 1, nil
}

func DistributedWriteRateLimitMiddleware(limiter *RedisSlidingWindowLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := "fuchang:rl:write:ip:" + c.ClientIP()
		if userID, ok := GetUserID(c); ok {
			key = fmt.Sprintf("fuchang:rl:write:user:%d", userID)
		}
		allowed, err := limiter.Allow(c.Request.Context(), key)
		if err != nil {
			if limiter != nil && limiter.failOpen {
				metrics.DistributedRateLimitRequests.WithLabelValues("error_fail_open").Inc()
				log.Printf("分布式写限流 Redis 异常，按配置放行: %v", err)
				c.Next()
				return
			}
			metrics.DistributedRateLimitRequests.WithLabelValues("error_fail_closed").Inc()
			log.Printf("分布式写限流 Redis 异常，拒绝请求: %v", err)
			response.Error(c, http.StatusServiceUnavailable, response.CodeRedisError, "限流服务暂不可用")
			c.Abort()
			return
		}
		if !allowed {
			metrics.DistributedRateLimitRequests.WithLabelValues("rejected").Inc()
			response.Error(c, http.StatusTooManyRequests, response.CodeTooManyRequests, "操作过于频繁，请稍后再试")
			c.Abort()
			return
		}
		metrics.DistributedRateLimitRequests.WithLabelValues("allowed").Inc()
		c.Next()
	}
}
