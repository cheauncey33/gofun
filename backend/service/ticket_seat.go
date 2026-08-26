package service

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"gofun/models"

	"gorm.io/gorm"
)

type FlexibleID int64

func (id *FlexibleID) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		*id = 0
		return nil
	}
	if strings.HasPrefix(raw, `"`) {
		unquoted, err := strconv.Unquote(raw)
		if err != nil {
			return err
		}
		raw = unquoted
	}
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || value <= 0 {
		return fmt.Errorf("无效座位 ID")
	}
	*id = FlexibleID(value)
	return nil
}

func parseSeatIDs(ids []FlexibleID) ([]int64, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("%w: 选座购票必须选择座位", ErrInvalidTicketCatalog)
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		value := int64(id)
		if value <= 0 {
			return nil, fmt.Errorf("%w: 座位 ID 无效", ErrInvalidTicketCatalog)
		}
		if _, ok := seen[value]; ok {
			return nil, fmt.Errorf("%w: 不能重复选择同一座位", ErrInvalidTicketCatalog)
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func seatRowLabel(row int) string {
	if row < 1 {
		return "A"
	}
	n := row
	var letters []byte
	for n > 0 {
		n--
		letters = append([]byte{byte('A' + n%26)}, letters...)
		n /= 26
	}
	return string(letters)
}

func defaultSeatLabel(row, col int) string {
	return fmt.Sprintf("%s%d", seatRowLabel(row), col)
}

func normalizeSeatLabel(raw string, row, col int) string {
	label := strings.TrimSpace(raw)
	if label == "" {
		return defaultSeatLabel(row, col)
	}
	if utf8.RuneCountInString(label) > 16 {
		runes := []rune(label)
		label = string(runes[:16])
	}
	return label
}

func seatedTicketTierIDs(tx *gorm.DB) (map[int64]struct{}, error) {
	var ids []int64
	err := tx.Table("ticket_tier").
		Select("ticket_tier.id").
		Joins("JOIN event_session ON event_session.id = ticket_tier.session_id AND event_session.delete_time IS NULL").
		Joins("JOIN event ON event.id = event_session.event_id AND event.delete_time IS NULL").
		Where("ticket_tier.delete_time IS NULL").
		Where("event.sale_mode = ?", models.EventSaleModeSeated).
		Pluck("ticket_tier.id", &ids).Error
	if err != nil {
		return nil, err
	}
	out := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out, nil
}

func sessionSeatsHeldOrSold(tx *gorm.DB, orderID int64) (int64, error) {
	var count int64
	err := tx.Model(&models.SessionSeat{}).
		Where("order_id = ? AND status IN ?", orderID, []models.SessionSeatStatus{
			models.SessionSeatHeld, models.SessionSeatSold,
		}).Count(&count).Error
	return count, err
}

func holdSessionSeats(tx *gorm.DB, order *models.TicketOrder, seatIDs []int64) error {
	if order == nil || len(order.Items) == 0 || len(seatIDs) == 0 {
		return fmt.Errorf("%w: 选座参数不完整", ErrInvalidTicketCatalog)
	}
	want := 0
	wantByTier := map[int64]int{}
	for _, item := range order.Items {
		want += item.Quantity
		wantByTier[item.TicketTierID] += item.Quantity
	}
	if want != len(seatIDs) {
		return fmt.Errorf("%w: 座位数必须与购票数量一致", ErrInvalidTicketCatalog)
	}
	result := tx.Model(&models.SessionSeat{}).
		Where(
			"id IN ? AND session_id = ? AND status = ?",
			seatIDs, order.SessionID, models.SessionSeatAvailable,
		).
		Updates(map[string]interface{}{
			"status":   models.SessionSeatHeld,
			"order_id": order.ID,
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(seatIDs)) {
		return fmt.Errorf("%w: 座位已被占用或不可购买", ErrTicketQuotaInsufficient)
	}
	var held []models.SessionSeat
	if err := tx.Select("ticket_tier_id").Where("order_id = ?", order.ID).Find(&held).Error; err != nil {
		return err
	}
	gotByTier := map[int64]int{}
	for _, seat := range held {
		gotByTier[seat.TicketTierID]++
	}
	for tierID, qty := range wantByTier {
		if gotByTier[tierID] != qty {
			return fmt.Errorf("%w: 座位价区与订单明细不一致", ErrInvalidTicketCatalog)
		}
		if err := adjustSeatedTierQuota(tx, tierID, -qty, 0); err != nil {
			return err
		}
	}
	return nil
}

func markSessionSeatsSold(tx *gorm.DB, orderID int64) error {
	var seats []models.SessionSeat
	if err := tx.Select("id", "ticket_tier_id").
		Where("order_id = ? AND status = ?", orderID, models.SessionSeatHeld).
		Order("id ASC").Find(&seats).Error; err != nil {
		return err
	}
	if len(seats) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(seats))
	for _, seat := range seats {
		ids = append(ids, seat.ID)
	}
	result := tx.Model(&models.SessionSeat{}).
		Where("id IN ? AND order_id = ? AND status = ?", ids, orderID, models.SessionSeatHeld).
		Update("status", models.SessionSeatSold)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != int64(len(ids)) {
		return fmt.Errorf("%w: 座位占用状态异常", ErrTicketOrderState)
	}
	soldByTier := map[int64]int{}
	for _, seat := range seats {
		soldByTier[seat.TicketTierID]++
	}
	for tierID, count := range soldByTier {
		if err := adjustSeatedTierQuota(tx, tierID, 0, count); err != nil {
			return err
		}
	}
	return nil
}

func releaseSessionSeats(tx *gorm.DB, orderID int64) (int, error) {
	var seats []models.SessionSeat
	if err := tx.Select("id", "ticket_tier_id", "status").
		Where("order_id = ? AND status IN ?", orderID, []models.SessionSeatStatus{
			models.SessionSeatHeld, models.SessionSeatSold,
		}).
		Order("id ASC").Find(&seats).Error; err != nil {
		return 0, err
	}
	if len(seats) == 0 {
		return 0, nil
	}
	ids := make([]int64, 0, len(seats))
	soldByTier := map[int64]int{}
	heldByTier := map[int64]int{}
	for _, seat := range seats {
		ids = append(ids, seat.ID)
		if seat.Status == models.SessionSeatSold {
			soldByTier[seat.TicketTierID]++
		} else {
			heldByTier[seat.TicketTierID]++
		}
	}
	result := tx.Model(&models.SessionSeat{}).
		Where("id IN ? AND order_id = ?", ids, orderID).
		Updates(map[string]interface{}{
			"status":   models.SessionSeatAvailable,
			"order_id": nil,
		})
	if result.Error != nil {
		return 0, result.Error
	}
	if result.RowsAffected != int64(len(ids)) {
		return 0, fmt.Errorf("%w: 释放座位失败", ErrTicketOrderState)
	}
	for tierID, count := range heldByTier {
		if err := adjustSeatedTierQuota(tx, tierID, count, 0); err != nil {
			return 0, err
		}
	}
	for tierID, count := range soldByTier {
		if err := adjustSeatedTierQuota(tx, tierID, count, -count); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func adjustSeatedTierQuota(tx *gorm.DB, tierID int64, remainingDelta, soldDelta int) error {
	if remainingDelta == 0 && soldDelta == 0 {
		return nil
	}
	updates := map[string]interface{}{
		"version": gorm.Expr("version + 1"),
	}
	if remainingDelta != 0 {
		updates["remaining_quota"] = gorm.Expr("GREATEST(remaining_quota + ?, 0)", remainingDelta)
		if remainingDelta > 0 {
			updates["status"] = gorm.Expr(
				"CASE WHEN status = ? THEN ? ELSE status END",
				models.TicketTierStatusSoldOut,
				models.TicketTierStatusOnSale,
			)
		} else {
			updates["status"] = gorm.Expr(
				"CASE WHEN remaining_quota + ? <= 0 AND status = ? THEN ? ELSE status END",
				remainingDelta,
				models.TicketTierStatusOnSale,
				models.TicketTierStatusSoldOut,
			)
		}
	}
	if soldDelta != 0 {
		updates["sold_count"] = gorm.Expr("GREATEST(sold_count + ?, 0)", soldDelta)
	}
	return tx.Model(&models.TicketTier{}).Where("id = ?", tierID).Updates(updates).Error
}

func seatedPlaceLabels(tx *gorm.DB, order *models.TicketOrder) (map[string]string, error) {
	if order == nil {
		return nil, fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
	}
	var seats []models.SessionSeat
	if err := tx.Preload("Seat").
		Where("order_id = ? AND status IN ?", order.ID, []models.SessionSeatStatus{
			models.SessionSeatHeld, models.SessionSeatSold,
		}).
		Order("id ASC").Find(&seats).Error; err != nil {
		return nil, err
	}
	pool := map[int64][]string{}
	for _, seat := range seats {
		pool[seat.TicketTierID] = append(pool[seat.TicketTierID], seat.Seat.Label)
	}
	labels := make(map[string]string, len(seats))
	for _, item := range order.Items {
		names := pool[item.TicketTierID]
		if len(names) < item.Quantity {
			return nil, fmt.Errorf("%w: 座位与票档数量不一致", ErrTicketOrderNonRetryable)
		}
		for sequence := 1; sequence <= item.Quantity; sequence++ {
			labels[placeLabelKey(item.ID, sequence)] = names[sequence-1]
		}
		pool[item.TicketTierID] = names[item.Quantity:]
	}
	return labels, nil
}
