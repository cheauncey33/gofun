package service

import (
	"context"
	"errors"
	"log"
	"time"

	"WHU_Snack_GO/metrics"
	redislock "WHU_Snack_GO/pkg/lock"

	"github.com/redis/go-redis/v9"
)

const eventSearchSyncLockKey = "lock:event-search:sync"

type EventSearchCompensationService struct {
	catalog  *TicketCatalogService
	rdb      *redis.Client
	interval time.Duration
	lockTTL  time.Duration
}

func NewEventSearchCompensationService(
	catalog *TicketCatalogService,
	rdb *redis.Client,
	interval, lockTTL time.Duration,
) *EventSearchCompensationService {
	return &EventSearchCompensationService{
		catalog:  catalog,
		rdb:      rdb,
		interval: interval,
		lockTTL:  lockTTL,
	}
}

func (s *EventSearchCompensationService) Run(ctx context.Context) (EventSearchSyncResult, error) {
	var empty EventSearchSyncResult
	if s == nil || s.catalog == nil || !s.catalog.searcher.Enabled() {
		return empty, nil
	}
	lockTTL := s.lockTTL
	if lockTTL <= 0 {
		lockTTL = 5 * time.Minute
	}
	held, err := redislock.Acquire(ctx, s.rdb, eventSearchSyncLockKey, lockTTL)
	if errors.Is(err, redislock.ErrAlreadyHeld) {
		metrics.EventSearchSyncRuns.WithLabelValues("skipped").Inc()
		return empty, nil
	}
	if err != nil {
		metrics.EventSearchSyncRuns.WithLabelValues("error").Inc()
		return empty, err
	}
	defer func() {
		releaseCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := held.Release(releaseCtx); err != nil {
			log.Printf("ES 活动对账锁释放失败: %v", err)
		}
	}()

	result, err := s.catalog.SyncPublishedEvents(ctx)
	if err != nil {
		metrics.EventSearchSyncRuns.WithLabelValues("error").Inc()
		return result, err
	}
	if result.Missing > 0 || result.Deleted > 0 {
		metrics.EventSearchSyncRuns.WithLabelValues("anomalies_found").Inc()
	} else {
		metrics.EventSearchSyncRuns.WithLabelValues("success").Inc()
	}
	return result, nil
}

func (s *EventSearchCompensationService) Start(ctx context.Context) {
	if s == nil || s.interval <= 0 {
		return
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			result, err := s.Run(ctx)
			if err != nil {
				log.Printf("ES 活动对账失败: %v", err)
				continue
			}
			if result.Missing > 0 || result.Deleted > 0 {
				log.Printf(
					"ES 活动对账完成: indexed=%d missing=%d deleted=%d",
					result.Indexed, result.Missing, result.Deleted,
				)
			}
		}
	}
}
