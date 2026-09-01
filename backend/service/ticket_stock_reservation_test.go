package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"gofun/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func newStockReservationTestService(t *testing.T) (*TicketOrderService, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return &TicketOrderService{rdb: rdb}, mr
}

func mustRedisValue(t *testing.T, mr *miniredis.Miniredis, key string) string {
	t.Helper()
	value, err := mr.Get(key)
	if err != nil {
		t.Fatalf("get Redis key %s: %v", key, err)
	}
	return value
}

func TestNormalStockReservationRetryDoesNotDeductTwice(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	tier := &models.TicketTier{Base: models.Base{ID: 71}, TotalQuota: 5}
	mr.Set(ticketStockKey(tier.ID), "5")

	// 使用大于 2^53 的 ID，确保 Lua 始终把 Snowflake ID 当字符串传递。
	const firstOrderID int64 = 9007199254740993
	first, code, err := svc.reserveTicketStock(
		t.Context(), 11, tier, 2, "idem-normal-retry", firstOrderID,
	)
	if err != nil {
		t.Fatalf("first reserve: %v", err)
	}
	if code != stockReservationCodeCreated || !first.Created || first.OrderID != firstOrderID {
		t.Fatalf("first reserve = %#v code=%d", first, code)
	}
	if got := mustRedisValue(t, mr, ticketStockKey(tier.ID)); got != "3" {
		t.Fatalf("stock after first reserve = %s, want 3", got)
	}
	if got := mustRedisValue(t, mr, stockIdempotencyKey(11, ticketOrderOperationNormal, "idem-normal-retry")); got != strconv.FormatInt(firstOrderID, 10) {
		t.Fatalf("idempotency mapping = %s, want order %d", got, firstOrderID)
	}
	if !mr.Exists(stockReservationKey(firstOrderID)) {
		t.Fatalf("reservation must be addressed by order_id=%d", firstOrderID)
	}

	// 模拟第一次 Lua 已执行、但 HTTP 没拿到结果：同一幂等键再次进入预扣。
	second, code, err := svc.reserveTicketStock(
		t.Context(), 11, tier, 2, "idem-normal-retry", firstOrderID+1,
	)
	if err != nil {
		t.Fatalf("retry reserve: %v", err)
	}
	if code != stockReservationCodeExisting || second.Created || second.OrderID != firstOrderID {
		t.Fatalf("retry reserve = %#v code=%d", second, code)
	}
	if got := mustRedisValue(t, mr, ticketStockKey(tier.ID)); got != "3" {
		t.Fatalf("stock after retry = %s, want 3", got)
	}

	svc.confirmStockReservation(t.Context(), second)
	if got := mr.HGet(second.Key, "state"); got != stockReservationStateCommitted {
		t.Fatalf("reservation state = %q, want committed", got)
	}
	if members, _ := mr.ZMembers(stockReservationPendingKey); len(members) != 0 {
		t.Fatalf("pending reservations after confirm = %v", members)
	}
}

func TestStockReservationRejectsIdempotencyPayloadConflict(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	tier := &models.TicketTier{Base: models.Base{ID: 72}, TotalQuota: 5}
	mr.Set(ticketStockKey(tier.ID), "5")

	_, code, err := svc.reserveTicketStock(
		t.Context(), 12, tier, 1, "idem-normal-conflict", 7201,
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("first reserve code=%d err=%v", code, err)
	}
	_, code, err = svc.reserveTicketStock(
		t.Context(), 12, tier, 2, "idem-normal-conflict", 7202,
	)
	if err != nil {
		t.Fatalf("conflict reserve transport error: %v", err)
	}
	if code != stockReservationCodeConflict {
		t.Fatalf("conflict reserve code=%d, want %d", code, stockReservationCodeConflict)
	}
	if got := mustRedisValue(t, mr, ticketStockKey(tier.ID)); got != "4" {
		t.Fatalf("stock after conflict = %s, want 4", got)
	}
}

func TestRushReservationRollbackUsesDetachedContextAndIsIdempotent(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	rush := &RushSaleService{rdb: svc.rdb, order: svc}
	campaign := &models.RushSaleCampaign{
		Base: models.Base{ID: 81}, TicketTierID: 82, TotalQuota: 4, PerUserLimit: 2,
		EndsAt: time.Now().Add(time.Hour),
	}
	mr.Set(rushStockKey(campaign.ID), "4")
	mr.Set(ticketStockKey(campaign.TicketTierID), "4")

	reservation, code, err := rush.reserveRushStock(
		t.Context(), 13, campaign, 2, 3600, "idem-rush-cancelled", 8101,
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve rush code=%d err=%v result=%#v", code, err, reservation)
	}
	if got := mustRedisValue(t, mr, rushStockKey(campaign.ID)); got != "2" {
		t.Fatalf("rush stock after reserve = %s, want 2", got)
	}
	if got := mustRedisValue(t, mr, rushUserCountKey(campaign.ID, 13)); got != "2" {
		t.Fatalf("user count after reserve = %s, want 2", got)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	svc.rollbackStockReservation(cancelled, reservation)
	if got := mustRedisValue(t, mr, rushStockKey(campaign.ID)); got != "4" {
		t.Fatalf("rush stock after rollback = %s, want 4", got)
	}
	if got := mustRedisValue(t, mr, ticketStockKey(campaign.TicketTierID)); got != "4" {
		t.Fatalf("ticket stock after rollback = %s, want 4", got)
	}
	if mr.Exists(rushUserCountKey(campaign.ID, 13)) {
		t.Fatal("user count should be deleted after rollback")
	}
	if mr.Exists(reservation.Key) {
		t.Fatal("reservation should be deleted after rollback")
	}
	if mr.Exists(stockIdempotencyKey(13, ticketOrderOperationRush, "idem-rush-cancelled")) {
		t.Fatal("rolled back reservation must release its idempotency-to-order mapping")
	}

	// 第二次回滚必须是 no-op，不能把库存加到 6。
	svc.rollbackStockReservation(context.Background(), reservation)
	if got := mustRedisValue(t, mr, rushStockKey(campaign.ID)); got != "4" {
		t.Fatalf("rush stock after duplicate rollback = %s, want 4", got)
	}
}

func TestRushReservationRollbackPreservesOtherUserCountTTL(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	rush := &RushSaleService{rdb: svc.rdb, order: svc}
	campaign := &models.RushSaleCampaign{
		Base: models.Base{ID: 91}, TicketTierID: 92, TotalQuota: 4, PerUserLimit: 2,
		EndsAt: time.Now().Add(time.Hour),
	}
	mr.Set(rushStockKey(campaign.ID), "4")
	mr.Set(ticketStockKey(campaign.TicketTierID), "4")

	first, firstCode, firstErr := rush.reserveRushStock(
		t.Context(), 14, campaign, 1, 3600, "idem-rush-first", 9101,
	)
	_, secondCode, secondErr := rush.reserveRushStock(
		t.Context(), 14, campaign, 1, 3600, "idem-rush-second", 9102,
	)
	if firstErr != nil || secondErr != nil ||
		firstCode != stockReservationCodeCreated || secondCode != stockReservationCodeCreated {
		t.Fatalf("reserve two rush requests first=(%d,%v) second=(%d,%v)", firstCode, firstErr, secondCode, secondErr)
	}

	svc.rollbackStockReservation(t.Context(), first)
	userCountKey := rushUserCountKey(campaign.ID, 14)
	if got := mustRedisValue(t, mr, userCountKey); got != "1" {
		t.Fatalf("user count after one rollback = %s, want 1", got)
	}
	if ttl := mr.TTL(userCountKey); ttl <= 0 {
		t.Fatalf("user count TTL after rollback = %s, want positive", ttl)
	}
}

func TestRollbackDoesNotRecreateMissingStockKey(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	tier := &models.TicketTier{Base: models.Base{ID: 101}, TotalQuota: 3}
	mr.Set(ticketStockKey(tier.ID), "3")

	reservation, code, err := svc.reserveTicketStock(
		t.Context(), 21, tier, 1, "idem-missing-stock", 10101,
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve code=%d err=%v", code, err)
	}
	mr.Del(ticketStockKey(tier.ID))
	svc.rollbackStockReservation(t.Context(), reservation)

	if mr.Exists(ticketStockKey(tier.ID)) {
		t.Fatal("rollback must not rebuild a missing aggregate stock key from one reservation")
	}
	if mr.Exists(reservation.Key) {
		t.Fatal("reservation should be removed so full reconciliation can rebuild safely")
	}
}

func TestPendingStockReservationKeys(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	tier := &models.TicketTier{Base: models.Base{ID: 111}, TotalQuota: 3}
	mr.Set(ticketStockKey(tier.ID), "3")

	reservation, code, err := svc.reserveTicketStock(
		t.Context(), 22, tier, 1, "idem-pending-keys", 11101,
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve code=%d err=%v", code, err)
	}
	keys, err := svc.pendingStockReservationKeys(t.Context())
	if err != nil {
		t.Fatalf("pending keys: %v", err)
	}
	if _, ok := keys[ticketStockKey(tier.ID)]; !ok {
		t.Fatalf("pending stock keys=%v, want %s", keys, ticketStockKey(tier.ID))
	}

	svc.confirmStockReservation(t.Context(), reservation)
	keys, err = svc.pendingStockReservationKeys(t.Context())
	if err != nil {
		t.Fatalf("pending keys after confirm: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("pending stock keys after confirm=%v, want empty", keys)
	}
}

func TestPendingReservationHashHasNoTTL(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	tier := &models.TicketTier{Base: models.Base{ID: 121}, TotalQuota: 3}
	mr.Set(ticketStockKey(tier.ID), "3")

	reservation, code, err := svc.reserveTicketStock(
		t.Context(), 23, tier, 1, "idem-pending-ttl", 12101,
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve code=%d err=%v", code, err)
	}
	if ttl := mr.TTL(reservation.Key); ttl > 0 {
		t.Fatalf("pending reservation TTL=%s, want none so recovery can still read quantity", ttl)
	}

	svc.confirmStockReservation(t.Context(), reservation)
	if ttl := mr.TTL(reservation.Key); ttl <= 0 {
		t.Fatalf("committed reservation TTL=%s, want positive cleanup window", ttl)
	}
}

func TestRecoverAllLeavesFreshReservations(t *testing.T) {
	svc, mr := newStockReservationTestService(t)
	tier := &models.TicketTier{Base: models.Base{ID: 131}, TotalQuota: 3}
	mr.Set(ticketStockKey(tier.ID), "3")

	reservation, code, err := svc.reserveTicketStock(
		t.Context(), 24, tier, 1, "idem-recover-all-fresh", 13101,
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve code=%d err=%v", code, err)
	}

	recovered, err := svc.RecoverAllStockReservations(t.Context())
	if err != nil || recovered != 0 {
		t.Fatalf("recover all fresh recovered=%d err=%v", recovered, err)
	}
	if !mr.Exists(reservation.Key) {
		t.Fatal("fresh pending reservation must survive startup recovery")
	}
	if got := mustRedisValue(t, mr, ticketStockKey(tier.ID)); got != "2" {
		t.Fatalf("stock after recover-all=%s, want 2", got)
	}
}
