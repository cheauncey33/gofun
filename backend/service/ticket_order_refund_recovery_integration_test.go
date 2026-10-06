//go:build integration

package service

import (
	"context"
	"testing"
	"time"

	"gofun/models"
)

// TestRecoverStuckRefunds 验证 refunding 悬挂订单的重驱动：
// CancelOrder 在事务外调用网关退款，进程在两步之间崩溃会把订单留在 refunding。
// 本测试模拟「首段事务已提交、网关已成功退款、收尾事务未执行」的崩溃残留，
// 恢复 Worker 应完成收尾：订单 cancelled+refunded、电子票撤销、流水置 refunded。
func TestRecoverStuckRefunds(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	db := env.db

	tier := env.seedPurchasableTier(t, 10, 5)
	var session models.EventSession
	if err := db.First(&session, tier.SessionID).Error; err != nil {
		t.Fatalf("读取场次失败: %v", err)
	}
	var event models.Event
	if err := db.First(&event, session.EventID).Error; err != nil {
		t.Fatalf("读取活动失败: %v", err)
	}

	user := &models.User{Username: "refund-recovery-" + time.Now().Format("150405.000000000")}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("播种用户失败: %v", err)
	}
	orderID := time.Now().UnixNano()
	order := &models.TicketOrder{
		Base:             models.Base{ID: orderID},
		OrderNo:          "FC" + time.Now().Format("150405.000000000"),
		UserID:           user.ID,
		OrganizerID:      event.OrganizerID,
		EventID:          session.EventID,
		SessionID:        session.ID,
		OrderSource:      models.TicketOrderSourceNormal,
		Status:           models.TicketOrderStatusPaid,
		PaymentStatus:    models.PaymentStatusPaid,
		TotalAmountCents: 100,
		ContactName:      "退款恢复测试",
		ContactPhone:     "13800000000",
		ExpiresAt:        time.Now().Add(-time.Hour),
		Items: []models.TicketOrderItem{{
			TicketTierID:            tier.ID,
			Quantity:                1,
			UnitPriceCents:          100,
			EventTitleSnapshot:      "测试活动",
			TierNameSnapshot:        tier.Name,
			SessionStartsAtSnapshot: session.StartsAt,
		}},
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatalf("播种订单失败: %v", err)
	}
	defer func() {
		db.Exec("DELETE FROM ticket_order_outbox WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM admission_ticket WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM payment_transaction WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM ticket_order_item WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM ticket_order WHERE id = ?", orderID)
		db.Exec("DELETE FROM user WHERE id = ?", user.ID)
	}()

	paymentNo := "PAY-RECOVER-" + time.Now().Format("150405.000000000")
	payment := models.PaymentTransaction{
		PaymentNo:         paymentNo,
		OrderID:           orderID,
		UserID:            user.ID,
		Provider:          "sandbox",
		ProviderPaymentID: paymentNo,
		AmountCents:       100,
		Status:            models.PaymentTransactionSuccess,
		ExpiresAt:         time.Now().Add(-time.Hour),
	}
	if err := db.Create(&payment).Error; err != nil {
		t.Fatalf("播种支付流水失败: %v", err)
	}
	// 让沙箱网关持有 success 状态，等价于「网关侧已收到过支付成功」。
	restorer, ok := env.svc.payment.(PaymentStateRestorer)
	if !ok {
		t.Fatalf("沙箱网关应实现 PaymentStateRestorer")
	}
	if err := restorer.RestorePayment(PaymentStateRestoreRequest{
		PaymentNo: paymentNo, OrderID: orderID, UserID: user.ID,
		AmountCents: 100, Status: "success",
	}); err != nil {
		t.Fatalf("恢复网关状态失败: %v", err)
	}

	ticket := models.AdmissionTicket{
		Base:         models.Base{ID: env.svc.node.Generate().Int64()},
		TicketNo:     "FT" + time.Now().Format("150405.000000000"),
		OrderID:      orderID,
		OrderItemID:  order.Items[0].ID,
		SequenceNo:   1,
		UserID:       user.ID,
		OrganizerID:  order.OrganizerID,
		EventID:      order.EventID,
		SessionID:    order.SessionID,
		TicketTierID: tier.ID,
		Status:       models.AdmissionTicketStatusValid,
		IssuedAt:     time.Now().Add(-time.Hour),
	}
	if err := db.Create(&ticket).Error; err != nil {
		t.Fatalf("播种电子票失败: %v", err)
	}

	// 模拟崩溃残留：首段事务已提交 refunding；UpdateColumn 绕过 autoUpdateTime
	// 把 update_time 拨到宽限期之前。
	if err := db.Model(&models.TicketOrder{}).Where("id = ?", orderID).
		UpdateColumn("payment_status", models.PaymentStatusRefunding).Error; err != nil {
		t.Fatalf("置 refunding 失败: %v", err)
	}
	if err := db.Model(&models.TicketOrder{}).Where("id = ?", orderID).
		UpdateColumn("update_time", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatalf("拨老 update_time 失败: %v", err)
	}

	recovered, err := env.svc.RecoverStuckRefunds(ctx)
	if err != nil {
		t.Fatalf("恢复执行失败: %v", err)
	}
	if recovered != 1 {
		t.Fatalf("恢复笔数 = %d, 期望 1", recovered)
	}

	var after models.TicketOrder
	if err := db.First(&after, orderID).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if after.Status != models.TicketOrderStatusCancelled || after.PaymentStatus != models.PaymentStatusRefunded {
		t.Errorf("订单终态 = %s/%s, 期望 cancelled/refunded", after.Status, after.PaymentStatus)
	}
	var txStatus models.PaymentTransaction
	if err := db.Where("payment_no = ?", paymentNo).First(&txStatus).Error; err != nil {
		t.Fatalf("读取支付流水失败: %v", err)
	}
	if txStatus.Status != models.PaymentTransactionRefunded {
		t.Errorf("支付流水状态 = %s, 期望 refunded", txStatus.Status)
	}
	var ticketStatus models.AdmissionTicket
	if err := db.First(&ticketStatus, ticket.ID).Error; err != nil {
		t.Fatalf("读取电子票失败: %v", err)
	}
	if ticketStatus.Status != models.AdmissionTicketStatusRevoked {
		t.Errorf("电子票状态 = %s, 期望 revoked", ticketStatus.Status)
	}
}

// TestRecoverStuckRefundsRevertsWhenGatewayRejects 验证网关从未退款成功时，
// 恢复 Worker 会把 refunding 回退为 paid，让用户可以重新发起取消。
func TestRecoverStuckRefundsRevertsWhenGatewayRejects(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	db := env.db

	tier := env.seedPurchasableTier(t, 10, 5)
	var session models.EventSession
	if err := db.First(&session, tier.SessionID).Error; err != nil {
		t.Fatalf("读取场次失败: %v", err)
	}
	var event models.Event
	if err := db.First(&event, session.EventID).Error; err != nil {
		t.Fatalf("读取活动失败: %v", err)
	}

	user := &models.User{Username: "refund-revert-" + time.Now().Format("150405.000000000")}
	if err := db.Create(user).Error; err != nil {
		t.Fatalf("播种用户失败: %v", err)
	}
	orderID := time.Now().UnixNano()
	order := &models.TicketOrder{
		Base:             models.Base{ID: orderID},
		OrderNo:          "FC" + time.Now().Format("150405.000000000"),
		UserID:           user.ID,
		OrganizerID:      event.OrganizerID,
		EventID:          session.EventID,
		SessionID:        session.ID,
		OrderSource:      models.TicketOrderSourceNormal,
		Status:           models.TicketOrderStatusPaid,
		PaymentStatus:    models.PaymentStatusRefunding,
		TotalAmountCents: 100,
		ContactName:      "退款回退测试",
		ContactPhone:     "13800000000",
		ExpiresAt:        time.Now().Add(-time.Hour),
		Items: []models.TicketOrderItem{{
			TicketTierID:            tier.ID,
			Quantity:                1,
			UnitPriceCents:          100,
			EventTitleSnapshot:      "测试活动",
			TierNameSnapshot:        tier.Name,
			SessionStartsAtSnapshot: session.StartsAt,
		}},
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatalf("播种订单失败: %v", err)
	}
	defer func() {
		db.Exec("DELETE FROM ticket_order_outbox WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM payment_transaction WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM ticket_order_item WHERE order_id = ?", orderID)
		db.Exec("DELETE FROM ticket_order WHERE id = ?", orderID)
		db.Exec("DELETE FROM user WHERE id = ?", user.ID)
	}()

	// 支付流水 success，但网关内存里没有该流水（未 Restore）——
	// 模拟「网关侧无此支付」的拒绝路径。
	paymentNo := "PAY-REVERT-" + time.Now().Format("150405.000000000")
	payment := models.PaymentTransaction{
		PaymentNo:         paymentNo,
		OrderID:           orderID,
		UserID:            user.ID,
		Provider:          "sandbox",
		ProviderPaymentID: paymentNo,
		AmountCents:       100,
		Status:            models.PaymentTransactionSuccess,
		ExpiresAt:         time.Now().Add(-time.Hour),
	}
	if err := db.Create(&payment).Error; err != nil {
		t.Fatalf("播种支付流水失败: %v", err)
	}
	if err := db.Model(&models.TicketOrder{}).Where("id = ?", orderID).
		UpdateColumn("update_time", time.Now().Add(-10*time.Minute)).Error; err != nil {
		t.Fatalf("拨老 update_time 失败: %v", err)
	}

	recovered, err := env.svc.RecoverStuckRefunds(ctx)
	if err != nil {
		t.Fatalf("恢复执行失败: %v", err)
	}
	// 回退分支不算成功恢复。
	if recovered != 0 {
		t.Fatalf("恢复笔数 = %d, 期望 0（应走回退分支）", recovered)
	}
	var after models.TicketOrder
	if err := db.First(&after, orderID).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if after.Status != models.TicketOrderStatusPaid || after.PaymentStatus != models.PaymentStatusPaid {
		t.Errorf("订单终态 = %s/%s, 期望回退为 paid/paid", after.Status, after.PaymentStatus)
	}
}
