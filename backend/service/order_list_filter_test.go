package service

import (
	"strings"
	"testing"

	"gofun/models"
)

func TestParseOrderListFilter(t *testing.T) {
	got := ParseOrderListFilter(" pending_payment ", "  宽窄  ", "")
	if got.StatusGroup != "pending" || got.Keyword != "宽窄" {
		t.Fatalf("unexpected filter: %#v", got)
	}
	if len(got.Statuses()) != 2 ||
		got.Statuses()[0] != models.TicketOrderStatusQueued ||
		got.Statuses()[1] != models.TicketOrderStatusPendingPayment {
		t.Fatalf("pending should include queued + pending_payment: %#v", got.Statuses())
	}

	paid := ParseOrderListFilter("paid", "", " 外滩 ")
	if paid.StatusGroup != "paid" || paid.Keyword != "外滩" || len(paid.Statuses()) != 1 {
		t.Fatalf("paid+q: %#v", paid)
	}

	queued := ParseOrderListFilter("queued", "", "")
	if queued.StatusGroup != "pending" {
		t.Fatalf("queued should map to pending: %#v", queued)
	}

	refunded := ParseOrderListFilter("refunding", "", "")
	if refunded.StatusGroup != "refunded" || refunded.Statuses() != nil {
		t.Fatalf("refunding should map to refunded group: %#v", refunded)
	}

	all := ParseOrderListFilter("all", "", "")
	if all.StatusGroup != "" || all.Statuses() != nil {
		t.Fatalf("all should be unfiltered: %#v", all)
	}

	long := strings.Repeat("活动", 40)
	trimmed := ParseOrderListFilter("", long, "")
	if len([]rune(trimmed.Keyword)) != 64 {
		t.Fatalf("keyword should cap at 64 runes, got %d", len([]rune(trimmed.Keyword)))
	}
}

func TestEscapeOrderListLikeStripsWildcards(t *testing.T) {
	if got := escapeOrderListLike("100%_off"); got != "100off" {
		t.Fatalf("escapeOrderListLike = %q", got)
	}
}
