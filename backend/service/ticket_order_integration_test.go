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

// newOrderIntegrationEnv 装配一个无 MQ 的 TicketOrderService：
// sync outbox 模式让「订单 + Outbox 行」在同一事务落库，无需 RabbitMQ。
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
	svc.ConfigureOutboxWriter(config.OrderOutboxConfig{WriteMode: "sync"})

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
				TicketTierID:     tier.ID,
				Quantity:         1,
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
