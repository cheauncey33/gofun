//go:build integration

package service

import (
	"context"
	"encoding/json"
	"errors"
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
	amqp "github.com/rabbitmq/amqp091-go"
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
	&models.TicketStockRecoveryFence{},
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

// Redis 已完成预扣但 HTTP 没拿到结果时，相同幂等键重试应复用原 order_id，不能再次扣库存。
func TestIntegrationRetryAfterUnknownRedisReserve(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 3, 3)
	user := env.newUser(t, fmt.Sprintf("unknown-redis-%d", tier.ID))
	idempotencyKey := fmt.Sprintf("idem-unknown-redis-%d", tier.ID)
	proposedOrderID := env.svc.node.Generate().Int64()

	reservation, code, err := env.svc.reserveTicketStock(
		ctx, user.ID, tier, 1, idempotencyKey, proposedOrderID,
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("initial Redis reserve code=%d err=%v", code, err)
	}

	receipt, err := env.svc.CreateOrder(ctx, user.ID, idempotencyKey, "req-retry", CreateTicketOrderInput{
		TicketTierID:      tier.ID,
		Quantity:          1,
		PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("retry CreateOrder: %v", err)
	}
	if receipt.OrderID != reservation.OrderID {
		t.Fatalf("retry order ID=%d, want reserved ID=%d", receipt.OrderID, reservation.OrderID)
	}
	stock, err := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int()
	if err != nil || stock != 2 {
		t.Fatalf("Redis stock after retry=%d err=%v, want 2", stock, err)
	}
	if state := env.mr.HGet(reservation.Key, "state"); state != stockReservationStateCommitted {
		t.Fatalf("reservation state=%q, want committed", state)
	}
	var orderFence models.TicketStockRecoveryFence
	if err := env.db.First(&orderFence, "order_id = ?", reservation.OrderID).Error; err != nil {
		t.Fatalf("read order fence: %v", err)
	}
	if orderFence.Owner != models.TicketStockRecoveryFenceOrder {
		t.Fatalf("fence owner=%q, want order", orderFence.Owner)
	}
}

// 无 MySQL 订单的超时 pending 凭证必须归还库存，并且重复恢复不能多归还。
func TestIntegrationRecoverOrphanStockReservation(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 2, 2)
	user := env.newUser(t, fmt.Sprintf("orphan-reserve-%d", tier.ID))

	reservation, code, err := env.svc.reserveTicketStock(
		ctx, user.ID, tier, 1, fmt.Sprintf("idem-orphan-%d", tier.ID), env.svc.node.Generate().Int64(),
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve orphan code=%d err=%v", code, err)
	}

	recovered, err := env.svc.RecoverStaleStockReservations(
		ctx, time.Now().Add(stockReservationRecoveryGrace),
	)
	if err != nil || recovered != 1 {
		t.Fatalf("recover orphan recovered=%d err=%v", recovered, err)
	}
	stock, err := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int()
	if err != nil || stock != 2 {
		t.Fatalf("Redis stock after recovery=%d err=%v, want 2", stock, err)
	}
	if env.mr.Exists(reservation.Key) {
		t.Fatal("orphan reservation should be deleted")
	}
	var recoveryFence models.TicketStockRecoveryFence
	if err := env.db.First(&recoveryFence, "order_id = ?", reservation.OrderID).Error; err != nil {
		t.Fatalf("read recovery fence: %v", err)
	}
	if recoveryFence.Owner != models.TicketStockRecoveryFenceRecovery {
		t.Fatalf("fence owner=%q, want recovery", recoveryFence.Owner)
	}
	recovered, err = env.svc.RecoverStaleStockReservations(
		ctx, time.Now().Add(stockReservationRecoveryGrace),
	)
	if err != nil || recovered != 0 {
		t.Fatalf("duplicate recovery recovered=%d err=%v", recovered, err)
	}
}

// 恢复任务在未提交订单上查不到记录时，会阻塞在同一个 order_id 栅栏上；
// 原事务提交后只能确认 Redis 预扣，不能再把库存加回。
func TestIntegrationRecoveryFenceWaitsForOrderCommit(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 2, 2)
	user := env.newUser(t, fmt.Sprintf("fence-race-%d", tier.ID))
	idempotencyKey := fmt.Sprintf("idem-fence-race-%d", tier.ID)

	reservation, code, err := env.svc.reserveTicketStock(
		ctx, user.ID, tier, 1, idempotencyKey, env.svc.node.Generate().Int64(),
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve code=%d err=%v", code, err)
	}
	record, err := env.svc.loadStockReservation(ctx, reservation.Key)
	if err != nil || record == nil {
		t.Fatalf("load reservation record=%#v err=%v", record, err)
	}

	var session models.EventSession
	if err := env.db.First(&session, tier.SessionID).Error; err != nil {
		t.Fatalf("load session: %v", err)
	}
	var event models.Event
	if err := env.db.First(&event, session.EventID).Error; err != nil {
		t.Fatalf("load event: %v", err)
	}
	now := time.Now()
	order := &models.TicketOrder{
		Base:                  models.Base{ID: reservation.OrderID},
		OrderNo:               strconv.FormatInt(reservation.OrderID, 10),
		UserID:                user.ID,
		OrganizerID:           event.OrganizerID,
		EventID:               event.ID,
		SessionID:             session.ID,
		OrderSource:           models.TicketOrderSourceNormal,
		Status:                models.TicketOrderStatusQueued,
		PaymentStatus:         models.PaymentStatusUnpaid,
		TotalAmountCents:      tier.PriceCents,
		ContactName:           "张三",
		ContactPhone:          "13800138000",
		PurchaseNoticeVersion: purchaseNoticeVersion,
		TermsAcceptedAt:       &now,
		IdempotencyKey:        idempotencyKey,
		ExpiresAt:             now.Add(15 * time.Minute),
	}

	tx := env.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		t.Fatalf("begin order tx: %v", tx.Error)
	}
	defer tx.Rollback()
	if err := tx.Create(&models.TicketStockRecoveryFence{
		OrderID: reservation.OrderID,
		Owner:   models.TicketStockRecoveryFenceOrder,
	}).Error; err != nil {
		t.Fatalf("insert uncommitted order fence: %v", err)
	}
	if err := tx.Create(order).Error; err != nil {
		t.Fatalf("insert uncommitted order: %v", err)
	}

	type recoveryResult struct {
		outcome stockReservationRecoveryOutcome
		err     error
	}
	resultCh := make(chan recoveryResult, 1)
	go func() {
		outcome, err := env.svc.claimStockReservationRecovery(ctx, *record)
		resultCh <- recoveryResult{outcome: outcome, err: err}
	}()
	select {
	case got := <-resultCh:
		t.Fatalf("recovery returned before order transaction ended: %#v", got)
	case <-time.After(100 * time.Millisecond):
	}
	if err := tx.Commit().Error; err != nil {
		t.Fatalf("commit order tx: %v", err)
	}

	select {
	case got := <-resultCh:
		if got.err != nil || got.outcome != stockReservationRecoveryConfirmed {
			t.Fatalf("recovery after commit=%#v", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("recovery did not finish after order commit")
	}
	stock, err := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int()
	if err != nil || stock != 1 {
		t.Fatalf("Redis stock after race=%d err=%v, want 1", stock, err)
	}
}

func TestIntegrationFaultAfterRedisReserveIsRolledBack(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 2, 2)
	user := env.newUser(t, fmt.Sprintf("fault-after-redis-%d", tier.ID))
	idempotencyKey := fmt.Sprintf("fault-after-redis-%d", tier.ID)

	env.svc.faultInjector = TicketFaultInjectorFunc(func(
		_ context.Context, point TicketFaultPoint, _ int64,
	) error {
		if point == FaultAfterRedisReserve {
			return errors.New("stop after Redis reserve")
		}
		return nil
	})
	if _, err := env.svc.CreateOrder(ctx, user.ID, idempotencyKey, "fault", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 1, PurchaseInfoInput: validPurchaseInfo(),
	}); err == nil {
		t.Fatal("expected injected fault after Redis reserve")
	}
	env.svc.faultInjector = nil

	reservationKey := stockReservationKey(user.ID, idempotencyKey)
	if !env.mr.Exists(reservationKey) {
		t.Fatal("pending Redis reservation should remain after interruption")
	}
	if stock, _ := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int(); stock != 1 {
		t.Fatalf("stock after interruption=%d, want 1", stock)
	}
	if recovered, err := env.svc.RecoverStaleStockReservations(
		ctx, time.Now().Add(stockReservationRecoveryGrace),
	); err != nil || recovered != 1 {
		t.Fatalf("recover interrupted reserve=%d err=%v", recovered, err)
	}
	if stock, _ := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int(); stock != 2 {
		t.Fatalf("stock after recovery=%d, want 2", stock)
	}
}

func TestIntegrationFaultAfterOrderCommitIsConfirmed(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 2, 2)
	user := env.newUser(t, fmt.Sprintf("fault-after-commit-%d", tier.ID))
	idempotencyKey := fmt.Sprintf("fault-after-commit-%d", tier.ID)

	env.svc.faultInjector = TicketFaultInjectorFunc(func(
		_ context.Context, point TicketFaultPoint, _ int64,
	) error {
		if point == FaultAfterOrderCommit {
			return errors.New("stop after order commit")
		}
		return nil
	})
	if _, err := env.svc.CreateOrder(ctx, user.ID, idempotencyKey, "fault", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 1, PurchaseInfoInput: validPurchaseInfo(),
	}); err == nil {
		t.Fatal("expected injected fault after order commit")
	}
	env.svc.faultInjector = nil

	var order models.TicketOrder
	if err := env.db.Where("user_id = ? AND idempotency_key = ?", user.ID, idempotencyKey).
		First(&order).Error; err != nil {
		t.Fatalf("committed order not found: %v", err)
	}
	reservationKey := stockReservationKey(user.ID, idempotencyKey)
	if state := env.mr.HGet(reservationKey, "state"); state != stockReservationStatePending {
		t.Fatalf("reservation state=%q, want pending", state)
	}
	if recovered, err := env.svc.RecoverStaleStockReservations(
		ctx, time.Now().Add(stockReservationRecoveryGrace),
	); err != nil || recovered != 1 {
		t.Fatalf("confirm committed reservation=%d err=%v", recovered, err)
	}
	if state := env.mr.HGet(reservationKey, "state"); state != stockReservationStateCommitted {
		t.Fatalf("reservation state after recovery=%q, want committed", state)
	}
	if stock, _ := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int(); stock != 1 {
		t.Fatalf("stock after committed recovery=%d, want 1", stock)
	}
}

func TestIntegrationConsumerCommitBeforeAckRedeliversIdempotently(t *testing.T) {
	rabbitURL := strings.TrimSpace(os.Getenv("GOFUN_TEST_RABBITMQ_URL"))
	if rabbitURL == "" {
		t.Skip("未设置 GOFUN_TEST_RABBITMQ_URL，跳过 RabbitMQ 故障测试")
	}
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 2, 2)
	user := env.newUser(t, fmt.Sprintf("ack-loss-%d", tier.ID))
	receipt, err := env.svc.CreateOrder(ctx, user.ID, fmt.Sprintf("ack-loss-%d", tier.ID), "fault", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 1, PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("create queued order: %v", err)
	}

	conn, err := amqp.Dial(rabbitURL)
	if err != nil {
		t.Fatalf("dial RabbitMQ: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	newChannel := func() (*amqp.Channel, error) { return conn.Channel() }
	setupChannel, err := newChannel()
	if err != nil {
		t.Fatalf("open setup channel: %v", err)
	}
	queueName := fmt.Sprintf("gofun.it.ack-loss.%d", receipt.OrderID)
	retryQueueName := queueName + ".retry"
	for _, name := range []string{queueName, retryQueueName} {
		if _, err := setupChannel.QueueDeclare(name, false, false, false, false, nil); err != nil {
			t.Fatalf("declare queue %s: %v", name, err)
		}
	}
	t.Cleanup(func() {
		cleanupChannel, openErr := newChannel()
		if openErr == nil {
			_, _ = cleanupChannel.QueueDelete(queueName, false, false, false)
			_, _ = cleanupChannel.QueueDelete(retryQueueName, false, false, false)
			_ = cleanupChannel.Close()
		}
	})
	message := TicketOrderMessage{
		OrderID: receipt.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 1,
	}
	body, _ := json.Marshal(message)
	if err := setupChannel.PublishWithContext(ctx, "", queueName, false, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, Body: body,
	}); err != nil {
		t.Fatalf("publish test order: %v", err)
	}
	_ = setupChannel.Close()

	consumer := NewTicketOrderConsumer(newChannel, queueName, retryQueueName, "", "", env.svc)
	attempts := 0
	secondAttempt := make(chan struct{}, 1)
	env.svc.faultInjector = TicketFaultInjectorFunc(func(
		_ context.Context, point TicketFaultPoint, _ int64,
	) error {
		if point != FaultAfterConsumerCommit {
			return nil
		}
		attempts++
		if attempts == 1 {
			return errors.New("drop before RabbitMQ ack")
		}
		select {
		case secondAttempt <- struct{}{}:
		default:
		}
		return nil
	})
	firstCtx, firstCancel := context.WithTimeout(ctx, 5*time.Second)
	firstErr := consumer.consume(firstCtx, 1, 1, 1)
	firstCancel()
	if firstErr == nil || !strings.Contains(firstErr.Error(), "drop before RabbitMQ ack") {
		t.Fatalf("first consume error=%v, want injected ack loss", firstErr)
	}

	secondCtx, secondCancel := context.WithCancel(ctx)
	secondDone := make(chan error, 1)
	go func() { secondDone <- consumer.consume(secondCtx, 2, 1, 1) }()
	select {
	case <-secondAttempt:
	case <-time.After(5 * time.Second):
		secondCancel()
		t.Fatal("RabbitMQ did not redeliver the unacked message")
	}
	time.Sleep(200 * time.Millisecond)
	secondCancel()
	if err := <-secondDone; err != nil {
		t.Fatalf("second consume: %v", err)
	}
	env.svc.faultInjector = nil

	var order models.TicketOrder
	if err := env.db.First(&order, receipt.OrderID).Error; err != nil {
		t.Fatalf("load redelivered order: %v", err)
	}
	if order.Status != models.TicketOrderStatusPendingPayment {
		t.Fatalf("order status=%s, want pending_payment", order.Status)
	}
	var tierAfter models.TicketTier
	if err := env.db.First(&tierAfter, tier.ID).Error; err != nil {
		t.Fatalf("load tier: %v", err)
	}
	if tierAfter.RemainingQuota != 1 {
		t.Fatalf("MySQL stock after redelivery=%d, want 1", tierAfter.RemainingQuota)
	}
	if attempts != 2 {
		t.Fatalf("consumer attempts=%d, want 2", attempts)
	}
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

// 用例 4：幂等缓存命中后必须回源刷新状态，客户端重试不能拿到过期的 queued 快照。
func TestIntegrationIdempotencyCacheRefreshesStatus(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()

	tier := env.seedPurchasableTier(t, 100, 6)
	user := env.newUser(t, fmt.Sprintf("idem-refresh-user-%d", tier.ID))
	idemKey := fmt.Sprintf("idem-refresh-%d", tier.ID)

	first, err := env.svc.CreateOrder(ctx, user.ID, idemKey, "req-1", CreateTicketOrderInput{
		TicketTierID: tier.ID, Quantity: 1, PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("首次下单失败: %v", err)
	}
	// 模拟极端场景：缓存快照仍是 queued，但订单真实状态已推进到 pending_payment。
	if err := env.rdb.Set(ctx, ticketIdempotencyRedisKey(user.ID, idemKey),
		fmt.Sprintf("%d|queued", first.OrderID), time.Minute).Err(); err != nil {
		t.Fatalf("预置过期缓存快照: %v", err)
	}
	if err := env.svc.ProcessOrderTask(ctx, TicketOrderMessage{
		OrderID: first.OrderID, UserID: user.ID, TicketTierID: tier.ID, Quantity: 1,
	}); err != nil {
		t.Fatalf("推进 pending_payment: %v", err)
	}

	receipt, err := env.svc.lookupIdempotentOrder(ctx, user.ID, idemKey)
	if err != nil {
		t.Fatalf("幂等回源查询失败: %v", err)
	}
	if receipt.OrderID != first.OrderID {
		t.Fatalf("幂等命中订单 %d，应为 %d", receipt.OrderID, first.OrderID)
	}
	if receipt.Status != models.TicketOrderStatusPendingPayment {
		t.Fatalf("幂等缓存命中返回状态 = %s，应为真实状态 pending_payment", receipt.Status)
	}
}

// 用例 5：WarmTicketQuota 只重建在售票档的 Redis 库存，已下架票档不会复活。
func TestIntegrationWarmTicketQuotaSkipsDisabledTiers(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()

	tier := env.seedPurchasableTier(t, 100, 6)
	var session models.EventSession
	if err := env.db.First(&session, tier.SessionID).Error; err != nil {
		t.Fatalf("读取测试场次失败: %v", err)
	}
	disabled := models.TicketTier{
		SessionID: session.ID, Name: "已下架票", PriceCents: 100,
		TotalQuota: 10, RemainingQuota: 10, Status: models.TicketTierStatusDisabled,
	}
	if err := env.db.Create(&disabled).Error; err != nil {
		t.Fatalf("创建下架票档失败: %v", err)
	}
	// 模拟取消前遗留的 Redis 库存 key。
	if err := env.rdb.Set(ctx, ticketStockKey(disabled.ID), 10, 0).Err(); err != nil {
		t.Fatalf("预置遗留 key: %v", err)
	}
	if err := env.svc.WarmTicketQuota(ctx); err != nil {
		t.Fatalf("预热失败: %v", err)
	}
	got, err := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int64()
	if err != nil || got != 100 {
		t.Fatalf("在售票档预热后 = %d, err=%v，应为 100", got, err)
	}
	// 删除下架票档 key 后再预热，确认不会被重建。
	if err := env.rdb.Del(ctx, ticketStockKey(disabled.ID)).Err(); err != nil {
		t.Fatalf("删除遗留 key: %v", err)
	}
	if err := env.svc.WarmTicketQuota(ctx); err != nil {
		t.Fatalf("二次预热失败: %v", err)
	}
	if _, err := env.rdb.Get(ctx, ticketStockKey(disabled.ID)).Int64(); !errors.Is(err, redis.Nil) {
		t.Fatalf("下架票档 key 不应被重建，got err=%v", err)
	}
}

func TestIntegrationWarmTicketQuotaSkipsPendingReservationKeys(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	tier := env.seedPurchasableTier(t, 5, 5)
	user := env.newUser(t, fmt.Sprintf("warm-skip-pending-%d", tier.ID))

	reservation, code, err := env.svc.reserveTicketStock(
		ctx, user.ID, tier, 1, fmt.Sprintf("idem-warm-skip-%d", tier.ID), env.svc.node.Generate().Int64(),
	)
	if err != nil || code != stockReservationCodeCreated {
		t.Fatalf("reserve code=%d err=%v", code, err)
	}
	if stock, _ := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int(); stock != 4 {
		t.Fatalf("stock after reserve=%d, want 4", stock)
	}

	if err := env.svc.WarmTicketQuota(ctx); err != nil {
		t.Fatalf("warm with pending reservation: %v", err)
	}
	if stock, _ := env.rdb.Get(ctx, ticketStockKey(tier.ID)).Int(); stock != 4 {
		t.Fatalf("warm overwrote pending stock to %d, want 4", stock)
	}
	if !env.mr.Exists(reservation.Key) {
		t.Fatal("pending reservation must remain after warm")
	}
}

// 用例 6：活动取消联动关闭抢票活动：状态置 cancelled、Redis 库存与本地缓存清理。
func TestIntegrationCancelEventCampaignsClosesRush(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()

	tier := env.seedPurchasableTier(t, 100, 6)
	var session models.EventSession
	if err := env.db.First(&session, tier.SessionID).Error; err != nil {
		t.Fatalf("读取测试场次失败: %v", err)
	}
	var event models.Event
	if err := env.db.First(&event, session.EventID).Error; err != nil {
		t.Fatalf("读取测试活动失败: %v", err)
	}
	campaign := models.RushSaleCampaign{
		OrganizerID: event.OrganizerID, TicketTierID: tier.ID,
		Name: "测试秒杀", RushPriceCents: 100,
		TotalQuota: 10, RemainingQuota: 10, PerUserLimit: 1,
		StartsAt: time.Now().Add(-time.Hour), EndsAt: time.Now().Add(time.Hour),
		Status: models.RushSaleStatusActive,
	}
	if err := env.db.Create(&campaign).Error; err != nil {
		t.Fatalf("创建抢票活动失败: %v", err)
	}
	if err := env.rdb.Set(ctx, rushStockKey(campaign.ID), 10, 0).Err(); err != nil {
		t.Fatalf("预热抢票库存: %v", err)
	}
	cache := gocache.New(time.Minute, time.Minute)
	cache.Set(rushCampaignLocalKey(campaign.ID), campaign, time.Minute)
	rush := &RushSaleService{
		db: env.db, rdb: env.rdb, order: env.svc, localCache: cache,
	}
	if err := rush.CancelEventCampaigns(ctx, event.ID); err != nil {
		t.Fatalf("取消活动联动失败: %v", err)
	}

	var after models.RushSaleCampaign
	if err := env.db.First(&after, campaign.ID).Error; err != nil {
		t.Fatalf("读取抢票活动失败: %v", err)
	}
	if after.Status != models.RushSaleStatusCancelled {
		t.Fatalf("抢票活动状态 = %s，应为 cancelled", after.Status)
	}
	if _, err := env.rdb.Get(ctx, rushStockKey(campaign.ID)).Int64(); !errors.Is(err, redis.Nil) {
		t.Fatalf("抢票库存 key 应被删除，got err=%v", err)
	}
	if _, found := cache.Get(rushCampaignLocalKey(campaign.ID)); found {
		t.Fatal("抢票活动本地缓存应被清理")
	}
	// 无关联抢票活动的活动取消不应报错。
	if err := rush.CancelEventCampaigns(ctx, 999999999); err != nil {
		t.Fatalf("无关联活动取消联动报错: %v", err)
	}
}
