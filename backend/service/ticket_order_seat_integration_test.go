//go:build integration

package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"gofun/container"
	"gofun/models"
	"gofun/pkg/ws"
	"gofun/repository"
)

type recordingOrderEvents struct {
	mu     sync.Mutex
	events []ws.Event
}

func (r *recordingOrderEvents) Publish(_ int64, event ws.Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (r *recordingOrderEvents) names() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.events))
	for _, event := range r.events {
		out = append(out, event.Event)
	}
	return out
}

type seatedFixture struct {
	event    models.Event
	session  models.EventSession
	vipTier  models.TicketTier
	stdTier  models.TicketTier
	vipSeat  models.SessionSeat
	stdSeatA models.SessionSeat
	stdSeatB models.SessionSeat
	catalog  *TicketCatalogService
}

func flexSeatIDs(ids ...int64) []FlexibleID {
	out := make([]FlexibleID, len(ids))
	for i, id := range ids {
		out[i] = FlexibleID(id)
	}
	return out
}

func (e *orderIntegrationEnv) seedSeatedEvent(t *testing.T) *seatedFixture {
	t.Helper()
	now := time.Now()
	org := models.Organizer{
		Name: "选座主办方", Slug: fmt.Sprintf("seat-org-%d", now.UnixNano()),
		Status: models.OrganizerStatusActive, AuditStatus: models.AuditStatusApproved,
	}
	if err := e.db.Create(&org).Error; err != nil {
		t.Fatalf("create organizer: %v", err)
	}
	venue := models.Venue{OrganizerID: org.ID, Name: "选座场馆", City: "武汉", Address: "测试路 1 号"}
	if err := e.db.Create(&venue).Error; err != nil {
		t.Fatalf("create venue: %v", err)
	}
	event := models.Event{
		OrganizerID: org.ID, Title: "选座活动", Category: "livehouse",
		Status: models.EventStatusPublished, SaleMode: models.EventSaleModeSeated,
		RealNameRequired: false, MaxTicketsPerOrder: 4,
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
	vip := models.TicketTier{
		SessionID: session.ID, Name: "VIP", PriceCents: 19900,
		TotalQuota: 1, RemainingQuota: 1, PurchaseLimit: 4, Status: models.TicketTierStatusOnSale,
	}
	std := models.TicketTier{
		SessionID: session.ID, Name: "标准", PriceCents: 9900,
		TotalQuota: 2, RemainingQuota: 2, PurchaseLimit: 4, Status: models.TicketTierStatusOnSale,
	}
	if err := e.db.Create(&vip).Error; err != nil {
		t.Fatalf("create vip tier: %v", err)
	}
	if err := e.db.Create(&std).Error; err != nil {
		t.Fatalf("create std tier: %v", err)
	}
	layout := models.SeatLayout{EventID: &event.ID, Name: "测试厅", RowCount: 1, ColCount: 3}
	if err := e.db.Create(&layout).Error; err != nil {
		t.Fatalf("create layout: %v", err)
	}
	layoutSeats := []models.Seat{
		{LayoutID: layout.ID, TicketTierID: &vip.ID, RowNo: 1, ColNo: 1, Label: "A1"},
		{LayoutID: layout.ID, TicketTierID: &std.ID, RowNo: 1, ColNo: 2, Label: "A2"},
		{LayoutID: layout.ID, TicketTierID: &std.ID, RowNo: 1, ColNo: 3, Label: "A3"},
	}
	if err := e.db.Create(&layoutSeats).Error; err != nil {
		t.Fatalf("create layout seats: %v", err)
	}
	sessionSeats := []models.SessionSeat{
		{SessionID: session.ID, SeatID: layoutSeats[0].ID, TicketTierID: vip.ID, Status: models.SessionSeatAvailable},
		{SessionID: session.ID, SeatID: layoutSeats[1].ID, TicketTierID: std.ID, Status: models.SessionSeatAvailable},
		{SessionID: session.ID, SeatID: layoutSeats[2].ID, TicketTierID: std.ID, Status: models.SessionSeatAvailable},
	}
	if err := e.db.Create(&sessionSeats).Error; err != nil {
		t.Fatalf("create session seats: %v", err)
	}
	return &seatedFixture{
		event:    event,
		session:  session,
		vipTier:  vip,
		stdTier:  std,
		vipSeat:  sessionSeats[0],
		stdSeatA: sessionSeats[1],
		stdSeatB: sessionSeats[2],
		catalog: NewTicketCatalogService(&container.Container{
			DB:                e.db,
			RDB:               e.rdb,
			TicketCatalogRepo: repository.NewTicketCatalogRepository(e.db),
		}),
	}
}

func (e *orderIntegrationEnv) seatStatus(t *testing.T, seatID int64) models.SessionSeatStatus {
	t.Helper()
	var row models.SessionSeat
	if err := e.db.First(&row, seatID).Error; err != nil {
		t.Fatalf("read session seat %d: %v", seatID, err)
	}
	return row.Status
}

func (e *orderIntegrationEnv) createSeatedOrder(
	t *testing.T,
	userID int64,
	idempotencyKey string,
	tierID int64,
	seatIDs ...int64,
) *TicketOrderReceipt {
	t.Helper()
	receipt, err := e.svc.CreateOrder(context.Background(), userID, idempotencyKey, "seat-req", CreateTicketOrderInput{
		TicketTierID:      tierID,
		Quantity:          len(seatIDs),
		SeatIDs:           flexSeatIDs(seatIDs...),
		PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return receipt
}

func (e *orderIntegrationEnv) processSeated(
	t *testing.T,
	userID int64,
	receipt *TicketOrderReceipt,
	tierID int64,
	seatIDs []int64,
) {
	t.Helper()
	err := e.svc.ProcessOrderTask(context.Background(), TicketOrderMessage{
		EventID:      receipt.OrderID + 100,
		EventType:    ticketOrderFinalizeEventType,
		OrderID:      receipt.OrderID,
		UserID:       userID,
		TicketTierID: tierID,
		Quantity:     len(seatIDs),
		SeatIDs:      seatIDs,
	})
	if err != nil {
		t.Fatalf("ProcessOrderTask: %v", err)
	}
}

func (e *orderIntegrationEnv) paySeatedOrder(t *testing.T, userID, orderID int64) {
	t.Helper()
	ctx := context.Background()
	intent, err := e.svc.PayOrder(ctx, userID, orderID, "manual")
	if err != nil {
		t.Fatalf("PayOrder: %v", err)
	}
	gateway, ok := e.svc.payment.(*SandboxPaymentGateway)
	if !ok {
		t.Fatal("需要 sandbox payment gateway")
	}
	gateway.emitCallback(intent.PaymentNo, "success")
	var order models.TicketOrder
	if err := e.db.First(&order, orderID).Error; err != nil {
		t.Fatalf("read paid order: %v", err)
	}
	if order.Status != models.TicketOrderStatusPaid {
		t.Fatalf("支付后订单状态 = %s，应为 paid", order.Status)
	}
}

func TestIntegrationSeatedConcurrentHold(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	fx := env.seedSeatedEvent(t)
	userA := env.newUser(t, fmt.Sprintf("seat-a-%d", fx.stdSeatA.ID))
	userB := env.newUser(t, fmt.Sprintf("seat-b-%d", fx.stdSeatA.ID))

	var wg sync.WaitGroup
	type outcome struct {
		receipt *TicketOrderReceipt
		err     error
	}
	results := make([]outcome, 2)
	users := []*models.User{userA, userB}
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			receipt, err := env.svc.CreateOrder(
				context.Background(),
				users[idx].ID,
				fmt.Sprintf("seat-race-%d-%d", fx.stdSeatA.ID, idx),
				"seat-req",
				CreateTicketOrderInput{
					TicketTierID:      fx.stdTier.ID,
					Quantity:          1,
					SeatIDs:           flexSeatIDs(fx.stdSeatA.ID),
					PurchaseInfoInput: validPurchaseInfo(),
				},
			)
			results[idx] = outcome{receipt: receipt, err: err}
		}(i)
	}
	wg.Wait()

	okCount := 0
	failCount := 0
	for _, row := range results {
		if row.err == nil {
			okCount++
			continue
		}
		failCount++
		if !errors.Is(row.err, ErrTicketQuotaInsufficient) && !errors.Is(row.err, ErrTicketOrderUnavailable) {
			t.Fatalf("失败错误应为座位占用，得到 %v", row.err)
		}
	}
	if okCount != 1 || failCount != 1 {
		t.Fatalf("并发占座成功=%d 失败=%d，应为一成一败", okCount, failCount)
	}
	if env.seatStatus(t, fx.stdSeatA.ID) != models.SessionSeatHeld {
		t.Fatalf("胜出后座位状态 = %s，应为 held", env.seatStatus(t, fx.stdSeatA.ID))
	}
}

func TestIntegrationSeatedDisabledTierRejected(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	ctx := context.Background()
	fx := env.seedSeatedEvent(t)
	if err := env.db.Model(&models.TicketTier{}).Where("id = ?", fx.vipTier.ID).
		Update("status", models.TicketTierStatusDisabled).Error; err != nil {
		t.Fatalf("停售 VIP: %v", err)
	}

	views, err := fx.catalog.ListSessionSeats(ctx, fx.event.ID, fx.session.ID)
	if err != nil {
		t.Fatalf("ListSessionSeats: %v", err)
	}
	byID := map[int64]models.SessionSeatStatus{}
	for _, view := range views {
		byID[view.ID] = view.Status
	}
	if byID[fx.vipSeat.ID] != models.SessionSeatOffSale {
		t.Fatalf("停售分区座位列表状态 = %s，应为 off_sale", byID[fx.vipSeat.ID])
	}
	if byID[fx.stdSeatA.ID] != models.SessionSeatAvailable {
		t.Fatalf("在售座位被误标为 %s", byID[fx.stdSeatA.ID])
	}

	user := env.newUser(t, fmt.Sprintf("seat-mix-%d", fx.event.ID))
	_, err = env.svc.CreateOrder(ctx, user.ID, fmt.Sprintf("seat-mix-%d", fx.event.ID), "seat-req", CreateTicketOrderInput{
		TicketTierID:      fx.stdTier.ID,
		Quantity:          2,
		SeatIDs:           flexSeatIDs(fx.stdSeatA.ID, fx.vipSeat.ID),
		PurchaseInfoInput: validPurchaseInfo(),
	})
	if !errors.Is(err, ErrTicketOrderUnavailable) {
		t.Fatalf("混合停售座位应整单失败，得到 %v", err)
	}
	if env.seatStatus(t, fx.vipSeat.ID) != models.SessionSeatAvailable {
		t.Fatalf("失败后 VIP 座位应变回 available，得到 %s", env.seatStatus(t, fx.vipSeat.ID))
	}
	if env.seatStatus(t, fx.stdSeatA.ID) != models.SessionSeatAvailable {
		t.Fatalf("失败后标准座位应变回 available，得到 %s", env.seatStatus(t, fx.stdSeatA.ID))
	}
}

func TestIntegrationSeatedCancelRestoresSeatAndPublishes(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	events := &recordingOrderEvents{}
	env.svc.orderEvents = events
	fx := env.seedSeatedEvent(t)
	user := env.newUser(t, fmt.Sprintf("seat-cancel-%d", fx.stdSeatA.ID))
	receipt := env.createSeatedOrder(t, user.ID, fmt.Sprintf("seat-cancel-%d", fx.stdSeatA.ID), fx.stdTier.ID, fx.stdSeatA.ID)
	env.processSeated(t, user.ID, receipt, fx.stdTier.ID, []int64{fx.stdSeatA.ID})
	if env.seatStatus(t, fx.stdSeatA.ID) != models.SessionSeatHeld {
		t.Fatalf("待支付前座位应为 held，得到 %s", env.seatStatus(t, fx.stdSeatA.ID))
	}
	before := env.userCacheVersion(t, user.ID)
	if err := env.svc.CancelOrder(context.Background(), user.ID, receipt.OrderID, "用户取消"); err != nil {
		t.Fatalf("CancelOrder: %v", err)
	}
	if env.seatStatus(t, fx.stdSeatA.ID) != models.SessionSeatAvailable {
		t.Fatalf("取消后座位应为 available，得到 %s", env.seatStatus(t, fx.stdSeatA.ID))
	}
	if !containsEvent(events.names(), "cancelled") {
		t.Fatalf("选座取消应推 cancelled，得到 %v", events.names())
	}
	if env.userCacheVersion(t, user.ID) <= before {
		t.Fatal("选座取消应失效订单查询缓存")
	}
}

func TestIntegrationSeatedTimeoutRestoresSeatAndPublishes(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	events := &recordingOrderEvents{}
	env.svc.orderEvents = events
	fx := env.seedSeatedEvent(t)
	user := env.newUser(t, fmt.Sprintf("seat-timeout-%d", fx.stdSeatB.ID))
	receipt := env.createSeatedOrder(t, user.ID, fmt.Sprintf("seat-timeout-%d", fx.stdSeatB.ID), fx.stdTier.ID, fx.stdSeatB.ID)
	env.processSeated(t, user.ID, receipt, fx.stdTier.ID, []int64{fx.stdSeatB.ID})
	before := env.userCacheVersion(t, user.ID)
	if err := env.svc.cancelPendingPaymentOnly(context.Background(), user.ID, receipt.OrderID, "支付超时自动取消"); err != nil {
		t.Fatalf("timeout cancel: %v", err)
	}
	if env.seatStatus(t, fx.stdSeatB.ID) != models.SessionSeatAvailable {
		t.Fatalf("超时后座位应为 available，得到 %s", env.seatStatus(t, fx.stdSeatB.ID))
	}
	if !containsEvent(events.names(), "timeout_cancelled") {
		t.Fatalf("选座超时应推 timeout_cancelled，得到 %v", events.names())
	}
	if env.userCacheVersion(t, user.ID) <= before {
		t.Fatal("选座超时应失效订单查询缓存")
	}
}

func TestIntegrationSeatedPayAndRefund(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	fx := env.seedSeatedEvent(t)
	user := env.newUser(t, fmt.Sprintf("seat-pay-%d", fx.stdSeatA.ID))
	receipt := env.createSeatedOrder(t, user.ID, fmt.Sprintf("seat-pay-%d", fx.stdSeatA.ID), fx.stdTier.ID, fx.stdSeatA.ID)
	env.processSeated(t, user.ID, receipt, fx.stdTier.ID, []int64{fx.stdSeatA.ID})
	env.paySeatedOrder(t, user.ID, receipt.OrderID)
	if env.seatStatus(t, fx.stdSeatA.ID) != models.SessionSeatSold {
		t.Fatalf("支付后座位应为 sold，得到 %s", env.seatStatus(t, fx.stdSeatA.ID))
	}
	var tickets []models.AdmissionTicket
	if err := env.db.Where("order_id = ?", receipt.OrderID).Find(&tickets).Error; err != nil {
		t.Fatalf("read tickets: %v", err)
	}
	if len(tickets) != 1 || tickets[0].Status != models.AdmissionTicketStatusValid {
		t.Fatalf("支付后应签发 1 张有效电子票，得到 %+v", tickets)
	}

	if err := env.svc.CancelOrder(context.Background(), user.ID, receipt.OrderID, "申请退款"); err != nil {
		t.Fatalf("refund CancelOrder: %v", err)
	}
	if env.seatStatus(t, fx.stdSeatA.ID) != models.SessionSeatAvailable {
		t.Fatalf("退款后座位应为 available，得到 %s", env.seatStatus(t, fx.stdSeatA.ID))
	}
	if err := env.db.Where("order_id = ?", receipt.OrderID).Find(&tickets).Error; err != nil {
		t.Fatalf("read tickets after refund: %v", err)
	}
	if len(tickets) != 1 || tickets[0].Status != models.AdmissionTicketStatusRevoked {
		t.Fatalf("退款后电子票应作废，得到 %+v", tickets)
	}
}

func TestIntegrationSeatedIdempotentHold(t *testing.T) {
	env := newOrderIntegrationEnv(t)
	fx := env.seedSeatedEvent(t)
	user := env.newUser(t, fmt.Sprintf("seat-idem-%d", fx.vipSeat.ID))
	key := fmt.Sprintf("seat-idem-%d", fx.vipSeat.ID)
	first := env.createSeatedOrder(t, user.ID, key, fx.vipTier.ID, fx.vipSeat.ID)
	second, err := env.svc.CreateOrder(context.Background(), user.ID, key, "seat-req-2", CreateTicketOrderInput{
		TicketTierID:      fx.vipTier.ID,
		Quantity:          1,
		SeatIDs:           flexSeatIDs(fx.vipSeat.ID),
		PurchaseInfoInput: validPurchaseInfo(),
	})
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if second.OrderID != first.OrderID {
		t.Fatalf("同幂等键应复用订单 %d，得到 %d", first.OrderID, second.OrderID)
	}
	var held int64
	if err := env.db.Model(&models.SessionSeat{}).
		Where("id = ? AND status = ? AND order_id = ?", fx.vipSeat.ID, models.SessionSeatHeld, first.OrderID).
		Count(&held).Error; err != nil {
		t.Fatalf("count held: %v", err)
	}
	if held != 1 {
		t.Fatalf("同幂等键重试后占用行数 = %d，应为 1", held)
	}
}

func (e *orderIntegrationEnv) userCacheVersion(t *testing.T, userID int64) int64 {
	t.Helper()
	n, err := e.rdb.Get(context.Background(), orderUserVersionKey(userID)).Int64()
	if err != nil {
		return 0
	}
	return n
}

func containsEvent(names []string, want string) bool {
	for _, name := range names {
		if name == want {
			return true
		}
	}
	return false
}
