//go:build integration

package service

import (
	"context"
	"fmt"
	"gofun/models"
	"strconv"
	"testing"
	"time"
)

func TestIntegrationTimeoutScanAdvancesPastFailedPage(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	tier := env.seedPurchasableTier(t, 20, 6)
	user := env.newUser(t, fmt.Sprintf("scanner-user-%d", tier.ID))
	var session models.EventSession
	if err := env.db.First(&session, tier.SessionID).Error; err != nil {
		t.Fatal(err)
	}
	var event models.Event
	if err := env.db.First(&event, session.EventID).Error; err != nil {
		t.Fatal(err)
	}
	var validID int64
	for i := 0; i < 101; i++ {
		id := env.svc.node.Generate().Int64()
		order := models.TicketOrder{
			Base: models.Base{ID: id}, OrderNo: "SCAN" + strconv.FormatInt(id, 10),
			UserID: user.ID, OrganizerID: event.OrganizerID, EventID: event.ID, SessionID: session.ID,
			OrderSource: models.TicketOrderSourceNormal, Status: models.TicketOrderStatusPendingPayment,
			PaymentStatus: models.PaymentStatusUnpaid, IdempotencyKey: "scan-" + strconv.FormatInt(id, 10),
			ExpiresAt: time.Now().Add(-2 * time.Hour),
		}
		if i == 100 {
			order.ExpiresAt = time.Now().Add(-time.Hour)
			validID = id
		}
		if err := env.db.Create(&order).Error; err != nil {
			t.Fatal(err)
		}
		if i == 100 {
			item := models.TicketOrderItem{OrderID: id, TicketTierID: tier.ID, Quantity: 1, UnitPriceCents: tier.PriceCents, SessionStartsAtSnapshot: session.StartsAt}
			if err := env.db.Create(&item).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := env.db.Model(tier).Update("remaining_quota", 19).Error; err != nil {
		t.Fatal(err)
	}
	if err := env.rdb.Set(context.Background(), ticketStockKey(tier.ID), 19, 0).Err(); err != nil {
		t.Fatal(err)
	}
	env.svc.paymentTimeout = 0
	if err := env.svc.CancelExpiredOrders(t.Context()); err == nil {
		t.Fatal("expected invalid-order errors")
	}
	if _, err := env.svc.RecoverStockReturns(t.Context()); err != nil {
		t.Fatal(err)
	}
	var order models.TicketOrder
	if err := env.db.First(&order, validID).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != models.TicketOrderStatusCancelled {
		t.Fatalf("valid order status=%s", order.Status)
	}
	if err := env.db.First(tier, tier.ID).Error; err != nil {
		t.Fatal(err)
	}
	if tier.RemainingQuota != 20 {
		t.Fatalf("stock=%d", tier.RemainingQuota)
	}
}
