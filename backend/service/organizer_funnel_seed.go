package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"strings"
	"time"

	"gofun/models"

	"gorm.io/gorm"
)

const (
	funnelHistoryDays = 30
	funnelBaseBrowse  = 240.0
)

// FunnelHistoryEvent 是日汇总种子的一条活动权重。
type FunnelHistoryEvent struct {
	ID          int64
	OrganizerID int64
	Title       string
	Weight      float64
}

func funnelHistoryWeight(title string) float64 {
	if strings.Contains(title, "夏夜回声") {
		return 1
	}
	return 0.42
}

func funnelHistoryDayStart(now time.Time, daysAgo int) time.Time {
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return day.AddDate(0, 0, -daysAgo)
}

func generateOrganizerFunnelHistory(
	events []FunnelHistoryEvent,
	now time.Time,
	days int,
) ([]models.FunnelDaily, []models.FunnelOrderDaily) {
	if days <= 0 {
		days = funnelHistoryDays
	}
	visits := make([]models.FunnelDaily, 0, len(events)*days*3)
	orders := make([]models.FunnelOrderDaily, 0, len(events)*days*3)
	for _, event := range events {
		weight := event.Weight
		if weight <= 0 {
			weight = funnelHistoryWeight(event.Title)
		}
		for ago := 0; ago < days; ago++ {
			day := funnelHistoryDayStart(now, ago)
			weekend := day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
			mul := weight
			if weekend {
				mul *= 1.32
			}
			if ago < 7 {
				mul *= 1.12
			}
			browse := int64(funnelBaseBrowse*mul + 0.5)
			if browse < 8 {
				browse = 8
			}
			detail := browse * 72 / 100
			checkout := detail * 38 / 100
			submitted := checkout * 58 / 100
			if submitted < 1 {
				submitted = 1
			}
			hitsMul := int64(118 + (ago % 7))
			visits = append(visits,
				models.FunnelDaily{
					EventID: event.ID, OrganizerID: event.OrganizerID, Day: day,
					Stage: models.FunnelStageBrowse, Hits: browse * hitsMul / 100, Uniques: browse,
				},
				models.FunnelDaily{
					EventID: event.ID, OrganizerID: event.OrganizerID, Day: day,
					Stage: models.FunnelStageDetail, Hits: detail * hitsMul / 100, Uniques: detail,
				},
				models.FunnelDaily{
					EventID: event.ID, OrganizerID: event.OrganizerID, Day: day,
					Stage: models.FunnelStageCheckout, Hits: checkout * hitsMul / 100, Uniques: checkout,
				},
			)
			normalSub := submitted * 68 / 100
			rushSub := submitted * 24 / 100
			waitSub := submitted - normalSub - rushSub
			if waitSub < 0 {
				waitSub = 0
			}
			orders = append(orders,
				funnelOrderHistoryRow(event, day, models.TicketOrderSourceNormal, normalSub, 74, 6),
				funnelOrderHistoryRow(event, day, models.TicketOrderSourceRushSale, rushSub, 68, 5),
				funnelOrderHistoryRow(event, day, models.TicketOrderSourceWaitlist, waitSub, 80, 8),
			)
		}
	}
	return visits, orders
}

func funnelOrderHistoryRow(
	event FunnelHistoryEvent,
	day time.Time,
	source models.TicketOrderSource,
	submitted int64,
	payRate, refundRate int64,
) models.FunnelOrderDaily {
	paid := submitted * payRate / 100
	refunded := paid * refundRate / 100
	return models.FunnelOrderDaily{
		EventID:     event.ID,
		OrganizerID: event.OrganizerID,
		Day:         day,
		Source:      string(source),
		Submitted:   submitted,
		Paid:        paid,
		Refunded:    refunded,
	}
}

func sumFunnelVisitUniques(rows []models.FunnelDaily, now time.Time, days int, stage string) int64 {
	from := funnelHistoryDayStart(now, days-1)
	var total int64
	for _, row := range rows {
		if row.Stage != stage {
			continue
		}
		day := time.Date(row.Day.Year(), row.Day.Month(), row.Day.Day(), 0, 0, 0, 0, now.Location())
		if day.Before(from) {
			continue
		}
		total += row.Uniques
	}
	return total
}

func sumFunnelOrderSubmitted(rows []models.FunnelOrderDaily, now time.Time, days int) int64 {
	from := funnelHistoryDayStart(now, days-1)
	var total int64
	for _, row := range rows {
		day := time.Date(row.Day.Year(), row.Day.Month(), row.Day.Day(), 0, 0, 0, 0, now.Location())
		if day.Before(from) {
			continue
		}
		total += row.Submitted
	}
	return total
}

func generateOrganizerFunnelVisitorHistory(
	visits []models.FunnelDaily,
	orders []models.FunnelOrderDaily,
) []models.FunnelVisitorDaily {
	type dayCounts struct {
		eventID, organizerID     int64
		day                      time.Time
		browse, detail, checkout int64
		submitted, paid          int64
	}
	byDay := map[string]*dayCounts{}
	keyFor := func(eventID int64, day time.Time) string {
		return fmt.Sprintf("%d:%s", eventID, day.Format("2006-01-02"))
	}
	for _, row := range visits {
		key := keyFor(row.EventID, row.Day)
		counts := byDay[key]
		if counts == nil {
			counts = &dayCounts{eventID: row.EventID, organizerID: row.OrganizerID, day: row.Day}
			byDay[key] = counts
		}
		switch row.Stage {
		case models.FunnelStageBrowse:
			counts.browse = row.Uniques
		case models.FunnelStageDetail:
			counts.detail = row.Uniques
		case models.FunnelStageCheckout:
			counts.checkout = row.Uniques
		}
	}
	for _, row := range orders {
		key := keyFor(row.EventID, row.Day)
		counts := byDay[key]
		if counts == nil {
			counts = &dayCounts{eventID: row.EventID, organizerID: row.OrganizerID, day: row.Day}
			byDay[key] = counts
		}
		counts.submitted += row.Submitted
		counts.paid += row.Paid
	}
	// 只灌浏览路径；提交/支付必须来自真实订单，不能用汇总种子冒充。
	stages := []struct {
		name string
		get  func(*dayCounts) int64
	}{
		{models.FunnelStageBrowse, func(c *dayCounts) int64 { return c.browse }},
		{models.FunnelStageDetail, func(c *dayCounts) int64 { return c.detail }},
		{models.FunnelStageCheckout, func(c *dayCounts) int64 { return c.checkout }},
	}
	rows := make([]models.FunnelVisitorDaily, 0)
	for _, counts := range byDay {
		at := counts.day.Add(12 * time.Hour)
		for index := int64(0); index < counts.browse; index++ {
			raw := fmt.Sprintf("demo|%d|%s|%d", counts.eventID, counts.day.Format("2006-01-02"), index)
			sum := sha256.Sum256([]byte(raw))
			visitorKey := fmt.Sprintf("%x", sum[:])
			for _, stage := range stages {
				if index >= stage.get(counts) {
					continue
				}
				rows = append(rows, models.FunnelVisitorDaily{
					EventID: counts.eventID, OrganizerID: counts.organizerID,
					Day: counts.day, VisitorKey: visitorKey, Stage: stage.name,
					Hits: 1, FirstAt: at, LastAt: at,
				})
			}
		}
	}
	return rows
}

// ReplaceOrganizerFunnelHistory 用日汇总覆盖该主办方近 30 天漏斗。
// 只写 funnel_daily / funnel_order_daily，不插订单明细。
func ReplaceOrganizerFunnelHistory(
	ctx context.Context,
	db *gorm.DB,
	organizerID int64,
	events []FunnelHistoryEvent,
	now time.Time,
) error {
	if db == nil || organizerID <= 0 || len(events) == 0 {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	from := funnelHistoryDayStart(now, funnelHistoryDays-1)
	fromDay := from.Format("2006-01-02")
	visits, orders := generateOrganizerFunnelHistory(events, now, funnelHistoryDays)
	visitorStages := generateOrganizerFunnelVisitorHistory(visits, orders)
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(
			`DELETE FROM funnel_daily WHERE organizer_id = ? AND day >= ?`,
			organizerID, fromDay,
		).Error; err != nil {
			return err
		}
		if err := tx.Exec(
			`DELETE FROM funnel_order_daily WHERE organizer_id = ? AND day >= ?`,
			organizerID, fromDay,
		).Error; err != nil {
			return err
		}
		if err := tx.Exec(
			`DELETE FROM funnel_visitor_daily WHERE organizer_id = ? AND day >= ?`,
			organizerID, fromDay,
		).Error; err != nil {
			return err
		}
		if len(visits) > 0 {
			if err := tx.Create(&visits).Error; err != nil {
				return err
			}
		}
		if len(orders) > 0 {
			if err := tx.Create(&orders).Error; err != nil {
				return err
			}
		}
		if len(visitorStages) > 0 {
			if err := tx.CreateInBatches(&visitorStages, 500).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
