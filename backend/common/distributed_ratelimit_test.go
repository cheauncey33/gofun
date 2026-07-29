package common

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestRedisSlidingWindowLimiterSharedAcrossInstances(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	first := NewRedisSlidingWindowLimiter(client, time.Second, 2, false)
	second := NewRedisSlidingWindowLimiter(client, time.Second, 2, false)
	key := "fuchang:rl:write:user:42"

	assertRateLimitResult(t, first, key, true)
	assertRateLimitResult(t, second, key, true)
	assertRateLimitResult(t, first, key, false)

	server.FastForward(2 * time.Second)
	assertRateLimitResult(t, second, key, true)
}

func assertRateLimitResult(
	t *testing.T,
	limiter *RedisSlidingWindowLimiter,
	key string,
	want bool,
) {
	t.Helper()
	got, err := limiter.Allow(context.Background(), key)
	if err != nil {
		t.Fatalf("Allow() error = %v", err)
	}
	if got != want {
		t.Fatalf("Allow() = %v, want %v", got, want)
	}
}
