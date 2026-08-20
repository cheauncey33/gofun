package service

import (
	"context"
	"encoding/json"
	"fmt"
	"gofun/models"
	"log"
	"math/rand"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"
)

// Browse-side Redis cache: short TTL (+ jitter) + version bump / delete on writes.
// Intentionally not a full consistency system — stale remaining_quota for tens
// of seconds on list/detail is acceptable for browsing.
//
// Miss paths should go through singleflight (per-service groups) to avoid
// stampedes. Negative lookups may cache a short-lived empty sentinel.

const (
	catalogCachePrefix      = "fuchang:catalog:"
	catalogListVersionKey   = catalogCachePrefix + "events:list:ver"
	catalogMetaKey          = catalogCachePrefix + "meta"
	catalogRushSalesListKey = catalogCachePrefix + "rush_sales:list"
	catalogEventsListTTL    = 45 * time.Second
	catalogEventDetailTTL   = 45 * time.Second
	catalogMetaTTL          = 15 * time.Minute
	catalogRushSalesListTTL = 30 * time.Second // meta only; stock overlaid from Redis
	catalogEmptyTTL         = 5 * time.Second
	cacheEmptySentinel      = "__empty__"
)

func catalogEventKey(eventID int64) string {
	return fmt.Sprintf("%sevent:%d", catalogCachePrefix, eventID)
}

func catalogEventsListKey(version, city, category, keyword string, page, pageSize int) string {
	return fmt.Sprintf(
		"%sevents:list:v=%s:city=%s:cat=%s:kw=%s:p=%d:ps=%d",
		catalogCachePrefix,
		version,
		strings.TrimSpace(city),
		strings.TrimSpace(category),
		strings.TrimSpace(keyword),
		page,
		pageSize,
	)
}

type cachedEventList struct {
	Events []models.Event `json:"events"`
	Total  int64          `json:"total"`
}

// jitterTTL adds 0~25% to base so mass-written keys do not expire together.
func jitterTTL(base time.Duration) time.Duration {
	if base <= 0 {
		return base
	}
	span := base / 4
	if span <= 0 {
		return base
	}
	return base + time.Duration(rand.Int63n(int64(span)+1))
}

func redisCacheGetJSON[T any](ctx context.Context, rdb *redis.Client, key string, dest *T) (bool, error) {
	if rdb == nil {
		return false, nil
	}
	raw, err := rdb.Get(ctx, key).Bytes()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if string(raw) == cacheEmptySentinel {
		return false, errCacheEmpty
	}
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

var errCacheEmpty = fmt.Errorf("cache empty sentinel")

func redisCacheSetJSON(ctx context.Context, rdb *redis.Client, key string, value any, ttl time.Duration) {
	if rdb == nil || ttl <= 0 {
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		log.Printf("[catalog-cache] marshal %s: %v", key, err)
		return
	}
	if err := rdb.Set(ctx, key, body, jitterTTL(ttl)).Err(); err != nil {
		log.Printf("[catalog-cache] set %s: %v", key, err)
	}
}

func redisCacheSetEmpty(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) {
	if rdb == nil {
		return
	}
	if ttl <= 0 {
		ttl = catalogEmptyTTL
	}
	if err := rdb.Set(ctx, key, cacheEmptySentinel, jitterTTL(ttl)).Err(); err != nil {
		log.Printf("[catalog-cache] set empty %s: %v", key, err)
	}
}

// cacheAsideJSON loads from Redis or runs load once per key via sf.
// load may return errCacheEmpty (or wrap it) to store a short negative cache.
func cacheAsideJSON[T any](
	ctx context.Context,
	rdb *redis.Client,
	sf *singleflight.Group,
	key string,
	ttl time.Duration,
	dest *T,
	load func(context.Context) (T, error),
) error {
	ok, err := redisCacheGetJSON(ctx, rdb, key, dest)
	if err == errCacheEmpty {
		return ErrTicketResourceNotFound
	}
	if err != nil {
		log.Printf("[catalog-cache] get %s: %v", key, err)
	}
	if ok {
		return nil
	}
	if sf == nil {
		val, loadErr := load(ctx)
		if loadErr != nil {
			if errorsIsEmpty(loadErr) {
				redisCacheSetEmpty(ctx, rdb, key, catalogEmptyTTL)
			}
			return loadErr
		}
		*dest = val
		redisCacheSetJSON(ctx, rdb, key, val, ttl)
		return nil
	}
	v, loadErr, _ := sf.Do(key, func() (interface{}, error) {
		var again T
		if hit, e := redisCacheGetJSON(ctx, rdb, key, &again); e == nil && hit {
			return again, nil
		} else if e == errCacheEmpty {
			return nil, ErrTicketResourceNotFound
		}
		val, e := load(ctx)
		if e != nil {
			if errorsIsEmpty(e) {
				redisCacheSetEmpty(ctx, rdb, key, catalogEmptyTTL)
			}
			return nil, e
		}
		redisCacheSetJSON(ctx, rdb, key, val, ttl)
		return val, nil
	})
	if loadErr != nil {
		return loadErr
	}
	*dest = v.(T)
	return nil
}

func errorsIsEmpty(err error) bool {
	return err != nil && (err == ErrTicketResourceNotFound || err == errCacheEmpty)
}

func (s *TicketCatalogService) catalogListVersion(ctx context.Context) string {
	if s.rdb == nil {
		return "0"
	}
	ver, err := s.rdb.Get(ctx, catalogListVersionKey).Result()
	if err == redis.Nil || ver == "" {
		return "0"
	}
	if err != nil {
		return "0"
	}
	return ver
}

func (s *TicketCatalogService) invalidateCatalogCaches(ctx context.Context, eventID int64) {
	if s.rdb == nil {
		return
	}
	pipe := s.rdb.Pipeline()
	pipe.Incr(ctx, catalogListVersionKey)
	pipe.Del(ctx, catalogMetaKey)
	pipe.Del(ctx, catalogRushSalesListKey)
	if eventID > 0 {
		pipe.Del(ctx, catalogEventKey(eventID))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		log.Printf("[catalog-cache] invalidate event=%d: %v", eventID, err)
	}
}

func (s *RushSaleService) invalidateRushSalesListCache(ctx context.Context) {
	if s.rdb == nil {
		return
	}
	if err := s.rdb.Del(ctx, catalogRushSalesListKey).Err(); err != nil {
		log.Printf("[catalog-cache] invalidate rush list: %v", err)
	}
}
