package service

import (
	"context"
	"errors"
	"fmt"
	"gofun/models"
	"log"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrWaitlistNotFound    = errors.New("候补单不存在")
	ErrWaitlistUnavailable = errors.New("当前票档不接受候补")
	ErrWaitlistState       = errors.New("当前候补状态不允许此操作")
	ErrWaitlistSeated      = errors.New("选座活动暂不支持候补")
)

const waitlistWorkerInterval = 15 * time.Second

type WaitlistReceipt struct {
	WaitlistID int64                 `json:"waitlist_id,string"`
	WaitlistNo string                `json:"waitlist_no"`
	Status     models.WaitlistStatus `json:"status"`
}

// publicRedisExpected 是公开售卖 Redis 应对齐的安全可用量。
// remaining 含待派发；waitlistPending 必须剔除，否则对账会把候补票补回公开池。
// 分桶时只要有待派发，整档公开 Redis 置 0，避免退票落到别的桶被公开买走。
func publicRedisExpected(remaining, waitlistPending, queued int, bucketsEnabled bool) int {
	if waitlistPending < 0 {
		waitlistPending = 0
	}
	if bucketsEnabled && waitlistPending > 0 {
		return 0
	}
	available := remaining - waitlistPending - queued
	if available < 0 {
		return 0
	}
	return available
}

// planWaitlistFulfillment 严格 FIFO：队头张数大于待派发就停，不跳过给后面的人。
// leftoverPublic 只在队列已空时才允许回到公开库存。
func planWaitlistFulfillment(pending int, quantities []int) (fulfilledIndexes []int, leftoverPublic int, keepEarmarked bool) {
	if pending < 0 {
		pending = 0
	}
	for i, qty := range quantities {
		if qty <= 0 || qty > pending {
			return fulfilledIndexes, pending, true
		}
		fulfilledIndexes = append(fulfilledIndexes, i)
		pending -= qty
	}
	return fulfilledIndexes, pending, false
}

func shouldDivertReleasedQuota(queuedWaitlistCount int, rush, seatedSkipRedis bool) bool {
	return queuedWaitlistCount > 0 && !rush && !seatedSkipRedis
}

func (s *TicketOrderService) CreateWaitlist(
	ctx context.Context,
	userID int64,
	idempotencyKey, requestID string,
	input CreateTicketOrderInput,
) (*WaitlistReceipt, error) {
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if userID <= 0 || input.TicketTierID <= 0 ||
		len(idempotencyKey) < 8 || len(idempotencyKey) > 64 {
		return nil, ErrInvalidTicketCatalog
	}
	if receipt, err := s.lookupIdempotentWaitlist(ctx, userID, idempotencyKey); err != nil {
		return nil, err
	} else if receipt != nil {
		return receipt, nil
	}

	tier, session, event, venue, err := s.loadWaitlistableTier(ctx, input.TicketTierID)
	if err != nil {
		return nil, err
	}
	if event.SaleMode.IsSeated() {
		return nil, ErrWaitlistSeated
	}
	if len(input.SeatIDs) > 0 {
		return nil, fmt.Errorf("%w: 候补不能选座", ErrInvalidTicketCatalog)
	}
	if input.Quantity <= 0 {
		return nil, ErrInvalidTicketCatalog
	}
	if input.Quantity > event.MaxTicketsPerOrder {
		return nil, fmt.Errorf("%w: 超过账号限购", ErrWaitlistUnavailable)
	}
	if input.Quantity > tier.PurchaseLimit {
		return nil, fmt.Errorf("%w: 超过限购数量", ErrWaitlistUnavailable)
	}
	public := tier.RemainingQuota - tier.WaitlistPending
	if public > 0 {
		return nil, fmt.Errorf("%w: 仍有余票，请直接购买", ErrWaitlistUnavailable)
	}
	used, err := s.countUserEventTickets(ctx, userID, event.ID)
	if err != nil {
		return nil, err
	}
	waiting, err := s.countUserWaitlistTickets(ctx, userID, event.ID)
	if err != nil {
		return nil, err
	}
	if used+waiting+input.Quantity > event.MaxTicketsPerOrder {
		return nil, fmt.Errorf("%w: 本场每账号限购 %d 张", ErrWaitlistUnavailable, event.MaxTicketsPerOrder)
	}
	if err := s.expandAttendeeProfiles(ctx, userID, &input); err != nil {
		return nil, err
	}
	if err := validatePurchaseInfo(input.PurchaseInfoInput, input.Quantity, event.RealNameRequired); err != nil {
		return nil, err
	}
	if err := s.assertEventIdentitiesFree(ctx, event.ID, input.Attendees); err != nil {
		return nil, err
	}

	var active int64
	if err := s.db.WithContext(ctx).Model(&models.WaitlistEntry{}).
		Where("user_id = ? AND ticket_tier_id = ? AND status IN ?",
			userID, tier.ID, []models.WaitlistStatus{
				models.WaitlistStatusPendingPayment,
				models.WaitlistStatusQueued,
			}).Count(&active).Error; err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, fmt.Errorf("%w: 该票档已有进行中的候补", ErrWaitlistUnavailable)
	}

	waitlistID := s.node.Generate().Int64()
	now := time.Now()
	entry := &models.WaitlistEntry{
		Base:                    models.Base{ID: waitlistID},
		WaitlistNo:              "WL" + strconv.FormatInt(waitlistID, 10),
		UserID:                  userID,
		OrganizerID:             event.OrganizerID,
		EventID:                 event.ID,
		SessionID:               session.ID,
		TicketTierID:            tier.ID,
		Quantity:                input.Quantity,
		AmountCents:             tier.PriceCents * int64(input.Quantity),
		Status:                  models.WaitlistStatusPendingPayment,
		ContactName:             strings.TrimSpace(input.ContactName),
		ContactPhone:            strings.TrimSpace(input.ContactPhone),
		RealNameRequired:        event.RealNameRequired,
		PurchaseNoticeVersion:   purchaseNoticeVersion,
		EventTitleSnapshot:      event.Title,
		SessionStartsAtSnapshot: session.StartsAt,
		VenueNameSnapshot:       venue.Name,
		VenueAddressSnapshot:    venue.Address,
		TierNameSnapshot:        tier.Name,
		IdempotencyKey:          idempotencyKey,
		RequestID:               strings.TrimSpace(requestID),
		ExpiresAt:               now.Add(s.paymentTimeout),
	}
	if attendees := s.buildWaitlistAttendeeSnapshots(waitlistID, input.Attendees, event.RealNameRequired); len(attendees) > 0 {
		entry.Attendees = attendees
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(entry).Error; err != nil {
			return err
		}
		return bumpFunnelOrderDaily(
			tx, event.ID, event.OrganizerID, string(models.TicketOrderSourceWaitlist),
			1, 0, 0, now,
		)
	})
	if err != nil {
		if receipt, lookupErr := s.lookupIdempotentWaitlist(ctx, userID, idempotencyKey); lookupErr == nil && receipt != nil {
			return receipt, nil
		}
		return nil, err
	}
	bumpFunnelCacheVersion(ctx, s.rdb, event.OrganizerID)
	return &WaitlistReceipt{
		WaitlistID: entry.ID,
		WaitlistNo: entry.WaitlistNo,
		Status:     entry.Status,
	}, nil
}

func (s *TicketOrderService) GetWaitlist(
	ctx context.Context,
	userID, waitlistID int64,
) (*models.WaitlistEntry, error) {
	var entry models.WaitlistEntry
	if err := s.db.WithContext(ctx).Preload("Attendees").
		Where("id = ? AND user_id = ?", waitlistID, userID).
		First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWaitlistNotFound
		}
		return nil, err
	}
	s.stampWaitlistExpiry(&entry)
	entry.QueuePosition = s.waitlistQueuePosition(ctx, &entry)
	return &entry, nil
}

func (s *TicketOrderService) ListWaitlists(
	ctx context.Context,
	userID int64,
	page, pageSize int,
) ([]models.WaitlistEntry, int64, error) {
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 50 {
		pageSize = 20
	}
	query := s.db.WithContext(ctx).Model(&models.WaitlistEntry{}).Where("user_id = ?", userID)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []models.WaitlistEntry
	if err := query.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	for i := range list {
		s.stampWaitlistExpiry(&list[i])
		list[i].QueuePosition = s.waitlistQueuePosition(ctx, &list[i])
	}
	return list, total, nil
}

func (s *TicketOrderService) PayWaitlist(
	ctx context.Context,
	userID, waitlistID int64,
	scenario string,
) (*PaymentIntent, error) {
	var entry models.WaitlistEntry
	if err := s.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", waitlistID, userID).
		First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWaitlistNotFound
		}
		return nil, err
	}
	if entry.Status != models.WaitlistStatusPendingPayment || !s.waitlistPaymentWindowOpen(&entry) {
		return nil, ErrWaitlistState
	}

	var pending models.PaymentTransaction
	if err := s.db.WithContext(ctx).
		Where("waitlist_id = ? AND status = ?", waitlistID, models.PaymentTransactionPending).
		Order("id DESC").First(&pending).Error; err == nil {
		if err := s.restoreAndSchedulePayment(pending); err != nil {
			return nil, err
		}
		return &PaymentIntent{
			PaymentNo: pending.PaymentNo, Provider: pending.Provider,
			Status: string(pending.Status), AmountCents: pending.AmountCents,
			ExpiresAt: pending.ExpiresAt,
		}, nil
	}

	intent, err := s.payment.CreatePayment(ctx, PaymentCreateRequest{
		WaitlistID: waitlistID, UserID: userID, AmountCents: entry.AmountCents,
		ExpiresAt: entry.ExpiresAt, Scenario: scenario,
	})
	if err != nil {
		return nil, err
	}
	var scheduledPayment models.PaymentTransaction
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked models.WaitlistEntry
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", waitlistID, userID).First(&locked).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWaitlistNotFound
			}
			return err
		}
		if locked.Status != models.WaitlistStatusPendingPayment || !s.waitlistPaymentWindowOpen(&locked) {
			return ErrWaitlistState
		}
		var active models.PaymentTransaction
		if err := tx.Where("waitlist_id = ? AND status = ?", waitlistID, models.PaymentTransactionPending).
			First(&active).Error; err == nil {
			scheduledPayment = active
			intent.PaymentNo = active.PaymentNo
			intent.Provider = active.Provider
			intent.Status = string(active.Status)
			intent.AmountCents = active.AmountCents
			intent.ExpiresAt = active.ExpiresAt
			return nil
		}
		scheduledPayment = models.PaymentTransaction{
			PaymentNo: intent.PaymentNo, WaitlistID: waitlistID, UserID: userID,
			Provider: intent.Provider, ProviderPaymentID: intent.PaymentNo,
			AmountCents: intent.AmountCents, Status: models.PaymentTransactionPending,
			Scenario: normalizePaymentScenario(scenario), ExpiresAt: locked.ExpiresAt,
		}
		return tx.Create(&scheduledPayment).Error
	})
	if err != nil {
		return nil, err
	}
	if err := s.restoreAndSchedulePayment(scheduledPayment); err != nil {
		return nil, err
	}
	return intent, nil
}

func (s *TicketOrderService) CancelWaitlist(
	ctx context.Context,
	userID, waitlistID int64,
	reason string,
) error {
	var refundPaymentNo string
	var refundCents int64
	var tierID, organizerID int64
	queued := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var entry models.WaitlistEntry
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", waitlistID, userID).First(&entry).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWaitlistNotFound
			}
			return err
		}
		if entry.Status != models.WaitlistStatusPendingPayment &&
			entry.Status != models.WaitlistStatusQueued {
			return ErrWaitlistState
		}
		now := time.Now()
		reason = strings.TrimSpace(reason)
		if entry.Status == models.WaitlistStatusQueued {
			var payment models.PaymentTransaction
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("waitlist_id = ? AND status = ?", entry.ID, models.PaymentTransactionSuccess).
				Order("id DESC").First(&payment).Error; err != nil {
				return err
			}
			refundPaymentNo = payment.PaymentNo
			refundCents = payment.AmountCents
			queued = true
			tierID = entry.TicketTierID
			organizerID = entry.OrganizerID
		} else {
			_ = tx.Model(&models.PaymentTransaction{}).
				Where("waitlist_id = ? AND status = ?", entry.ID, models.PaymentTransactionPending).
				Update("status", models.PaymentTransactionClosed).Error
		}
		if err := tx.Model(&models.WaitlistEntry{}).Where("id = ?", entry.ID).
			Updates(map[string]interface{}{
				"status":        models.WaitlistStatusCancelled,
				"cancelled_at":  &now,
				"cancel_reason": reason,
			}).Error; err != nil {
			return err
		}
		if queued {
			return bumpFunnelOrderDaily(
				tx, entry.EventID, entry.OrganizerID, string(models.TicketOrderSourceWaitlist),
				0, 0, 1, now,
			)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if queued {
		bumpFunnelCacheVersion(ctx, s.rdb, organizerID)
	}
	if refundPaymentNo != "" {
		if err := s.payment.Refund(ctx, refundPaymentNo, refundCents); err != nil {
			return err
		}
		_ = s.db.WithContext(ctx).Model(&models.PaymentTransaction{}).
			Where("payment_no = ?", refundPaymentNo).
			Updates(map[string]interface{}{
				"status":      models.PaymentTransactionRefunded,
				"refunded_at": time.Now(),
			}).Error
	}
	if queued && tierID > 0 {
		_, _ = s.AllocateWaitlist(ctx, tierID)
	}
	return nil
}

func (s *TicketOrderService) divertReleasedQuotaToWaitlist(tx *gorm.DB, tierID int64, quantity int) (bool, error) {
	if tierID <= 0 || quantity <= 0 {
		return false, nil
	}
	var queued int64
	if err := tx.Model(&models.WaitlistEntry{}).
		Where("ticket_tier_id = ? AND status = ?", tierID, models.WaitlistStatusQueued).
		Count(&queued).Error; err != nil {
		return false, err
	}
	if queued == 0 {
		return false, nil
	}
	result := tx.Exec(`
		UPDATE ticket_tier
		SET waitlist_pending = waitlist_pending + ?,
		    status = CASE WHEN remaining_quota <= waitlist_pending + ? THEN ? ELSE status END,
		    version = version + 1
		WHERE id = ? AND delete_time IS NULL
	`, quantity, quantity, models.TicketTierStatusSoldOut, tierID)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func (s *TicketOrderService) maybeDivertRestoredQuota(
	tx *gorm.DB,
	order *models.TicketOrder,
	skipRedis bool,
) (bool, int64, error) {
	if skipRedis || order == nil || order.RushSaleCampaignID != nil || len(order.Items) != 1 {
		return skipRedis, 0, nil
	}
	diverted, err := s.divertReleasedQuotaToWaitlist(tx, order.Items[0].TicketTierID, order.Items[0].Quantity)
	if err != nil {
		return skipRedis, 0, err
	}
	if diverted {
		return true, order.Items[0].TicketTierID, nil
	}
	return skipRedis, 0, nil
}

// AllocateWaitlist 把待派发库存按 FIFO 派给已付款队头，队空了才把剩余回公开 Redis。
func (s *TicketOrderService) AllocateWaitlist(ctx context.Context, tierID int64) (int, error) {
	if tierID <= 0 {
		return 0, nil
	}
	var leftover int
	fulfilled := 0
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var tier models.TicketTier
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", tierID).First(&tier).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		pending := tier.WaitlistPending
		if pending <= 0 {
			return nil
		}
		var entries []models.WaitlistEntry
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Preload("Attendees").
			Where("ticket_tier_id = ? AND status = ?", tierID, models.WaitlistStatusQueued).
			Order("id ASC").Find(&entries).Error; err != nil {
			return err
		}
		quantities := make([]int, len(entries))
		for i, entry := range entries {
			quantities[i] = entry.Quantity
		}
		indexes, leftoverPublic, keep := planWaitlistFulfillment(pending, quantities)
		for _, index := range indexes {
			if err := s.fulfillWaitlistEntry(tx, &entries[index], &tier); err != nil {
				return err
			}
			pending -= entries[index].Quantity
			fulfilled++
		}
		updates := map[string]interface{}{
			"waitlist_pending": pending,
			"version":          gorm.Expr("version + 1"),
		}
		if keep {
			leftover = 0
		} else {
			leftover = leftoverPublic
			updates["waitlist_pending"] = 0
			if leftoverPublic == 0 && tier.RemainingQuota <= 0 {
				updates["status"] = models.TicketTierStatusSoldOut
			} else if leftoverPublic > 0 {
				updates["status"] = models.TicketTierStatusOnSale
			}
		}
		if err := tx.Model(&models.TicketTier{}).Where("id = ?", tierID).Updates(updates).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return fulfilled, err
	}
	if leftover > 0 {
		s.refreshPublicRedisForTier(ctx, tierID)
	}
	return fulfilled, nil
}

func deductWaitlistFromBuckets(tx *gorm.DB, tierID int64, quantity int) error {
	var buckets []models.TicketTierBucket
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tier_id = ?", tierID).Order("bucket_no").Find(&buckets).Error; err != nil {
		return err
	}
	left := quantity
	for i := range buckets {
		if left <= 0 {
			break
		}
		take := buckets[i].RemainingQuota
		if take > left {
			take = left
		}
		if take <= 0 {
			continue
		}
		if err := deductTierBucket(tx, tierID, buckets[i].BucketNo, take); err != nil {
			return err
		}
		left -= take
	}
	if left > 0 {
		return fmt.Errorf("候补派票分桶库存不足")
	}
	return nil
}

func (s *TicketOrderService) refreshPublicRedisForTier(ctx context.Context, tierID int64) {
	if s.rdb == nil {
		return
	}
	var tier models.TicketTier
	if err := s.db.WithContext(ctx).First(&tier, tierID).Error; err != nil {
		return
	}
	occ, err := loadQueuedStockOccupancy(ctx, s.db, s.inventory)
	if err != nil {
		return
	}
	if s.inventory.Enabled {
		var buckets []models.TicketTierBucket
		if err := s.db.WithContext(ctx).Where("tier_id = ?", tierID).Find(&buckets).Error; err != nil {
			return
		}
		for _, bucket := range buckets {
			key := fmt.Sprintf("%d:%d", bucket.TierID, bucket.BucketNo)
			expected := publicRedisExpected(
				bucket.RemainingQuota, tier.WaitlistPending, occ.byTierBucket[key], true,
			)
			_ = s.rdb.Set(ctx, TicketStockBucketKey(tierID, bucket.BucketNo), expected, 0).Err()
		}
		return
	}
	expected := publicRedisExpected(tier.RemainingQuota, tier.WaitlistPending, occ.byTier[tierID], false)
	_ = s.rdb.Set(ctx, ticketStockKey(tierID), expected, 0).Err()
}

func (s *TicketOrderService) fulfillWaitlistEntry(
	tx *gorm.DB,
	entry *models.WaitlistEntry,
	tier *models.TicketTier,
) error {
	now := time.Now()
	orderID := s.node.Generate().Int64()
	itemID := s.node.Generate().Int64()
	order := models.TicketOrder{
		Base:                  models.Base{ID: orderID},
		OrderNo:               "FC" + strconv.FormatInt(orderID, 10),
		UserID:                entry.UserID,
		OrganizerID:           entry.OrganizerID,
		EventID:               entry.EventID,
		SessionID:             entry.SessionID,
		OrderSource:           models.TicketOrderSourceWaitlist,
		Status:                models.TicketOrderStatusPaid,
		PaymentStatus:         models.PaymentStatusPaid,
		TotalAmountCents:      entry.AmountCents,
		ContactName:           entry.ContactName,
		ContactPhone:          entry.ContactPhone,
		RealNameRequired:      entry.RealNameRequired,
		PurchaseNoticeVersion: entry.PurchaseNoticeVersion,
		IdempotencyKey:        "waitlist:" + entry.WaitlistNo,
		RequestID:             entry.RequestID,
		ExpiresAt:             now,
		PaidAt:                &now,
		Items: []models.TicketOrderItem{{
			Base:                    models.Base{ID: itemID},
			OrderID:                 orderID,
			TicketTierID:            entry.TicketTierID,
			Quantity:                entry.Quantity,
			UnitPriceCents:          tier.PriceCents,
			EventTitleSnapshot:      entry.EventTitleSnapshot,
			SessionStartsAtSnapshot: entry.SessionStartsAtSnapshot,
			VenueNameSnapshot:       entry.VenueNameSnapshot,
			VenueAddressSnapshot:    entry.VenueAddressSnapshot,
			TierNameSnapshot:        entry.TierNameSnapshot,
		}},
	}
	for _, attendee := range entry.Attendees {
		order.Attendees = append(order.Attendees, models.TicketOrderAttendee{
			Base:           models.Base{ID: s.node.Generate().Int64()},
			OrderID:        orderID,
			SequenceNo:     attendee.SequenceNo,
			Name:           attendee.Name,
			IDType:         attendee.IDType,
			IDNumberMasked: attendee.IDNumberMasked,
			IDNumberHash:   attendee.IDNumberHash,
			IdentityKey:    attendee.IdentityKey,
		})
	}
	if err := tx.Create(&order).Error; err != nil {
		return err
	}
	if err := tx.Where("order_id = ?", orderID).Find(&order.Items).Error; err != nil {
		return err
	}
	if err := s.issueAdmissionTickets(tx, &order, now); err != nil {
		return err
	}
	if s.inventory.Enabled {
		if err := deductWaitlistFromBuckets(tx, tier.ID, entry.Quantity); err != nil {
			return err
		}
	} else {
		result := tx.Model(&models.TicketTier{}).
			Where("id = ? AND remaining_quota >= ? AND waitlist_pending >= ?",
				tier.ID, entry.Quantity, entry.Quantity).
			Updates(map[string]interface{}{
				"remaining_quota": gorm.Expr("remaining_quota - ?", entry.Quantity),
				"sold_count":      gorm.Expr("sold_count + ?", entry.Quantity),
				"version":         gorm.Expr("version + 1"),
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("候补派票库存不足")
		}
		tier.RemainingQuota -= entry.Quantity
	}
	if err := tx.Model(&models.WaitlistEntry{}).Where("id = ? AND status = ?", entry.ID, models.WaitlistStatusQueued).
		Updates(map[string]interface{}{
			"status":             models.WaitlistStatusFulfilled,
			"fulfilled_order_id": orderID,
		}).Error; err != nil {
		return err
	}
	return tx.Model(&models.PaymentTransaction{}).
		Where("waitlist_id = ? AND status = ?", entry.ID, models.PaymentTransactionSuccess).
		Update("order_id", orderID).Error
}

func (s *TicketOrderService) applyWaitlistPaymentInTx(
	tx *gorm.DB,
	payment models.PaymentTransaction,
	notification PaymentNotification,
	callbackID int64,
) (queued bool, userID, waitlistID int64, eventName, eventMessage string, tierID, organizerID int64, err error) {
	var entry models.WaitlistEntry
	if err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("id = ?", payment.WaitlistID).First(&entry).Error; err != nil {
		return
	}
	now := time.Now()
	if notification.Status != "success" {
		reason := notification.FailureReason
		if reason == "" {
			reason = "支付未完成"
		}
		if err = tx.Model(&models.PaymentTransaction{}).Where("id = ?", payment.ID).
			Updates(map[string]interface{}{
				"status":         models.PaymentTransactionFailed,
				"failure_reason": reason,
			}).Error; err != nil {
			return
		}
		if err = tx.Model(&models.WaitlistEntry{}).
			Where("id = ? AND status = ?", entry.ID, models.WaitlistStatusPendingPayment).
			Updates(map[string]interface{}{
				"status":        models.WaitlistStatusCancelled,
				"cancelled_at":  &now,
				"cancel_reason": reason,
			}).Error; err != nil {
			return
		}
		err = tx.Model(&models.PaymentCallback{}).Where("id = ?", callbackID).
			Update("processed_at", &now).Error
		userID, waitlistID = entry.UserID, entry.ID
		eventName, eventMessage = "payment_failed", "候补支付未完成"
		return
	}
	if entry.Status != models.WaitlistStatusPendingPayment || now.After(entry.ExpiresAt) {
		if err = tx.Model(&models.PaymentTransaction{}).Where("id = ?", payment.ID).
			Update("status", models.PaymentTransactionClosed).Error; err != nil {
			return
		}
		err = tx.Model(&models.PaymentCallback{}).Where("id = ?", callbackID).
			Update("processed_at", &now).Error
		return
	}
	if err = tx.Model(&models.WaitlistEntry{}).
		Where("id = ? AND status = ?", entry.ID, models.WaitlistStatusPendingPayment).
		Updates(map[string]interface{}{
			"status":  models.WaitlistStatusQueued,
			"paid_at": &now,
		}).Error; err != nil {
		return
	}
	if err = tx.Model(&models.PaymentTransaction{}).Where("id = ?", payment.ID).
		Updates(map[string]interface{}{
			"status":  models.PaymentTransactionSuccess,
			"paid_at": &now,
		}).Error; err != nil {
		return
	}
	if err = tx.Model(&models.PaymentCallback{}).Where("id = ?", callbackID).
		Update("processed_at", &now).Error; err != nil {
		return
	}
	if err = bumpFunnelOrderDaily(
		tx, entry.EventID, entry.OrganizerID, string(models.TicketOrderSourceWaitlist),
		0, 1, 0, now,
	); err != nil {
		return
	}
	queued = true
	userID, waitlistID, tierID, organizerID = entry.UserID, entry.ID, entry.TicketTierID, entry.OrganizerID
	eventName, eventMessage = "waitlist_queued", "候补已付款，按提交顺序排队，有退票将派给你"
	return
}

func (s *TicketOrderService) StartWaitlistWorker(ctx context.Context) {
	ticker := time.NewTicker(waitlistWorkerInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.ExpireWaitlists(ctx); err != nil {
				log.Printf("候补过期处理失败: %v", err)
			}
			if err := s.AllocateAllWaitlists(ctx); err != nil {
				log.Printf("候补派票失败: %v", err)
			}
		}
	}
}

func (s *TicketOrderService) ExpireWaitlists(ctx context.Context) error {
	now := time.Now()
	var pending []models.WaitlistEntry
	if err := s.db.WithContext(ctx).
		Where("status = ? AND expires_at <= ?", models.WaitlistStatusPendingPayment, now).
		Find(&pending).Error; err != nil {
		return err
	}
	for _, entry := range pending {
		_ = s.CancelWaitlist(ctx, entry.UserID, entry.ID, "候补支付超时")
	}

	var queued []models.WaitlistEntry
	if err := s.db.WithContext(ctx).
		Where("status = ?", models.WaitlistStatusQueued).
		Find(&queued).Error; err != nil {
		return err
	}
	for _, entry := range queued {
		var session models.EventSession
		if err := s.db.WithContext(ctx).Select("id", "starts_at").
			First(&session, entry.SessionID).Error; err != nil {
			continue
		}
		if now.Before(session.StartsAt) {
			continue
		}
		if err := s.expireQueuedWaitlist(ctx, &entry, "开场前未配到票，候补已截止"); err != nil {
			log.Printf("候补截止退款失败 waitlist=%d: %v", entry.ID, err)
		}
	}
	return nil
}

func (s *TicketOrderService) expireQueuedWaitlist(ctx context.Context, entry *models.WaitlistEntry, reason string) error {
	var payment models.PaymentTransaction
	if err := s.db.WithContext(ctx).
		Where("waitlist_id = ? AND status = ?", entry.ID, models.PaymentTransactionSuccess).
		Order("id DESC").First(&payment).Error; err != nil {
		return err
	}
	if err := s.payment.Refund(ctx, payment.PaymentNo, payment.AmountCents); err != nil {
		return err
	}
	now := time.Now()
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&models.PaymentTransaction{}).
			Where("id = ? AND status = ?", payment.ID, models.PaymentTransactionSuccess).
			Updates(map[string]interface{}{
				"status":      models.PaymentTransactionRefunded,
				"refunded_at": &now,
			}).Error; err != nil {
			return err
		}
		if err := tx.Model(&models.WaitlistEntry{}).
			Where("id = ? AND status = ?", entry.ID, models.WaitlistStatusQueued).
			Updates(map[string]interface{}{
				"status":        models.WaitlistStatusExpired,
				"cancelled_at":  &now,
				"cancel_reason": reason,
			}).Error; err != nil {
			return err
		}
		return bumpFunnelOrderDaily(
			tx, entry.EventID, entry.OrganizerID, string(models.TicketOrderSourceWaitlist),
			0, 0, 1, now,
		)
	})
	if err != nil {
		return err
	}
	bumpFunnelCacheVersion(ctx, s.rdb, entry.OrganizerID)
	_, err = s.AllocateWaitlist(ctx, entry.TicketTierID)
	return err
}

func (s *TicketOrderService) AllocateAllWaitlists(ctx context.Context) error {
	var tierIDs []int64
	if err := s.db.WithContext(ctx).Model(&models.TicketTier{}).
		Where("waitlist_pending > 0").Pluck("id", &tierIDs).Error; err != nil {
		return err
	}
	for _, tierID := range tierIDs {
		if _, err := s.AllocateWaitlist(ctx, tierID); err != nil {
			log.Printf("候补派票 tier=%d: %v", tierID, err)
		}
	}
	return nil
}

func (s *TicketOrderService) loadWaitlistableTier(
	ctx context.Context,
	tierID int64,
) (*models.TicketTier, *models.EventSession, *models.Event, *models.Venue, error) {
	var tier models.TicketTier
	if err := s.db.WithContext(ctx).First(&tier, tierID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, nil, nil, ErrWaitlistUnavailable
		}
		return nil, nil, nil, nil, err
	}
	var session models.EventSession
	if err := s.db.WithContext(ctx).First(&session, tier.SessionID).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	var event models.Event
	if err := s.db.WithContext(ctx).First(&event, session.EventID).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	var venue models.Venue
	if err := s.db.WithContext(ctx).First(&venue, session.VenueID).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	now := time.Now()
	if event.Status != models.EventStatusPublished ||
		session.Status != models.SessionStatusOnSale ||
		(tier.Status != models.TicketTierStatusOnSale && tier.Status != models.TicketTierStatusSoldOut) ||
		now.Before(session.SaleStartsAt) || now.After(session.SaleEndsAt) ||
		!now.Before(session.StartsAt) {
		return nil, nil, nil, nil, ErrWaitlistUnavailable
	}
	var rush int64
	if err := s.db.WithContext(ctx).Model(&models.RushSaleCampaign{}).
		Where("ticket_tier_id = ? AND status IN ?", tier.ID, []models.RushSaleStatus{
			models.RushSaleStatusScheduled,
			models.RushSaleStatusActive,
		}).Count(&rush).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	if rush > 0 {
		return nil, nil, nil, nil, fmt.Errorf("%w: 限时开售票档不接受候补", ErrWaitlistUnavailable)
	}
	return &tier, &session, &event, &venue, nil
}

func (s *TicketOrderService) lookupIdempotentWaitlist(
	ctx context.Context,
	userID int64,
	idempotencyKey string,
) (*WaitlistReceipt, error) {
	var entry models.WaitlistEntry
	if err := s.db.WithContext(ctx).
		Where("user_id = ? AND idempotency_key = ?", userID, idempotencyKey).
		First(&entry).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &WaitlistReceipt{
		WaitlistID: entry.ID,
		WaitlistNo: entry.WaitlistNo,
		Status:     entry.Status,
	}, nil
}

func (s *TicketOrderService) countUserWaitlistTickets(ctx context.Context, userID, eventID int64) (int, error) {
	var total int
	err := s.db.WithContext(ctx).Model(&models.WaitlistEntry{}).
		Where("user_id = ? AND event_id = ?", userID, eventID).
		Where("status IN ?", []models.WaitlistStatus{
			models.WaitlistStatusPendingPayment,
			models.WaitlistStatusQueued,
		}).
		Select("COALESCE(SUM(quantity), 0)").
		Scan(&total).Error
	return total, err
}

func (s *TicketOrderService) waitlistQueuePosition(ctx context.Context, entry *models.WaitlistEntry) int {
	if entry == nil || entry.Status != models.WaitlistStatusQueued {
		return 0
	}
	var ahead int64
	if err := s.db.WithContext(ctx).Model(&models.WaitlistEntry{}).
		Where("ticket_tier_id = ? AND status = ? AND id < ?",
			entry.TicketTierID, models.WaitlistStatusQueued, entry.ID).
		Count(&ahead).Error; err != nil {
		return 0
	}
	return int(ahead) + 1
}

func (s *TicketOrderService) waitlistPaymentWindowOpen(entry *models.WaitlistEntry) bool {
	return entry != nil &&
		entry.Status == models.WaitlistStatusPendingPayment &&
		time.Now().Before(entry.ExpiresAt)
}

func (s *TicketOrderService) stampWaitlistExpiry(entry *models.WaitlistEntry) {
	if entry == nil || entry.ExpiresAt.IsZero() {
		return
	}
	entry.ExpiresAtUnix = entry.ExpiresAt.Unix()
}

func (s *TicketOrderService) buildWaitlistAttendeeSnapshots(
	waitlistID int64,
	inputs []TicketAttendeeInput,
	realNameRequired bool,
) []models.WaitlistAttendee {
	if !realNameRequired {
		return nil
	}
	attendees := make([]models.WaitlistAttendee, 0, len(inputs))
	for index, input := range inputs {
		idNumber := strings.ToUpper(strings.TrimSpace(input.IDNumber))
		attendees = append(attendees, models.WaitlistAttendee{
			Base:           models.Base{ID: s.node.Generate().Int64()},
			WaitlistID:     waitlistID,
			SequenceNo:     index + 1,
			Name:           strings.TrimSpace(input.Name),
			IDType:         "id_card",
			IDNumberMasked: maskIDNumber(idNumber),
			IDNumberHash:   orderAttendeeHash(s.identityHashKey, waitlistID, idNumber),
			IdentityKey:    stableIdentityKey(s.identityHashKey, idNumber),
		})
	}
	return attendees
}
