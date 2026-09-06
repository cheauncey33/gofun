package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"time"

	"gofun/models"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

const (
	funnelMaxEventIDs = 20
	funnelMinLeakDrop = 15.0
	funnelSnapshotTTL = 20 * time.Second
	funnelCachePrefix = "fuchang:funnel:"
)

type TrackFunnelInput struct {
	Stage     string       `json:"stage"`
	EventIDs  []FlexibleID `json:"event_ids"`
	VisitorID string       `json:"visitor_id"`
}

type FunnelStep struct {
	Key      string  `json:"key"`
	Label    string  `json:"label"`
	Count    int64   `json:"count"`
	Unit     string  `json:"unit"`
	FromPrev float64 `json:"from_prev"`
	FromTop  float64 `json:"from_top"`
	Drop     float64 `json:"drop"`
}

type FunnelLeak struct {
	StepKey string  `json:"step_key"`
	Label   string  `json:"label"`
	Drop    float64 `json:"drop"`
}

type FunnelChannel struct {
	Key        string  `json:"key"`
	Label      string  `json:"label"`
	Submitted  int64   `json:"submitted"`
	Paid       int64   `json:"paid"`
	Refunded   int64   `json:"refunded"`
	PayRate    float64 `json:"pay_rate"`
	RefundRate float64 `json:"refund_rate"`
}

type OrganizerFunnel struct {
	Days             int             `json:"days"`
	From             time.Time       `json:"from"`
	EventID          int64           `json:"event_id,string,omitempty"`
	VisitTracked     bool            `json:"visit_tracked"`
	Steps            []FunnelStep    `json:"steps"`
	Leak             *FunnelLeak     `json:"leak,omitempty"`
	Insight          string          `json:"insight"`
	Channels         []FunnelChannel `json:"channels"`
	PendingOpen      int64           `json:"pending_open"`
	Refunded         int64           `json:"refunded"`
	RefundRate       float64         `json:"refund_rate"`
	UsedTickets      int64           `json:"used_tickets"`
	ValidSoldTickets int64           `json:"valid_sold_tickets"`
	CheckinRate      float64         `json:"checkin_rate"`
}

type funnelEventRef struct {
	ID          int64 `gorm:"column:id"`
	OrganizerID int64 `gorm:"column:organizer_id"`
}

type funnelOrderAgg struct {
	Source    string
	Submitted int64
	Paid      int64
	Refunded  int64
}

func parseFunnelStage(raw string) (string, error) {
	switch strings.TrimSpace(raw) {
	case models.FunnelStageBrowse, models.FunnelStageDetail, models.FunnelStageCheckout:
		return strings.TrimSpace(raw), nil
	default:
		return "", fmt.Errorf("%w: 漏斗阶段不支持", ErrInvalidTicketCatalog)
	}
}

func normalizeFunnelDays(days int) int {
	if days <= 0 {
		return 7
	}
	if days > 90 {
		return 90
	}
	return days
}

func funnelPct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return math.Round(float64(part)/float64(whole)*1000) / 10
}

func funnelVisitorKey(visitorID, ip, ua string, userID int64) string {
	visitorID = strings.TrimSpace(visitorID)
	if len(visitorID) > 128 {
		visitorID = visitorID[:128]
	}
	if len(ua) > 200 {
		ua = ua[:200]
	}
	identity := "browser|" + visitorID
	if visitorID == "" && userID > 0 {
		identity = fmt.Sprintf("user|%d", userID)
	} else if visitorID == "" {
		identity = fmt.Sprintf("fallback|%s|%s", strings.TrimSpace(ip), ua)
	}
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:])
}

func attachFunnelRates(steps []FunnelStep) {
	var prev int64
	var top int64
	for i := range steps {
		if i == 0 {
			top = steps[i].Count
			steps[i].FromPrev = 100
			steps[i].Drop = 0
		} else if prev <= 0 {
			steps[i].FromPrev = 0
			steps[i].Drop = 0
		} else {
			steps[i].FromPrev = funnelPct(steps[i].Count, prev)
			drop := 100 - steps[i].FromPrev
			if drop < 0 {
				drop = 0
			}
			steps[i].Drop = drop
		}
		if top > 0 {
			fromTop := funnelPct(steps[i].Count, top)
			if fromTop > 100 {
				fromTop = 100
			}
			steps[i].FromTop = fromTop
		}
		prev = steps[i].Count
	}
}

func biggestFunnelLeak(steps []FunnelStep) *FunnelLeak {
	var best FunnelLeak
	found := false
	for i := 1; i < len(steps); i++ {
		if steps[i].Drop < funnelMinLeakDrop {
			continue
		}
		if !found || steps[i].Drop > best.Drop {
			best = FunnelLeak{
				StepKey: steps[i].Key,
				Label:   steps[i].Label,
				Drop:    steps[i].Drop,
			}
			found = true
		}
	}
	if !found {
		return nil
	}
	return &best
}

func funnelInsight(steps []FunnelStep, leak *FunnelLeak, refundRate float64) string {
	byKey := map[string]FunnelStep{}
	for _, step := range steps {
		byKey[step.Key] = step
	}
	browse := byKey["browse"]
	if browse.Count == 0 {
		return "所选时间窗内还没有形成可计算的同访客访问链路。"
	}
	if leak != nil {
		return fmt.Sprintf("同访客链路中，进入「%s」的比例相对上一步最低。", leak.Label)
	}
	if refundRate >= 15 {
		return "订单口径的退款比例较高；原因需要结合退款记录进一步判断。"
	}
	paid := byKey["paid"]
	if browse.Count > 0 && paid.FromTop >= 20 {
		return "浏览到支付没有单步特别掉队。"
	}
	return "所选时间窗内没有单一步骤出现明显掉队。"
}

func upsertFunnelVisitorStage(
	tx *gorm.DB,
	eventID, organizerID int64,
	visitorKey, stage string,
	at time.Time,
) error {
	if tx == nil || eventID <= 0 || organizerID <= 0 || strings.TrimSpace(visitorKey) == "" {
		return nil
	}
	if _, err := parseFunnelStage(stage); err != nil &&
		stage != models.FunnelStageSubmitted && stage != models.FunnelStagePaid {
		return err
	}
	if at.IsZero() {
		at = time.Now()
	}
	day := at.In(time.Local).Format("2006-01-02")
	return tx.Exec(
		`INSERT INTO funnel_visitor_daily
		 (event_id, organizer_id, day, visitor_key, stage, hits, first_at, last_at)
		 VALUES (?, ?, ?, ?, ?, 1, ?, ?)
		 ON DUPLICATE KEY UPDATE hits = hits + 1, last_at = VALUES(last_at)`,
		eventID, organizerID, day, visitorKey, stage, at, at,
	).Error
}

func applyFunnelEventFilter(db *gorm.DB, eventID int64) *gorm.DB {
	if eventID > 0 {
		return db.Where("event_id = ?", eventID)
	}
	return db
}

func (s *TicketCatalogService) TrackFunnelVisits(
	ctx context.Context,
	userID int64,
	ip, ua string,
	input TrackFunnelInput,
) error {
	stage, err := parseFunnelStage(input.Stage)
	if err != nil {
		return err
	}
	seen := make(map[int64]struct{}, len(input.EventIDs))
	ids := make([]int64, 0, len(input.EventIDs))
	for _, raw := range input.EventIDs {
		id := int64(raw)
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
		if len(ids) >= funnelMaxEventIDs {
			break
		}
	}
	if len(ids) == 0 {
		return nil
	}

	var events []funnelEventRef
	if err := s.db.WithContext(ctx).Model(&models.Event{}).
		Select("id, organizer_id").
		Where("id IN ? AND status = ?", ids, models.EventStatusPublished).
		Scan(&events).Error; err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}

	day := time.Now().Format("2006-01-02")
	uniques := make([]int64, len(events))
	for i := range uniques {
		uniques[i] = 1
	}
	if s.rdb != nil {
		visitor := funnelVisitorKey(input.VisitorID, ip, ua, userID)
		pipe := s.rdb.Pipeline()
		cmds := make([]*redis.BoolCmd, len(events))
		for i, event := range events {
			key := fmt.Sprintf("funnel:u:%s:%s:%d:%s", day, stage, event.ID, visitor)
			cmds[i] = pipe.SetNX(ctx, key, 1, 36*time.Hour)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			for i := range uniques {
				uniques[i] = 0
			}
		} else {
			for i, cmd := range cmds {
				ok, cmdErr := cmd.Result()
				if cmdErr != nil || !ok {
					uniques[i] = 0
				}
			}
		}
	}

	for i, event := range events {
		if err := upsertFunnelVisitorStage(
			s.db.WithContext(ctx), event.ID, event.OrganizerID,
			funnelVisitorKey(input.VisitorID, ip, ua, userID), stage, time.Now(),
		); err != nil {
			return err
		}
		if err := s.db.WithContext(ctx).Exec(
			`INSERT INTO funnel_daily (event_id, organizer_id, day, stage, hits, uniques)
			 VALUES (?, ?, ?, ?, 1, ?)
			 ON DUPLICATE KEY UPDATE hits = hits + 1, uniques = uniques + VALUES(uniques)`,
			event.ID, event.OrganizerID, day, stage, uniques[i],
		).Error; err != nil {
			return err
		}
	}
	return nil
}

func funnelVersionKey(organizerID int64) string {
	return fmt.Sprintf("%sver:%d", funnelCachePrefix, organizerID)
}

func funnelSnapshotKey(organizerID, eventID int64, days int, ver string) string {
	return fmt.Sprintf("%sv1:%d:%d:%d:%s", funnelCachePrefix, organizerID, eventID, days, ver)
}

func funnelCacheVersion(ctx context.Context, rdb *redis.Client, organizerID int64) string {
	if rdb == nil {
		return "0"
	}
	ver, err := rdb.Get(ctx, funnelVersionKey(organizerID)).Result()
	if err != nil || ver == "" {
		return "0"
	}
	return ver
}

func bumpFunnelCacheVersion(ctx context.Context, rdb *redis.Client, organizerID int64) {
	if rdb == nil || organizerID <= 0 {
		return
	}
	_ = rdb.Incr(ctx, funnelVersionKey(organizerID)).Err()
}

// BumpFunnelCacheVersion 供种子脚本在覆盖日汇总后立刻失效漏斗缓存。
func BumpFunnelCacheVersion(ctx context.Context, rdb *redis.Client, organizerID int64) {
	bumpFunnelCacheVersion(ctx, rdb, organizerID)
}

func bumpFunnelOrderDaily(
	tx *gorm.DB,
	eventID, organizerID int64,
	source string,
	submitted, paid, refunded int64,
	at time.Time,
) error {
	if tx == nil || eventID <= 0 || organizerID <= 0 || source == "" {
		return nil
	}
	if submitted == 0 && paid == 0 && refunded == 0 {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	day := at.In(time.Local).Format("2006-01-02")
	return tx.Exec(
		`INSERT INTO funnel_order_daily (event_id, organizer_id, day, source, submitted, paid, refunded)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON DUPLICATE KEY UPDATE
		   submitted = submitted + VALUES(submitted),
		   paid = paid + VALUES(paid),
		   refunded = refunded + VALUES(refunded)`,
		eventID, organizerID, day, source, submitted, paid, refunded,
	).Error
}

func (s *TicketCatalogService) GetOrganizerFunnel(
	ctx context.Context,
	userID, organizerID, eventID int64,
	days int,
) (*OrganizerFunnel, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	days = normalizeFunnelDays(days)
	if eventID > 0 {
		var n int64
		if err := s.db.WithContext(ctx).Model(&models.Event{}).
			Where("id = ? AND organizer_id = ?", eventID, organizerID).
			Count(&n).Error; err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, ErrTicketResourceNotFound
		}
	}

	ver := funnelCacheVersion(ctx, s.rdb, organizerID)
	key := funnelSnapshotKey(organizerID, eventID, days, ver)
	var out OrganizerFunnel
	err := cacheAsideJSON(ctx, s.rdb, &s.cacheSF, key, funnelSnapshotTTL, &out, func(ctx context.Context) (OrganizerFunnel, error) {
		loaded, loadErr := s.loadOrganizerFunnel(ctx, organizerID, eventID, days)
		if loadErr != nil {
			return OrganizerFunnel{}, loadErr
		}
		return *loaded, nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func (s *TicketCatalogService) loadOrganizerFunnel(
	ctx context.Context,
	organizerID, eventID int64,
	days int,
) (*OrganizerFunnel, error) {
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -(days - 1))
	fromDay := from.Format("2006-01-02")

	// 浏览路径只统计访客阶段；提交/支付走真实订单，避免演示种子和经营卡互相打脸。
	stageRows := applyFunnelEventFilter(
		s.db.WithContext(ctx).Model(&models.FunnelVisitorDaily{}).
			Select(`event_id, visitor_key,
				MAX(CASE WHEN stage = 'browse' THEN 1 ELSE 0 END) AS browse,
				MAX(CASE WHEN stage = 'detail' THEN 1 ELSE 0 END) AS detail,
				MAX(CASE WHEN stage = 'checkout' THEN 1 ELSE 0 END) AS checkout`).
			Where("organizer_id = ? AND day >= ? AND stage IN ?",
				organizerID, fromDay,
				[]string{models.FunnelStageBrowse, models.FunnelStageDetail, models.FunnelStageCheckout}),
		eventID,
	).Group("event_id, visitor_key")
	var cohort struct {
		Browse   int64
		Detail   int64
		Checkout int64
	}
	if err := s.db.WithContext(ctx).Table("(?) AS cohort", stageRows).
		Select(`
			COALESCE(SUM(CASE WHEN browse = 1 THEN 1 ELSE 0 END), 0) AS browse,
			COALESCE(SUM(CASE WHEN browse = 1 AND detail = 1 THEN 1 ELSE 0 END), 0) AS detail,
			COALESCE(SUM(CASE WHEN browse = 1 AND detail = 1 AND checkout = 1 THEN 1 ELSE 0 END), 0) AS checkout
		`).Scan(&cohort).Error; err != nil {
		return nil, err
	}

	var orderRows []funnelOrderAgg
	if err := applyFunnelEventFilter(
		s.db.WithContext(ctx).Model(&models.TicketOrder{}).
			Select(`
				order_source AS source,
				COUNT(*) AS submitted,
				COALESCE(SUM(CASE WHEN payment_status IN ('paid', 'refunding', 'refunded') THEN 1 ELSE 0 END), 0) AS paid,
				COALESCE(SUM(CASE WHEN payment_status = 'refunded' THEN 1 ELSE 0 END), 0) AS refunded
			`).
			Where("organizer_id = ? AND create_time >= ? AND delete_time IS NULL AND order_source <> ?",
				organizerID, from, models.TicketOrderSourceWaitlist),
		eventID,
	).Group("order_source").Scan(&orderRows).Error; err != nil {
		return nil, err
	}
	var waitlistRow funnelOrderAgg
	waitlistRow.Source = string(models.TicketOrderSourceWaitlist)
	if err := applyFunnelEventFilter(
		s.db.WithContext(ctx).Model(&models.WaitlistEntry{}).
			Select(`
				COUNT(*) AS submitted,
				COALESCE(SUM(CASE WHEN paid_at IS NOT NULL THEN 1 ELSE 0 END), 0) AS paid
			`).
			Where("organizer_id = ? AND create_time >= ? AND delete_time IS NULL", organizerID, from),
		eventID,
	).Scan(&waitlistRow).Error; err != nil {
		return nil, err
	}
	var waitlistRefunded int64
	waitlistRefundQuery := s.db.WithContext(ctx).Model(&models.PaymentTransaction{}).
		Joins("JOIN waitlist_entry ON waitlist_entry.id = payment_transaction.waitlist_id AND waitlist_entry.delete_time IS NULL").
		Where("waitlist_entry.organizer_id = ? AND waitlist_entry.create_time >= ? AND payment_transaction.status = ? AND payment_transaction.delete_time IS NULL",
			organizerID, from, models.PaymentTransactionRefunded)
	if eventID > 0 {
		waitlistRefundQuery = waitlistRefundQuery.Where("waitlist_entry.event_id = ?", eventID)
	}
	if err := waitlistRefundQuery.Distinct("payment_transaction.waitlist_id").Count(&waitlistRefunded).Error; err != nil {
		return nil, err
	}
	waitlistRow.Refunded = waitlistRefunded
	orderRows = append(orderRows, waitlistRow)

	var pending int64
	if err := applyFunnelEventFilter(
		s.db.WithContext(ctx).Model(&models.TicketOrder{}).
			Where("organizer_id = ? AND status = ? AND create_time >= ? AND delete_time IS NULL",
				organizerID, models.TicketOrderStatusPendingPayment, from),
		eventID,
	).Count(&pending).Error; err != nil {
		return nil, err
	}
	var waitlistPending int64
	if err := applyFunnelEventFilter(
		s.db.WithContext(ctx).Model(&models.WaitlistEntry{}).
			Where("organizer_id = ? AND status = ? AND create_time >= ? AND delete_time IS NULL",
				organizerID, models.WaitlistStatusPendingPayment, from),
		eventID,
	).Count(&waitlistPending).Error; err != nil {
		return nil, err
	}
	pending += waitlistPending

	usedTickets, validSoldTickets, err := s.loadCheckinProgress(ctx, organizerID, eventID, 0)
	if err != nil {
		return nil, err
	}

	bySource := map[string]funnelOrderAgg{}
	var submitted, paid, refunded int64
	for _, row := range orderRows {
		bySource[row.Source] = row
		submitted += row.Submitted
		paid += row.Paid
		refunded += row.Refunded
	}

	steps := []FunnelStep{
		{Key: "browse", Label: "列表出现", Count: cohort.Browse, Unit: "访客"},
		{Key: "detail", Label: "打开详情", Count: cohort.Detail, Unit: "访客"},
		{Key: "checkout", Label: "进入下单页", Count: cohort.Checkout, Unit: "访客"},
		{Key: "submitted", Label: "提交订单", Count: submitted, Unit: "单"},
		{Key: "paid", Label: "支付成功", Count: paid, Unit: "单"},
	}
	attachFunnelRates(steps)
	leak := biggestFunnelLeak(steps)
	refundRate := funnelPct(refunded, paid)
	waitlist := bySource[string(models.TicketOrderSourceWaitlist)]
	channels := []FunnelChannel{
		buildFunnelChannel("normal", "普通购票", bySource[string(models.TicketOrderSourceNormal)]),
		buildFunnelChannel("rush_sale", "限时开售", bySource[string(models.TicketOrderSourceRushSale)]),
		buildFunnelChannel("waitlist", "候补", waitlist),
	}

	return &OrganizerFunnel{
		Days:             days,
		From:             from,
		EventID:          eventID,
		VisitTracked:     cohort.Browse > 0,
		Steps:            steps,
		Leak:             leak,
		Insight:          funnelInsight(steps, leak, refundRate),
		Channels:         channels,
		PendingOpen:      pending,
		Refunded:         refunded,
		RefundRate:       refundRate,
		UsedTickets:      usedTickets,
		ValidSoldTickets: validSoldTickets,
		CheckinRate:      checkinRate(usedTickets, validSoldTickets),
	}, nil
}

func buildFunnelChannel(key, label string, row funnelOrderAgg) FunnelChannel {
	return FunnelChannel{
		Key:        key,
		Label:      label,
		Submitted:  row.Submitted,
		Paid:       row.Paid,
		Refunded:   row.Refunded,
		PayRate:    funnelPct(row.Paid, row.Submitted),
		RefundRate: funnelPct(row.Refunded, row.Paid),
	}
}
