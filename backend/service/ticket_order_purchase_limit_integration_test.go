//go:build integration

package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"gofun/models"

	"gorm.io/gorm"
)

// TestEnforceUserEventPurchaseLimit 验证普通票限购的权威校验：
// 已购数量 + 本次数量超过 MaxTicketsPerOrder 时必须失败并回滚事务。
// 该校验在 createOrderAndOutbox 的事务内、用户行锁之后执行，
// 消除「先 SELECT SUM 再写单」可被并发突破的竞态。
func TestEnforceUserEventPurchaseLimit(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	db := env.db

	user := &models.User{Username: "limit-test-" + time.Now().Format("150405.000000000")}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("播种用户失败: %v", err)
	}
	eventID := time.Now().UnixNano()
	defer func() {
		db.Exec("DELETE FROM ticket_order_item WHERE order_id IN (SELECT id FROM ticket_order WHERE user_id = ?)", user.ID)
		db.Exec("DELETE FROM ticket_order WHERE user_id = ?", user.ID)
		db.Exec("DELETE FROM user WHERE id = ?", user.ID)
	}()

	// 已购 1 张（paid 订单）。
	orderID := time.Now().UnixNano()
	paid := &models.TicketOrder{
		Base:             models.Base{ID: orderID},
		OrderNo:          "FC" + time.Now().Format("150405.000000000"),
		UserID:           user.ID,
		OrganizerID:      1,
		EventID:          eventID,
		SessionID:        1,
		OrderSource:      models.TicketOrderSourceNormal,
		Status:           models.TicketOrderStatusPaid,
		PaymentStatus:    models.PaymentStatusPaid,
		TotalAmountCents: 100,
		ContactName:      "限购测试",
		ContactPhone:     "13800000000",
		ExpiresAt:        time.Now().Add(time.Hour),
		Items: []models.TicketOrderItem{{
			TicketTierID:            1,
			Quantity:                1,
			UnitPriceCents:          100,
			EventTitleSnapshot:      "测试活动",
			TierNameSnapshot:        "普通票",
			SessionStartsAtSnapshot: time.Now().Add(time.Hour),
		}},
	}
	if err := db.Create(paid).Error; err != nil {
		t.Fatalf("播种已支付订单失败: %v", err)
	}

	const maxPerOrder = 2

	// 超限：1 + 2 > 2，应拒绝且返回业务哨兵。
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return enforceUserEventPurchaseLimit(tx, user.ID, eventID, 2, maxPerOrder)
	})
	if !errors.Is(err, ErrTicketOrderUnavailable) {
		t.Fatalf("已购1张再买2张应返回 ErrTicketOrderUnavailable, 实际 %v", err)
	}
	// 事务回滚后已购订单仍是 1 张。
	var orderCount int64
	db.Model(&models.TicketOrder{}).Where("user_id = ? AND event_id = ? AND status IN ?",
		user.ID, eventID, []models.TicketOrderStatus{
			models.TicketOrderStatusQueued,
			models.TicketOrderStatusPendingPayment,
			models.TicketOrderStatusPaid,
		}).Count(&orderCount)
	if orderCount != 1 {
		t.Fatalf("回滚后订单数 = %d, 期望 1", orderCount)
	}

	// 未超限：1 + 1 == 2，应通过。
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return enforceUserEventPurchaseLimit(tx, user.ID, eventID, 1, maxPerOrder)
	}); err != nil {
		t.Fatalf("已购1张再买1张应通过: %v", err)
	}

	// 用户不存在时必须拒绝：防止外键缺失时 SUM 统计为 0 而放行。
	if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return enforceUserEventPurchaseLimit(tx, user.ID+999_999_999, eventID, 1, maxPerOrder)
	}); err == nil {
		t.Fatalf("用户不存在应被拒绝")
	}
}
