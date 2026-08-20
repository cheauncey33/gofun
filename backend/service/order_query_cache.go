package service

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// User-order browse cache: short TTL + version bump on writes.
// List/detail may lag a few seconds after status changes; detail pages that
// need freshness can still hit GetOrder after a write from the same client.

const (
	orderQueryCachePrefix = "fuchang:orders:"
	orderListTTL          = 8 * time.Second
	orderDetailTTL        = 8 * time.Second
	orderCountTTL         = 8 * time.Second
)

type cachedOrderList struct {
	Orders []ticketOrderListJSON `json:"orders"`
	Total  int64                 `json:"total"`
}

// ticketOrderListJSON keeps the wire shape OrderList.vue expects (items[0] snapshots)
// without forcing a full Preload of every association.
type ticketOrderListJSON struct {
	ID               int64                  `json:"id,string"`
	OrderNo          string                 `json:"order_no"`
	UserID           int64                  `json:"user_id,string"`
	OrganizerID      int64                  `json:"organizer_id,string"`
	EventID          int64                  `json:"event_id,string"`
	SessionID        int64                  `json:"session_id,string"`
	Status           string                 `json:"status"`
	PaymentStatus    string                 `json:"payment_status"`
	TotalAmountCents int64                  `json:"total_amount_cents"`
	OrderSource      string                 `json:"order_source"`
	ExpiresAt        time.Time              `json:"expires_at"`
	PaidAt           *time.Time             `json:"paid_at,omitempty"`
	CancelledAt      *time.Time             `json:"cancelled_at,omitempty"`
	CreateTime       time.Time              `json:"create_time"`
	UpdateTime       time.Time              `json:"update_time"`
	Items            []ticketOrderItemBrief `json:"items,omitempty"`
}

type ticketOrderItemBrief struct {
	TicketTierID       int64  `json:"ticket_tier_id,string"`
	Quantity           int    `json:"quantity"`
	EventTitleSnapshot string `json:"event_title_snapshot"`
	TierNameSnapshot   string `json:"tier_name_snapshot"`
	VenueNameSnapshot  string `json:"venue_name_snapshot"`
}

func orderUserVersionKey(userID int64) string {
	return fmt.Sprintf("%suser:%d:ver", orderQueryCachePrefix, userID)
}

func orderListCacheKey(userID int64, version string, page, pageSize int) string {
	return fmt.Sprintf("%slist:u=%d:v=%s:p=%d:ps=%d", orderQueryCachePrefix, userID, version, page, pageSize)
}

func orderDetailCacheKey(userID, orderID int64, version string) string {
	return fmt.Sprintf("%sdetail:u=%d:o=%d:v=%s", orderQueryCachePrefix, userID, orderID, version)
}

func orderCountCacheKey(userID int64, version string) string {
	return fmt.Sprintf("%scount:u=%d:v=%s", orderQueryCachePrefix, userID, version)
}

func (s *TicketOrderService) orderListVersion(ctx context.Context, userID int64) string {
	if s.rdb == nil {
		return "0"
	}
	ver, err := s.rdb.Get(ctx, orderUserVersionKey(userID)).Result()
	if err == redis.Nil || ver == "" {
		return "0"
	}
	if err != nil {
		return "0"
	}
	return ver
}

func (s *TicketOrderService) invalidateUserOrderQueryCache(ctx context.Context, userID int64) {
	if s.rdb == nil || userID <= 0 {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := s.rdb.Incr(ctx, orderUserVersionKey(userID)).Err(); err != nil {
		log.Printf("[order-query-cache] bump user=%d: %v", userID, err)
		return
	}
	_ = s.rdb.Expire(ctx, orderUserVersionKey(userID), 24*time.Hour).Err()
}

func (s *TicketOrderService) cachedOrderCount(ctx context.Context, userID int64, version string) (int64, bool) {
	if s.rdb == nil {
		return 0, false
	}
	raw, err := s.rdb.Get(ctx, orderCountCacheKey(userID, version)).Result()
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func (s *TicketOrderService) storeOrderCount(ctx context.Context, userID int64, version string, total int64) {
	if s.rdb == nil {
		return
	}
	if err := s.rdb.Set(ctx, orderCountCacheKey(userID, version), strconv.FormatInt(total, 10), jitterTTL(orderCountTTL)).Err(); err != nil {
		log.Printf("[order-query-cache] set count user=%d: %v", userID, err)
	}
}
