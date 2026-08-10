package service

import (
	"encoding/json"
	"gofun/models"
	"strings"
	"testing"

	"github.com/bwmarrin/snowflake"
)

func TestNotifyOutboxPublisherCoalesces(t *testing.T) {
	s := &TicketOrderService{outboxNotify: make(chan struct{}, 1)}
	s.notifyOutboxPublisher()
	s.notifyOutboxPublisher()
	s.notifyOutboxPublisher()
	if len(s.outboxNotify) != 1 {
		t.Fatalf("expected coalesced notify depth 1, got %d", len(s.outboxNotify))
	}
	select {
	case <-s.outboxNotify:
	default:
		t.Fatal("expected one pending notify")
	}
	if len(s.outboxNotify) != 0 {
		t.Fatalf("notify channel should be empty after receive, got %d", len(s.outboxNotify))
	}
}

func TestNotifyOutboxPublisherNilSafe(t *testing.T) {
	s := &TicketOrderService{}
	s.notifyOutboxPublisher() // must not panic
}

func TestTicketOrderReceiptKeepsQueuedState(t *testing.T) {
	order := &models.TicketOrder{
		Base:    models.Base{ID: 123},
		OrderNo: "FC123",
		Status:  models.TicketOrderStatusQueued,
	}
	receipt := ticketOrderReceipt(order)
	if receipt.OrderID != 123 || receipt.OrderNo != "FC123" ||
		receipt.Status != models.TicketOrderStatusQueued {
		t.Fatalf("unexpected receipt: %#v", receipt)
	}
}

func TestValidatePurchaseInfoRequiresContactAndTerms(t *testing.T) {
	input := PurchaseInfoInput{ContactName: "张三", ContactPhone: "13800138000"}
	if err := validatePurchaseInfo(input, 1, false); err == nil {
		t.Fatal("expected purchase notice acceptance to be required")
	}
	input.TermsAccepted = true
	if err := validatePurchaseInfo(input, 1, false); err != nil {
		t.Fatalf("valid non-real-name purchase rejected: %v", err)
	}
}

func TestRushSalePurchaseInfoCannotSkipRealName(t *testing.T) {
	// 抢票与普通单共用校验；实名活动缺少观演人必须失败。
	input := PurchaseInfoInput{
		ContactName: "张三", ContactPhone: "13800138000", TermsAccepted: true,
	}
	if err := validatePurchaseInfo(input, 1, true); err == nil {
		t.Fatal("rush/real-name purchase without attendees must fail")
	}
}

func TestValidatePurchaseInfoMatchesAttendeesToQuantity(t *testing.T) {
	input := PurchaseInfoInput{
		ContactName: "张三", ContactPhone: "13800138000", TermsAccepted: true,
		Attendees: []TicketAttendeeInput{{
			Name: "李四", IDType: "id_card", IDNumber: "11010519491231002X",
		}},
	}
	if err := validatePurchaseInfo(input, 2, true); err == nil {
		t.Fatal("expected attendee count mismatch to fail")
	}
	input.Attendees = append(input.Attendees, TicketAttendeeInput{
		Name: "王五", IDType: "id_card", IDNumber: "110105194912310021",
	})
	if err := validatePurchaseInfo(input, 2, true); err == nil {
		t.Fatal("expected invalid ID checksum to fail")
	}
}

func TestAttendeeSnapshotDoesNotExposeRawIdentityNumber(t *testing.T) {
	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatal(err)
	}
	service := &TicketOrderService{node: node, identityHashKey: []byte("test-secret")}
	attendees := service.buildAttendeeSnapshots(99, []TicketAttendeeInput{{
		Name: "李四", IDType: "id_card", IDNumber: "11010519491231002X",
	}}, true)
	if got := attendees[0].IDNumberMasked; got != "110***********002X" {
		t.Fatalf("masked ID = %q", got)
	}
	body, err := json.Marshal(attendees[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "11010519491231002X") || strings.Contains(string(body), "id_number_hash") {
		t.Fatalf("sensitive identity data leaked in JSON: %s", body)
	}
}
