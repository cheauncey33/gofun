package lock

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Lock struct {
	client *redis.Client
	key    string
	value  string
	ttl    time.Duration
}

func Acquire(ctx context.Context, client *redis.Client, key string, ttl time.Duration) (*Lock, error) {
	value := uuid.New().String()
	ok, err := client.SetNX(ctx, key, value, ttl).Result()
	if err != nil {
		return nil, fmt.Errorf("lock acquire failed: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("lock %s is held by another process", key)
	}
	return &Lock{client: client, key: key, value: value, ttl: ttl}, nil
}

var releaseLua = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end
`

func (l *Lock) Release(ctx context.Context) error {
	_, err := l.client.Eval(ctx, releaseLua, []string{l.key}, l.value).Result()
	return err
}

func WithLock(ctx context.Context, client *redis.Client, key string, ttl time.Duration, fn func() error) error {
	l, err := Acquire(ctx, client, key, ttl)
	if err != nil {
		return err
	}
	defer l.Release(ctx)
	return fn()
}
