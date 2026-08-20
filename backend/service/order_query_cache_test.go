package service

import (
	"testing"
	"time"

	"gofun/models"
)

func TestOrdersToListJSONKeepsListBrief(t *testing.T) {
	now := time.Now()
	orders := []models.TicketOrder{{
		Base:             models.Base{ID: 1, CreateTime: now, UpdateTime: now},
		OrderNo:          "FC1",
		UserID:           9,
		Status:           models.TicketOrderStatusPendingPayment,
		PaymentStatus:    models.PaymentStatusUnpaid,
		TotalAmountCents: 1200,
		OrderSource:      models.TicketOrderSourceRushSale,
		ExpiresAt:        now.Add(time.Minute),
		Items: []models.TicketOrderItem{{
			TicketTierID:       7,
			Quantity:           2,
			EventTitleSnapshot: "Night Run",
			TierNameSnapshot:   "VIP",
			VenueNameSnapshot:  "Dock",
		}},
	}}
	rows := ordersToListJSON(orders)
	if len(rows) != 1 || rows[0].OrderNo != "FC1" || len(rows[0].Items) != 1 {
		t.Fatalf("unexpected list json: %#v", rows)
	}
	if rows[0].Items[0].EventTitleSnapshot != "Night Run" || rows[0].Items[0].Quantity != 2 {
		t.Fatalf("brief item missing fields: %#v", rows[0].Items[0])
	}
	roundTrip := listJSONToOrders(rows)
	if len(roundTrip) != 1 || roundTrip[0].Items[0].TierNameSnapshot != "VIP" {
		t.Fatalf("round trip failed: %#v", roundTrip)
	}
}
