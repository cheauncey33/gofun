//go:build integration

package service

import (
	"context"
	"errors"
	"fmt"
	"gofun/models"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

type consumerSelectCounter struct {
	gormlogger.Interface
	count int
}

func (l *consumerSelectCounter) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	if strings.HasPrefix(sql, "SELECT") {
		l.count++
	}
}

func TestIntegrationConsumerReadsOrderEvidenceInTwoQueries(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	tier := env.seedPurchasableTier(t, 10, 10)
	user := env.newUser(t, fmt.Sprintf("consumer-queries-%d", tier.ID))
	receipt, err := env.svc.CreateOrder(t.Context(), user.ID, "query-count", "request", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 2, PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatal(err)
	}
	counter := &consumerSelectCounter{Interface: gormlogger.Default.LogMode(gormlogger.Silent)}
	env.svc.workerDB = env.db.Session(&gorm.Session{Logger: counter})
	if err := env.svc.ProcessOrderTask(t.Context(), TicketOrderMessage{
		EventID: receipt.OrderID + 100, EventType: ticketOrderFinalizeEventType,
		OrderID: receipt.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if counter.count != 2 {
		t.Fatalf("consumer SELECT count=%d, want 2", counter.count)
	}
	var order models.TicketOrder
	if err := env.db.First(&order, receipt.OrderID).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != models.TicketOrderStatusPendingPayment {
		t.Fatalf("order status=%s", order.Status)
	}
}

func TestIntegrationConsumerRejectsMismatchedEvidence(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	tier := env.seedPurchasableTier(t, 20, 20)
	user := env.newUser(t, fmt.Sprintf("consumer-evidence-%d", tier.ID))
	for _, name := range []string{"quantity", "tier", "user", "campaign"} {
		t.Run(name, func(t *testing.T) {
			receipt, err := env.svc.CreateOrder(t.Context(), user.ID, "evidence-"+name, "request", CreateTicketOrderInput{
				TicketTierID: tier.ID, Quantity: 2, PurchaseInfoInput: validPurchaseInfo(),
			})
			if err != nil {
				t.Fatal(err)
			}
			message := TicketOrderMessage{EventID: receipt.OrderID + 100, EventType: ticketOrderFinalizeEventType,
				OrderID: receipt.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 2}
			switch name {
			case "quantity":
				message.Quantity++
			case "tier":
				message.TicketTierID++
			case "user":
				message.UserID++
			case "campaign":
				campaignID := tier.ID
				message.RushSaleCampaignID = &campaignID
			}
			if err := env.svc.ProcessOrderTask(t.Context(), message); !errors.Is(err, ErrTicketOrderNonRetryable) {
				t.Fatalf("mismatched evidence error=%v", err)
			}
			var order models.TicketOrder
			if err := env.db.First(&order, receipt.OrderID).Error; err != nil {
				t.Fatal(err)
			}
			var inboxCount int64
			if err := env.db.Model(&models.TicketOrderConsumerInbox{}).Where("event_id = ?", message.EventID).Count(&inboxCount).Error; err != nil {
				t.Fatal(err)
			}
			if order.Status != models.TicketOrderStatusQueued || inboxCount != 0 {
				t.Fatalf("failed validation committed: status=%s inbox=%d", order.Status, inboxCount)
			}
		})
	}
	var remaining int
	if err := env.db.Model(&models.TicketTier{}).Select("remaining_quota").Where("id = ?", tier.ID).Scan(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 20 {
		t.Fatalf("failed validation changed stock: %d", remaining)
	}
}
