package service

import (
	"testing"
	"time"
)

func TestIsTimeoutCancelReason(t *testing.T) {
	t.Parallel()
	if !isTimeoutCancelReason("支付超时自动取消") {
		t.Fatal("production timeout reason should match")
	}
	if !isTimeoutCancelReason(" 支付超时自动取消 ") {
		t.Fatal("trimmed timeout reason should match")
	}
	for _, reason := range []string{"", "用户取消", "候补支付超时", "并发超时", "开场前未配到票，候补已截止"} {
		if isTimeoutCancelReason(reason) {
			t.Fatalf("reason %q should not count as order payment timeout", reason)
		}
	}
}

func TestRollingCalendarPeriod(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 9, 6, 15, 4, 5, 0, loc)
	from, previousFrom, to := rollingCalendarPeriod(now, 7)
	if from.Day() != 31 || from.Month() != time.August || from.Hour() != 0 {
		t.Fatalf("from=%s", from)
	}
	if previousFrom.Day() != 24 || previousFrom.Month() != time.August {
		t.Fatalf("previousFrom=%s", previousFrom)
	}
	if !to.After(now) || to.Sub(now) != time.Second {
		t.Fatalf("to=%s", to)
	}
}

func TestCheckinRate(t *testing.T) {
	t.Parallel()
	if got := checkinRate(0, 0); got != 0 {
		t.Fatalf("empty=%v", got)
	}
	if got := checkinRate(3, 10); got != 30 {
		t.Fatalf("3/10=%v", got)
	}
	if got := checkinRate(10, 10); got != 100 {
		t.Fatalf("all used=%v", got)
	}
}

func TestOverviewPaymentSuccessRate(t *testing.T) {
	t.Parallel()
	if got := overviewPaymentSuccessRate(0, 0); got != 0 {
		t.Fatalf("empty window rate=%v", got)
	}
	if got := overviewPaymentSuccessRate(8, 2); got != 80 {
		t.Fatalf("8 paid / 10 resolved = 80, got %v", got)
	}
	if got := overviewPaymentSuccessRate(1, 2); got != 33.3 {
		t.Fatalf("1/3 should round to 33.3, got %v", got)
	}
	if got := overviewPaymentSuccessRate(5, 0); got != 100 {
		t.Fatalf("all paid should be 100, got %v", got)
	}
}
