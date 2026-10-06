//go:build integration

package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"gofun/models"
	"gorm.io/gorm"
	"testing"
	"time"
)

func createPendingRecoveryOrder(t *testing.T, env *orderIntegrationEnv) (*models.TicketTier, *models.TicketOrder) {
	t.Helper()
	tier := env.seedPurchasableTier(t, 3, 3)
	user := env.newUser(t, fmt.Sprintf("return-user-%d", tier.ID))
	receipt, err := env.svc.CreateOrder(t.Context(), user.ID, fmt.Sprintf("return-%d", tier.ID), "return-request", CreateTicketOrderInput{TicketTierID: tier.ID, Quantity: 1, PurchaseInfoInput: validPurchaseInfo()})
	if err != nil {
		t.Fatal(err)
	}
	message := TicketOrderMessage{EventID: env.svc.node.Generate().Int64(), EventType: ticketOrderFinalizeEventType, OrderID: receipt.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 1}
	if err := env.svc.ProcessOrderTask(t.Context(), message); err != nil {
		t.Fatal(err)
	}
	var order models.TicketOrder
	if err := env.db.Preload("Items").First(&order, receipt.OrderID).Error; err != nil {
		t.Fatal(err)
	}
	return tier, &order
}

func TestIntegrationReturnIntentSurvivesRedisFailureAndRestart(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	tier, order := createPendingRecoveryOrder(t, env)
	env.mr.SetError("temporary redis failure")
	if err := env.svc.CancelOrder(t.Context(), order.UserID, order.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	var intent models.TicketOrderOutbox
	if err := env.db.Where("order_id = ? AND event_type = ?", order.ID, stockRecoveryEventType).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	if intent.Status != models.TicketOrderOutboxPending {
		t.Fatalf("status=%s", intent.Status)
	}
	if _, err := env.svc.RecoverStockReturns(t.Context()); err == nil {
		t.Fatal("expected redis failure")
	}
	if err := env.db.First(&intent, intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if intent.Attempts != 1 || intent.LastError == "" {
		t.Fatalf("missing failure progress: %+v", intent)
	}
	env.mr.SetError("")
	if err := env.svc.WarmTicketQuota(t.Context()); err != nil {
		t.Fatal(err)
	}
	if value, _ := env.rdb.Get(t.Context(), ticketStockKey(tier.ID)).Int(); value != 2 {
		t.Fatalf("warm overwrote pending return: stock=%d", value)
	}
	if err := env.db.Model(&intent).Update("next_attempt_at", time.Now().Add(-time.Second)).Error; err != nil {
		t.Fatal(err)
	}
	restarted := &TicketOrderService{db: env.db, rdb: env.rdb, node: env.svc.node, inventory: env.svc.inventory, inventoryConfig: env.svc.inventoryConfig}
	if _, err := restarted.RecoverStockReturns(t.Context()); err != nil {
		t.Fatal(err)
	}
	if value, err := env.rdb.Get(t.Context(), ticketStockKey(tier.ID)).Int(); err != nil || value != 3 {
		t.Fatalf("stock=%d err=%v", value, err)
	}
	if err := env.db.First(&intent, intent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if intent.Status != stockRecoveryCompleted {
		t.Fatalf("status=%s", intent.Status)
	}
	if _, err := restarted.RecoverStockReturns(t.Context()); err != nil {
		t.Fatal(err)
	}
	if value, _ := env.rdb.Get(t.Context(), ticketStockKey(tier.ID)).Int(); value != 3 {
		t.Fatalf("replay stock=%d", value)
	}
}

func TestIntegrationReturnIntentRollsBackWithTransaction(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	tier, order := createPendingRecoveryOrder(t, env)
	err := env.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(order).Update("status", models.TicketOrderStatusCancelled).Error; err != nil {
			return err
		}
		if err := env.svc.enqueueOrderStockReturn(tx, order, nil, nil, false, "cancel"); err != nil {
			return err
		}
		return errors.New("rollback")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	var count int64
	if err := env.db.Model(&models.TicketOrderOutbox{}).Where("order_id = ? AND event_type = ?", order.ID, stockRecoveryEventType).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("return intents=%d", count)
	}
	if err := env.db.First(order, order.ID).Error; err != nil {
		t.Fatal(err)
	}
	if order.Status != models.TicketOrderStatusPendingPayment {
		t.Fatalf("status=%s", order.Status)
	}
	if value, _ := env.rdb.Get(t.Context(), ticketStockKey(tier.ID)).Int(); value != 2 {
		t.Fatalf("stock=%d", value)
	}
}

func TestIntegrationRedisReturnReplayAfterDBRollback(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	tier, order := createPendingRecoveryOrder(t, env)
	if err := env.svc.CancelOrder(t.Context(), order.UserID, order.ID, "cancel"); err != nil {
		t.Fatal(err)
	}
	var intent models.TicketOrderOutbox
	if err := env.db.Where("order_id = ? AND event_type = ?", order.ID, stockRecoveryEventType).First(&intent).Error; err != nil {
		t.Fatal(err)
	}
	var payload stockRecoveryPayload
	if err := json.Unmarshal([]byte(intent.Payload), &payload); err != nil {
		t.Fatal(err)
	}
	err := env.db.Transaction(func(tx *gorm.DB) error {
		for _, task := range payload.Tasks {
			if _, err := env.svc.stockReturnExecutor().execute(t.Context(), task); err != nil {
				return err
			}
		}
		if err := tx.Model(&intent).Update("status", stockRecoveryCompleted).Error; err != nil {
			return err
		}
		return errors.New("commit interrupted")
	})
	if err == nil {
		t.Fatal("expected rollback")
	}
	if err := env.svc.WarmTicketQuota(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := env.svc.RecoverStockReturns(t.Context()); err != nil {
		t.Fatal(err)
	}
	if value, _ := env.rdb.Get(t.Context(), ticketStockKey(tier.ID)).Int(); value != 3 {
		t.Fatalf("stock=%d", value)
	}
}
