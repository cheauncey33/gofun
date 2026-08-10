//go:build integration

package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gofun/config"
	"gofun/container"
	"gofun/models"

	"github.com/alicebob/miniredis/v2"
	"github.com/bwmarrin/snowflake"
	gocache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// testMySQLDSN 从环境变量读取测试库 DSN；未设置时返回空串（调用方据此 Skip）。
func testMySQLDSN(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(os.Getenv("GOFUN_TEST_MYSQL_DSN"))
}

// 集成测试（入口正确性）：miniredis 提供 Redis Lua，MySQL 用本地 DSN。
// 运行：
//   set GOFUN_TEST_MYSQL_DSN=root:root@tcp(127.0.0.1:3307)/gofun_test?charset=utf8mb4^&parseTime=True^&loc=Local
//   go test ./service/ -tags=integration -run Integration -count=1 -v
//
// 覆盖三条核心保证：不超卖、幂等重试、单笔限购。

var ticketIntegrationModels = []interface{}{
	&models.User{},
	&models.Organizer{},
	&models.OrganizerMember{},
	&models.Venue{},
	&models.Event{},
	&models.EventSession{},
	&models.TicketTier{},
	&models.TicketTierBucket{},
	&models.TicketOrder{},
	&models.TicketOrderItem{},
	&models.TicketOrderAttendee{},
	&models.TicketOrderOutbox{},
	&models.PaymentTransaction{},
	&models.PaymentCallback{},
	&models.RushSaleCampaign{},
	&models.RushCampaignBucket{},
	&models.AdmissionTicket{},
	&models.TicketVerificationRecord{},
	&models.EventComment{},
}

type orderIntegrationEnv struct {
	svc *TicketOrderService
	db  *gorm.DB
	rdb *redis.Client
	mr  *miniredis.Miniredis
}

// newOrderIntegrationEnv 装配一个无 MQ 的 TicketOrderService；
// 订单和 Outbox 行始终在同一主库事务中落库，无需 RabbitMQ 即可验证原子性。
func newOrderIntegrationEnv(t *testing.T) *orderIntegrationEnv {
	t.Helper()

	dsn := testMySQLDSN(t)
	if dsn == "" {
		t.Skip("未设置 GOFUN_TEST_MYSQL_DSN，跳过集成测试")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
		Logger:         gormlogger.Default.LogMode(gormlogger.Silent),
	})
	if err != nil {
		t.Skipf("MySQL 不可用，跳过集成测试（设置 GOFUN_TEST_MYSQL_DSN）: %v", err)
	}
	if err := db.AutoMigrate(ticketIntegrationModels...); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}

	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})

	node, err := snowflake.NewNode(1)
	if err != nil {
		t.Fatalf("snowflake node: %v", err)
	}
	cont := &container.Container{
		DB:              db,
		RDB:             rdb,
		SnowflakeNode:   node,
		LocalCache:      gocache.New(time.Minute, time.Minute),
		TicketQRSecret:  []byte("integration-qr-secret"),
		TicketQRSecrets: [][]byte{[]byte("integration-qr-secret")},
	}
	svc := NewTicketOrderService(cont, 15, config.PaymentConfig{})

	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
		_ = rdb.Close()
	})
	return &orderIntegrationEnv{svc: svc, db: db, rdb: rdb, mr: mr}
}

// seedPurchasableTier 建立一条完整可售票档：
// organizer → venue → event(published) → session(on_sale，当前在售窗口) → tier(on_sale)。
func (e *orderIntegrationEnv) seedPurchasableTier(t *testing.T, totalQuota, purchaseLimit int) *models.TicketTier {
	t.Helper()
	now := time.Now()

	org := models.Organizer{Name: "测试主办方", Slug: "org-" + strconv.FormatInt(now.UnixNano(), 10), Status: models.OrganizerStatusActive, AuditStatus: models.AuditStatusApproved}
	if err := e.db.Create(&org).Error; err != nil {
		t.Fatalf("create organizer: %v", err)
	}
	venue := models.Venue{OrganizerID: org.ID, Name: "测试场馆", City: "武汉", Address: "测试地址 1 号"}
	if err := e.db.Create(&venue).Error; err != nil {
		t.Fatalf("create venue: %v", err)
	}
	event := models.Event{
		OrganizerID: org.ID, Title: "测试活动", Category: "livehouse",
		Status: models.EventStatusPublished, RealNameRequired: false, MaxTicketsPerOrder: 20,
	}
	if err := e.db.Create(&event).Error; err != nil {
		t.Fatalf("create event: %v", err)
	}
	session := models.EventSession{
		EventID: event.ID, VenueID: venue.ID,
		StartsAt: now.Add(72 * time.Hour), EndsAt: now.Add(74 * time.Hour),
		SaleStartsAt: now.Add(-time.Hour), SaleEndsAt: now.Add(24 * time.Hour),
		Status: models.SessionStatusOnSale,
	}
	if err := e.db.Create(&session).Error; err != nil {
		t.Fatalf("create session: %v", err)
	}
	tier := models.TicketTier{
		SessionID: session.ID, Name: "标准票", PriceCents: 9900,
		TotalQuota: totalQuota, RemainingQuota: totalQuota,
		PurchaseLimit: purchaseLimit, Status: models.TicketTierStatusOnSale,
	}
	if err := e.db.Create(&tier).Error; err != nil {
		t.Fatalf("create tier: %v", err)
	}
	// 预热 Redis 库存（非分桶模式单 key）。
	if err := e.rdb.Set(context.Background(), ticketStockKey(tier.ID), totalQuota, 0).Err(); err != nil {
		t.Fatalf("warm redis stock: %v", err)
	}
	return &tier
}

func (e *orderIntegrationEnv) newUser(t *testing.T, username string) *models.User {
	t.Helper()
	u := models.User{Username: username, Password: "x", Role: "user"}
	if err := e.db.Create(&u).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &u
}

func validPurchaseInfo() PurchaseInfoInput {
	return PurchaseInfoInput{ContactName: "张三", ContactPhone: "13800138000", TermsAccepted: true}
}

// 用例 1：N 个不同用户并发抢 M 张票（N>M），成功数恒等于 M，Redis 库存归零，无超卖。
func TestIntegrationNoOversell(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()

	const stock = 20
	const buyers = 50
	tier := env.seedPurchasableTier(t, stock, 6)

	var wg sync.WaitGroup
	var mu sync.Mutex
	succeeded := 0
	for i := 0; i < buyers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			user := env.newUser(t, fmt.Sprintf("buyer-%d-%d", tier.ID, i))
			_, err := env.svc.CreateOrder(ctx, user.ID, fmt.Sprintf("idem-oversell-%d-%d", tier.ID, i), "req", CreateTicketOrderInput{
				TicketTierID:      tier.ID,
				Quantity:          1,
				PurchaseInfoInput: validPurchaseInfo(),
			})
			if err == nil {
				mu.Lock()
				succeeded++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if succeeded != stock {
		t.Fatalf("成功下单数 = %d，应等于库存 %d", succeeded, stock)
	}
	remain, err := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int64()
	if err != nil {
		t.Fatalf("read redis stock: %v", err)
	}
	if remain != 0 {
		t.Fatalf("Redis 剩余库存 = %d，应为 0", remain)
	}
	var orderCount int64
	if err := env.db.Model(&models.TicketOrder{}).Where("event_id = ?", tier.SessionID).Count(&orderCount).Error; err == nil {
		_ = orderCount // 订单数校验见下，按 user 维度更稳
	}
	var dbOrders int64
	env.db.Model(&models.TicketOrder{}).
		Where("id IN (?)", env.db.Model(&models.TicketOrderItem{}).Select("order_id").Where("ticket_tier_id = ?", tier.ID)).
		Count(&dbOrders)
	if dbOrders != int64(stock) {
		t.Fatalf("MySQL 订单数 = %d，应等于库存 %d（无超卖/无少卖）", dbOrders, stock)
	}
}

// 用例 2：同一用户携带相同幂等键重复下单，只应创建一个订单，且只扣一份库存。
func TestIntegrationIdempotentRetry(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()

	tier := env.seedPurchasableTier(t, 100, 6)
	user := env.newUser(t, fmt.Sprintf("idem-user-%d", tier.ID))
	idemKey := fmt.Sprintf("idem-retry-%d", tier.ID)

	first, err := env.svc.CreateOrder(ctx, user.ID, idemKey, "req-1", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 2, PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("首次下单失败: %v", err)
	}

	// 并发重试同一幂等键。
	var wg sync.WaitGroup
	receipts := make([]*TicketOrderReceipt, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r, _ := env.svc.CreateOrder(ctx, user.ID, idemKey, fmt.Sprintf("req-r%d", i), CreateTicketOrderInput{
				TicketTierID: tier.ID, Quantity: 2, PurchaseInfoInput: validPurchaseInfo(),
			})
			receipts[i] = r
		}(i)
	}
	wg.Wait()

	for i, r := range receipts {
		if r == nil {
			t.Fatalf("第 %d 次重试返回空凭据", i)
		}
		if r.OrderID != first.OrderID {
			t.Fatalf("第 %d 次重试得到不同订单 %d，应为 %d", i, r.OrderID, first.OrderID)
		}
	}
	var orderCount int64
	env.db.Model(&models.TicketOrder{}).Where("user_id = ? AND idempotency_key = ?", user.ID, idemKey).Count(&orderCount)
	if orderCount != 1 {
		t.Fatalf("相同幂等键建单数 = %d，应为 1", orderCount)
	}
	remain, _ := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int64()
	if remain != 98 {
		t.Fatalf("Redis 库存 = %d，应为 98（只扣一份 quantity=2）", remain)
	}
}

// 用例 3：单笔数量超过票档限购 purchase_limit 时被拒绝，不扣库存、不落单。
func TestIntegrationPurchaseLimitRejected(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()

	tier := env.seedPurchasableTier(t, 100, 2) // 单笔限购 2
	user := env.newUser(t, fmt.Sprintf("limit-user-%d", tier.ID))

	_, err := env.svc.CreateOrder(ctx, user.ID, fmt.Sprintf("idem-limit-%d", tier.ID), "req", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 3, PurchaseInfoInput: validPurchaseInfo(), // 超限购
	})
	if err == nil {
		t.Fatal("超过单笔限购应返回错误")
	}

	remain, rerr := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int64()
	if rerr != nil {
		t.Fatalf("read redis stock: %v", rerr)
	}
	if remain != 100 {
		t.Fatalf("被拒后 Redis 库存 = %d，应为 100（未扣减）", remain)
	}
	var orderCount int64
	env.db.Model(&models.TicketOrder{}).Where("user_id = ?", user.ID).Count(&orderCount)
	if orderCount != 0 {
		t.Fatalf("被拒后仍有 %d 个订单落库，应为 0", orderCount)
	}
}

func TestIntegrationTransactionalOutboxCommitAndRollback(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 20, 6)
	user := env.newUser(t, fmt.Sprintf("outbox-user-%d", tier.ID))
	var session models.EventSession
	if err := env.db.First(&session, tier.SessionID).Error; err != nil {
		t.Fatalf("读取测试场次失败: %v", err)
	}
	var event models.Event
	if err := env.db.First(&event, session.EventID).Error; err != nil {
		t.Fatalf("读取测试活动失败: %v", err)
	}

	newOrder := func() *models.TicketOrder {
		id := env.svc.node.Generate().Int64()
		return &models.TicketOrder{
			Base:             models.Base{ID: id},
			OrderNo:          "FC" + strconv.FormatInt(id, 10),
			UserID:           user.ID,
			OrganizerID:      event.OrganizerID,
			EventID:          event.ID,
			SessionID:        tier.SessionID,
			Status:           models.TicketOrderStatusQueued,
			PaymentStatus:    models.PaymentStatusUnpaid,
			TotalAmountCents: tier.PriceCents,
			IdempotencyKey:   fmt.Sprintf("outbox-%d", id),
		}
	}
	message := func(order *models.TicketOrder) TicketOrderMessage {
		return TicketOrderMessage{OrderID: order.ID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 1}
	}

	rolledBack := newOrder()
	err := env.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(rolledBack).Error; err != nil {
			return err
		}
		if err := env.svc.enqueueOutboxInTx(ctx, tx, rolledBack.ID, message(rolledBack)); err != nil {
			return err
		}
		return fmt.Errorf("force transactional rollback")
	})
	if err == nil {
		t.Fatal("expected forced transaction rollback")
	}
	var count int64
	env.db.Model(&models.TicketOrder{}).Where("id = ?", rolledBack.ID).Count(&count)
	if count != 0 {
		t.Fatalf("rollback 后订单仍存在: %d", count)
	}
	env.db.Model(&models.TicketOrderOutbox{}).Where("order_id = ?", rolledBack.ID).Count(&count)
	if count != 0 {
		t.Fatalf("rollback 后 outbox 仍存在: %d", count)
	}

	committed := newOrder()
	if err := env.svc.createOrderAndOutbox(ctx, committed, message(committed)); err != nil {
		t.Fatalf("订单和 outbox 同事务提交失败: %v", err)
	}
	env.db.Model(&models.TicketOrder{}).Where("id = ?", committed.ID).Count(&count)
	if count != 1 {
		t.Fatalf("提交后订单数 = %d，应为 1", count)
	}
	env.db.Model(&models.TicketOrderOutbox{}).Where("order_id = ? AND status = ?", committed.ID, models.TicketOrderOutboxPending).Count(&count)
	if count != 1 {
		t.Fatalf("提交后 pending outbox 数 = %d，应为 1", count)
	}
}

func TestIntegrationConsumerReservesBeforePendingPayment(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 4, 6)
	user := env.newUser(t, fmt.Sprintf("consumer-user-%d", tier.ID))

	receipt, err := env.svc.CreateOrder(ctx, user.ID, fmt.Sprintf("consumer-success-%d", tier.ID), "req", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 3, PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("创建成功订单失败: %v", err)
	}
	message := TicketOrderMessage{OrderID: receipt.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 3}
	if err := env.svc.ProcessOrderTask(ctx, message); err != nil {
		t.Fatalf("消费者扣库存失败: %v", err)
	}
	var order models.TicketOrder
	if err := env.db.First(&order, receipt.OrderID).Error; err != nil {
		t.Fatalf("读取订单失败: %v", err)
	}
	if order.Status != models.TicketOrderStatusPendingPayment {
		t.Fatalf("库存成功后订单状态 = %s，应为 pending_payment", order.Status)
	}
	var tierAfter models.TicketTier
	if err := env.db.First(&tierAfter, tier.ID).Error; err != nil {
		t.Fatalf("读取票档失败: %v", err)
	}
	if tierAfter.RemainingQuota != 1 {
		t.Fatalf("库存扣减后剩余 = %d，应为 1", tierAfter.RemainingQuota)
	}

	// 让 Redis 仍允许入口创建，再把主库库存压到不足，验证消费者失败时事务回滚且订单不会提前进入 pending_payment。
	_ = env.rdb.Set(ctx, ticketStockKey(tier.ID), 10, 0).Err()
	failedReceipt, err := env.svc.CreateOrder(ctx, user.ID, fmt.Sprintf("consumer-failure-%d", tier.ID), "req", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 2, PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("创建待失败订单失败: %v", err)
	}
	if err := env.db.Model(&models.TicketTier{}).Where("id = ?", tier.ID).Update("remaining_quota", 1).Error; err != nil {
		t.Fatalf("准备不足库存失败: %v", err)
	}
	failure := env.svc.ProcessOrderTask(ctx, TicketOrderMessage{
		OrderID: failedReceipt.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 2,
	})
	if failure == nil {
		t.Fatal("库存不足时消费者应返回错误")
	}
	if err := env.db.First(&order, failedReceipt.OrderID).Error; err != nil {
		t.Fatalf("读取失败订单: %v", err)
	}
	if order.Status != models.TicketOrderStatusQueued {
		t.Fatalf("库存失败后订单状态 = %s，应保持 queued", order.Status)
	}
}

func TestIntegrationPaymentAndTimeoutSerialize(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 3, 6)
	user := env.newUser(t, fmt.Sprintf("payment-timeout-user-%d", tier.ID))
	receipt, err := env.svc.CreateOrder(ctx, user.ID, fmt.Sprintf("payment-timeout-%d", tier.ID), "req", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 1, PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("创建支付竞争订单失败: %v", err)
	}
	if err := env.svc.ProcessOrderTask(ctx, TicketOrderMessage{OrderID: receipt.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 1}); err != nil {
		t.Fatalf("推进 pending_payment 失败: %v", err)
	}
	intent, err := env.svc.PayOrder(ctx, user.ID, receipt.OrderID, "manual")
	if err != nil {
		t.Fatalf("创建待支付交易失败: %v", err)
	}
	notification := PaymentNotification{
		ProviderEventID: fmt.Sprintf("race-event-%d", receipt.OrderID),
		PaymentNo:       intent.PaymentNo,
		Provider:        intent.Provider,
		Status:          "success",
		AmountCents:     intent.AmountCents,
	}
	gateway, ok := env.svc.payment.(*SandboxPaymentGateway)
	if !ok {
		t.Fatal("集成测试需要 sandbox payment gateway")
	}
	notification.Signature = gateway.sign(notification)

	var wg sync.WaitGroup
	callbackErr := make(chan error, 1)
	timeoutErr := make(chan error, 1)
	wg.Add(2)
	go func() {
		defer wg.Done()
		callbackErr <- env.svc.HandlePaymentCallback(ctx, notification)
	}()
	go func() {
		defer wg.Done()
		timeoutErr <- env.svc.cancelPendingPaymentOnly(ctx, user.ID, receipt.OrderID, "并发超时")
	}()
	wg.Wait()
	_ = <-callbackErr
	_ = <-timeoutErr

	var order models.TicketOrder
	if err := env.db.First(&order, receipt.OrderID).Error; err != nil {
		t.Fatalf("读取支付竞争订单失败: %v", err)
	}
	if order.Status != models.TicketOrderStatusPaid && order.Status != models.TicketOrderStatusCancelled {
		t.Fatalf("支付/超时竞争后订单状态 = %s，不是 paid/cancelled", order.Status)
	}
}
