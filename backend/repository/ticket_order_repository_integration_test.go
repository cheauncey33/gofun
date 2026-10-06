//go:build integration

package repository

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"gofun/migrations"
	"gofun/models"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// repository 集成测试：依赖 MySQL DSN（与 service 层集成测试同库复用）。
// 运行：
//
//	set GOFUN_TEST_MYSQL_DSN=root:root@tcp(127.0.0.1:3307)/gofun_test?charset=utf8mb4&parseTime=True&loc=Local
//	go test ./repository/ -tags=integration -count=1 -v

func testRepoDSN(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(os.Getenv("GOFUN_TEST_MYSQL_DSN"))
}

type repoTestEnv struct {
	db   *gorm.DB
	repo *TicketOrderRepository
}

func newRepoTestEnv(t *testing.T) *repoTestEnv {
	t.Helper()
	dsn := testRepoDSN(t)
	if dsn == "" {
		t.Skip("未设置 GOFUN_TEST_MYSQL_DSN，跳过集成测试")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Fatalf("连接测试库失败: %v", err)
	}
	if err := migrations.Run(db); err != nil {
		t.Fatalf("数据库迁移: %v", err)
	}
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return &repoTestEnv{db: db, repo: NewTicketOrderRepository(db)}
}

func (e *repoTestEnv) cleanupOrder(t *testing.T, orderID int64) {
	t.Helper()
	e.db.Exec("DELETE FROM ticket_order_item WHERE order_id = ?", orderID)
	e.db.Exec("DELETE FROM ticket_order_attendee WHERE order_id = ?", orderID)
	e.db.Exec("DELETE FROM ticket_order WHERE id = ?", orderID)
}

func seedOrder(t *testing.T, db *gorm.DB, orderID, userID, eventID int64, status models.TicketOrderStatus, qty int) *models.TicketOrder {
	t.Helper()
	order := &models.TicketOrder{
		Base:             models.Base{ID: orderID},
		OrderNo:          "FC" + time.Now().Format("150405.000000000"),
		UserID:           userID,
		IdempotencyKey:   time.Now().Format("150405.000000000"),
		OrganizerID:      1,
		EventID:          eventID,
		SessionID:        1,
		OrderSource:      models.TicketOrderSourceNormal,
		Status:           status,
		PaymentStatus:    models.PaymentStatusUnpaid,
		TotalAmountCents: 100,
		ContactName:      "测试",
		ContactPhone:     "13800000000",
		ExpiresAt:        time.Now().Add(time.Hour),
		Items: []models.TicketOrderItem{{
			TicketTierID:            1,
			Quantity:                qty,
			UnitPriceCents:          100,
			EventTitleSnapshot:      "测试活动",
			TierNameSnapshot:        "普通票",
			SessionStartsAtSnapshot: time.Now().Add(time.Hour),
		}},
	}
	if err := db.Create(order).Error; err != nil {
		t.Fatalf("播种订单失败: %v", err)
	}
	return order
}

func TestRepoLoadOrderDetailIsolation(t *testing.T) {
	env := newRepoTestEnv(t)
	ctx := context.Background()
	orderID := time.Now().UnixNano()
	seedOrder(t, env.db, orderID, 9101, 9200, models.TicketOrderStatusPendingPayment, 2)
	defer env.cleanupOrder(t, orderID)

	order, err := env.repo.LoadOrderDetail(ctx, 9101, orderID)
	if err != nil {
		t.Fatalf("加载订单详情失败: %v", err)
	}
	if len(order.Items) != 1 || order.Items[0].Quantity != 2 {
		t.Errorf("Items 预加载异常: %+v", order.Items)
	}

	if _, err := env.repo.LoadOrderDetail(ctx, 9999, orderID); err == nil {
		t.Errorf("其他用户加载他人订单应返回 ErrRecordNotFound")
	}
}

func TestRepoMarkQueuedOrderFailedIdempotent(t *testing.T) {
	env := newRepoTestEnv(t)
	ctx := context.Background()
	orderID := time.Now().UnixNano()
	seedOrder(t, env.db, orderID, 9102, 9201, models.TicketOrderStatusQueued, 1)
	defer env.cleanupOrder(t, orderID)

	if err := env.repo.MarkQueuedOrderFailed(ctx, orderID, "测试失败"); err != nil {
		t.Fatalf("标记失败: %v", err)
	}
	var order models.TicketOrder
	if err := env.db.First(&order, orderID).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if order.Status != models.TicketOrderStatusFailed || order.CancelReason != "测试失败" {
		t.Errorf("状态 = %s, reason = %s", order.Status, order.CancelReason)
	}
	// 已是 failed 的订单不会被再次改写（幂等）。
	if err := env.repo.MarkQueuedOrderFailed(ctx, orderID, "二次"); err != nil {
		t.Fatalf("重复标记应成功返回: %v", err)
	}
	env.db.First(&order, orderID)
	if order.CancelReason != "测试失败" {
		t.Errorf("重复标记不应覆盖已有原因, 实际 %q", order.CancelReason)
	}
}

func TestRepoUserTicketQueries(t *testing.T) {
	env := newRepoTestEnv(t)
	ctx := context.Background()
	userID := int64(9103)
	base := time.Now().UnixNano()
	older := seedOrder(t, env.db, base, userID, 9202, models.TicketOrderStatusPaid, 1)
	newer := seedOrder(t, env.db, base+1, userID, 9202, models.TicketOrderStatusPaid, 1)
	defer func() {
		env.db.Exec("DELETE FROM admission_ticket WHERE user_id = ?", userID)
		env.cleanupOrder(t, older.ID)
		env.cleanupOrder(t, newer.ID)
	}()

	seedTicket := func(order *models.TicketOrder, itemID int64, issuedAt time.Time) {
		t.Helper()
		ticket := models.AdmissionTicket{
			Base:         models.Base{ID: time.Now().UnixNano()},
			TicketNo:     "FT" + time.Now().Format("150405.000000000"),
			OrderID:      order.ID,
			OrderItemID:  itemID,
			SequenceNo:   1,
			UserID:       userID,
			OrganizerID:  order.OrganizerID,
			EventID:      order.EventID,
			SessionID:    order.SessionID,
			TicketTierID: 1,
			Status:       models.AdmissionTicketStatusValid,
			IssuedAt:     issuedAt,
		}
		if err := env.db.Create(&ticket).Error; err != nil {
			t.Fatalf("播种电子票失败: %v", err)
		}
	}
	seedTicket(older, older.Items[0].ID, time.Now().Add(-2*time.Hour))
	seedTicket(newer, newer.Items[0].ID, time.Now().Add(-1*time.Hour))

	total, err := env.repo.CountUserTickets(ctx, userID)
	if err != nil || total != 2 {
		t.Fatalf("CountUserTickets = %d, err = %v", total, err)
	}
	// issued_at DESC：最近签发的排最前；分页 size=1 应取到 newer。
	page1, err := env.repo.ListUserTickets(ctx, userID, 1, 0)
	if err != nil || len(page1) != 1 || page1[0].OrderID != newer.ID {
		t.Fatalf("第一页 = %+v, err = %v", page1, err)
	}
	page2, err := env.repo.ListUserTickets(ctx, userID, 1, 1)
	if err != nil || len(page2) != 1 || page2[0].OrderID != older.ID {
		t.Fatalf("第二页 = %+v, err = %v", page2, err)
	}
	if page1[0].OrderItem.ID != newer.Items[0].ID {
		t.Errorf("OrderItem 预加载异常: %+v", page1[0].OrderItem)
	}
}
