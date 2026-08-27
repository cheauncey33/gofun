package service

import (
	"testing"
	"time"

	"gofun/models"
)

func TestNormalizeFunnelDays(t *testing.T) {
	t.Parallel()
	if got := normalizeFunnelDays(0); got != 7 {
		t.Fatalf("default days=%d", got)
	}
	if got := normalizeFunnelDays(30); got != 30 {
		t.Fatalf("keep 30, got %d", got)
	}
	if got := normalizeFunnelDays(200); got != 90 {
		t.Fatalf("cap at 90, got %d", got)
	}
}

func TestParseFunnelStage(t *testing.T) {
	t.Parallel()
	if _, err := parseFunnelStage("browse"); err != nil {
		t.Fatal(err)
	}
	if _, err := parseFunnelStage("click"); err == nil {
		t.Fatal("unknown stage should fail")
	}
}

func TestFunnelVisitorKeyStable(t *testing.T) {
	t.Parallel()
	a := funnelVisitorKey("1.1.1.1", "ua", 0)
	b := funnelVisitorKey("1.1.1.1", "ua", 0)
	c := funnelVisitorKey("1.1.1.1", "ua", 9)
	if a != b || a == "" || a == c {
		t.Fatalf("visitor key a=%s b=%s c=%s", a, b, c)
	}
}

func TestAttachFunnelRatesAndLeak(t *testing.T) {
	t.Parallel()
	steps := []FunnelStep{
		{Key: "browse", Label: "列表曝光", Count: 100, Unit: "人"},
		{Key: "detail", Label: "打开详情", Count: 80, Unit: "人"},
		{Key: "checkout", Label: "进入下单页", Count: 20, Unit: "人"},
		{Key: "submitted", Label: "提交订单", Count: 16, Unit: "单"},
		{Key: "paid", Label: "支付成功", Count: 8, Unit: "单"},
	}
	attachFunnelRates(steps)
	if steps[0].FromPrev != 100 || steps[1].FromPrev != 80 || steps[2].FromPrev != 25 {
		t.Fatalf("from_prev=%v", []float64{steps[0].FromPrev, steps[1].FromPrev, steps[2].FromPrev})
	}
	if steps[2].Drop != 75 {
		t.Fatalf("checkout drop=%v want 75", steps[2].Drop)
	}
	leak := biggestFunnelLeak(steps)
	if leak == nil || leak.StepKey != "checkout" {
		t.Fatalf("leak=%+v", leak)
	}
	insight := funnelInsight(steps, leak, 0)
	if insight != "最大流失在详情到下单：票价、售罄或购票门槛可能劝退。" {
		t.Fatalf("insight=%q", insight)
	}
}

func TestBiggestFunnelLeakIgnoresSmallDrop(t *testing.T) {
	t.Parallel()
	steps := []FunnelStep{
		{Key: "browse", Count: 100},
		{Key: "detail", Count: 96},
		{Key: "checkout", Count: 94},
		{Key: "submitted", Count: 90},
		{Key: "paid", Count: 88},
	}
	attachFunnelRates(steps)
	if leak := biggestFunnelLeak(steps); leak != nil {
		t.Fatalf("small drops should not leak, got %+v", leak)
	}
}

func TestFunnelInsightVisitLag(t *testing.T) {
	t.Parallel()
	steps := []FunnelStep{
		{Key: "browse", Count: 0},
		{Key: "detail", Count: 0},
		{Key: "checkout", Count: 0},
		{Key: "submitted", Count: 12},
		{Key: "paid", Count: 8},
	}
	attachFunnelRates(steps)
	got := funnelInsight(steps, nil, 0)
	if got != "下单页浏览从本次上线后才记人数；时间窗内已有提交，浏览转化会偏低。" {
		t.Fatalf("insight=%q", got)
	}
}

func TestGenerateOrganizerFunnelHistoryWindows(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 27, 15, 0, 0, 0, time.Local)
	events := []FunnelHistoryEvent{{
		ID: 1, OrganizerID: 9, Title: "夏夜回声 Livehouse 专场", Weight: 1,
	}}
	visits, orders := generateOrganizerFunnelHistory(events, now, 30)
	if len(visits) != 90 {
		t.Fatalf("visit rows=%d want 90", len(visits))
	}
	if len(orders) != 90 {
		t.Fatalf("order rows=%d want 90", len(orders))
	}
	browse7 := sumFunnelVisitUniques(visits, now, 7, models.FunnelStageBrowse)
	browse30 := sumFunnelVisitUniques(visits, now, 30, models.FunnelStageBrowse)
	if browse7 < 1000 || browse30 < browse7*2 {
		t.Fatalf("browse 7d=%d 30d=%d, 30d should be much larger", browse7, browse30)
	}
	sub7 := sumFunnelOrderSubmitted(orders, now, 7)
	sub30 := sumFunnelOrderSubmitted(orders, now, 30)
	if sub7 < 200 || sub30 < sub7*2 {
		t.Fatalf("submitted 7d=%d 30d=%d, 30d should be much larger", sub7, sub30)
	}
	byDay := map[string]map[string]int64{}
	for _, row := range visits {
		key := row.Day.Format("2006-01-02")
		if byDay[key] == nil {
			byDay[key] = map[string]int64{}
		}
		byDay[key][row.Stage] = row.Uniques
	}
	for day, stages := range byDay {
		if stages[models.FunnelStageBrowse] < stages[models.FunnelStageDetail] ||
			stages[models.FunnelStageDetail] < stages[models.FunnelStageCheckout] {
			t.Fatalf("%s visit funnel is not decreasing: %+v", day, stages)
		}
	}
}
