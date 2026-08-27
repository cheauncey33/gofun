package service

import (
	"context"
	"fmt"
	"strings"

	"gofun/models"

	"gorm.io/gorm"
)

type SeatLayoutInput struct {
	Name     string          `json:"name"`
	RowCount int             `json:"row_count"`
	ColCount int             `json:"col_count"`
	Seats    []SeatCellInput `json:"seats"`
}

type SeatCellInput struct {
	TicketTierID FlexibleID `json:"ticket_tier_id"`
	RowNo        int        `json:"row_no"`
	ColNo        int        `json:"col_no"`
	Label        string     `json:"label"`
}

type SessionSeatView struct {
	ID           int64                    `json:"id,string"`
	SeatID       int64                    `json:"seat_id,string"`
	TicketTierID int64                    `json:"ticket_tier_id,string"`
	RowNo        int                      `json:"row_no"`
	ColNo        int                      `json:"col_no"`
	Label        string                   `json:"label"`
	Status       models.SessionSeatStatus `json:"status"`
}

func (s *TicketCatalogService) GetSeatLayout(
	ctx context.Context,
	userID, organizerID, eventID int64,
) (*models.SeatLayout, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	event, err := s.repo.FindEventByID(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	var layout models.SeatLayout
	err = s.db.WithContext(ctx).Preload("Seats").
		Where("event_id = ?", eventID).First(&layout).Error
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	return &layout, nil
}

func (s *TicketCatalogService) SaveSeatLayout(
	ctx context.Context,
	userID, organizerID, eventID int64,
	input SeatLayoutInput,
) (*models.SeatLayout, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	event, err := s.repo.FindEventDetail(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if !event.SaleMode.IsSeated() {
		return nil, fmt.Errorf("%w: 只有选座活动可以配置厅图", ErrInvalidTicketCatalog)
	}
	if event.Status != models.EventStatusDraft {
		return nil, fmt.Errorf("%w: 已发布活动不能改厅图，请先下架", ErrInvalidTicketCatalog)
	}
	if !models.SeatLayoutBoundsOK(input.RowCount, input.ColCount) {
		return nil, fmt.Errorf("%w: 厅图最多 %d 行 %d 列", ErrInvalidTicketCatalog, models.MaxSeatLayoutRows, models.MaxSeatLayoutCols)
	}
	tierByID := map[int64]models.TicketTier{}
	for _, session := range event.Sessions {
		for _, tier := range session.TicketTiers {
			tierByID[tier.ID] = tier
		}
	}
	if len(tierByID) == 0 {
		return nil, fmt.Errorf("%w: 请先创建票档再画厅图", ErrInvalidTicketCatalog)
	}
	if len(input.Seats) == 0 {
		return nil, fmt.Errorf("%w: 至少需要一个可售座位", ErrInvalidTicketCatalog)
	}
	seen := map[string]struct{}{}
	seats := make([]models.Seat, 0, len(input.Seats))
	quotaByTier := map[int64]int{}
	for _, cell := range input.Seats {
		tierID := int64(cell.TicketTierID)
		if _, ok := tierByID[tierID]; !ok {
			return nil, fmt.Errorf("%w: 座位必须分配到本场活动的票档", ErrInvalidTicketCatalog)
		}
		if cell.RowNo < 1 || cell.RowNo > input.RowCount || cell.ColNo < 1 || cell.ColNo > input.ColCount {
			return nil, fmt.Errorf("%w: 座位行列超出厅图范围", ErrInvalidTicketCatalog)
		}
		key := fmt.Sprintf("%d:%d", cell.RowNo, cell.ColNo)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("%w: 同一格子不能重复", ErrInvalidTicketCatalog)
		}
		seen[key] = struct{}{}
		quotaByTier[tierID]++
		seats = append(seats, models.Seat{
			TicketTierID: tierID,
			RowNo:        cell.RowNo,
			ColNo:        cell.ColNo,
			Label:        normalizeSeatLabel(cell.Label, cell.RowNo, cell.ColNo),
		})
	}

	var saved models.SeatLayout
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var layout models.SeatLayout
		findErr := tx.Where("event_id = ?", eventID).First(&layout).Error
		if findErr != nil && findErr != gorm.ErrRecordNotFound {
			return findErr
		}
		layout.EventID = eventID
		layout.Name = strings.TrimSpace(input.Name)
		layout.RowCount = input.RowCount
		layout.ColCount = input.ColCount
		if findErr == gorm.ErrRecordNotFound {
			if err := tx.Create(&layout).Error; err != nil {
				return err
			}
		} else if err := tx.Save(&layout).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("layout_id = ?", layout.ID).Delete(&models.Seat{}).Error; err != nil {
			return err
		}
		for i := range seats {
			seats[i].LayoutID = layout.ID
		}
		if err := tx.Create(&seats).Error; err != nil {
			return err
		}
		for _, session := range event.Sessions {
			for _, tier := range session.TicketTiers {
				quota := quotaByTier[tier.ID]
				if err := tx.Model(&models.TicketTier{}).Where("id = ?", tier.ID).
					Updates(map[string]interface{}{
						"total_quota":     quota,
						"remaining_quota": quota,
						"sold_count":      0,
						"status":          models.TicketTierStatusOnSale,
						"version":         gorm.Expr("version + 1"),
					}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Preload("Seats").First(&saved, layout.ID).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalogCaches(ctx, eventID)
	return &saved, nil
}

func (s *TicketCatalogService) ListSessionSeats(
	ctx context.Context,
	eventID, sessionID int64,
) ([]SessionSeatView, error) {
	event, err := s.repo.FindEventByID(ctx, eventID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.Status != models.EventStatusPublished || !event.SaleMode.IsSeated() {
		return nil, fmt.Errorf("%w: 当前活动不提供选座", ErrInvalidTicketCatalog)
	}
	session, err := s.repo.FindSessionByID(ctx, sessionID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if session.EventID != eventID {
		return nil, ErrTicketResourceNotFound
	}
	var rows []models.SessionSeat
	if err := s.db.WithContext(ctx).Preload("Seat").
		Where("session_id = ?", sessionID).
		Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var tiers []models.TicketTier
	if err := s.db.WithContext(ctx).Select("id", "status").
		Where("session_id = ?", sessionID).Find(&tiers).Error; err != nil {
		return nil, err
	}
	onSale := make(map[int64]bool, len(tiers))
	for _, tier := range tiers {
		onSale[tier.ID] = tier.Status == models.TicketTierStatusOnSale
	}
	views := make([]SessionSeatView, 0, len(rows))
	for _, row := range rows {
		views = append(views, SessionSeatView{
			ID:           row.ID,
			SeatID:       row.SeatID,
			TicketTierID: row.TicketTierID,
			RowNo:        row.Seat.RowNo,
			ColNo:        row.Seat.ColNo,
			Label:        row.Seat.Label,
			Status:       overlaySessionSeatStatus(row.Status, onSale[row.TicketTierID]),
		})
	}
	return views, nil
}

func (s *TicketCatalogService) generateSessionSeats(tx *gorm.DB, event *models.Event) error {
	var layout models.SeatLayout
	if err := tx.Preload("Seats").Where("event_id = ?", event.ID).First(&layout).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return fmt.Errorf("%w: 选座活动发布前必须配置厅图", ErrInvalidTicketCatalog)
		}
		return err
	}
	if len(layout.Seats) == 0 {
		return fmt.Errorf("%w: 厅图至少需要一个可售座位", ErrInvalidTicketCatalog)
	}
	seatsByTier := map[int64][]models.Seat{}
	for _, seat := range layout.Seats {
		seatsByTier[seat.TicketTierID] = append(seatsByTier[seat.TicketTierID], seat)
	}
	generated := 0
	for _, session := range event.Sessions {
		sessionHasSeat := false
		liveSeatIDs := make([]int64, 0)
		for _, tier := range session.TicketTiers {
			seats := seatsByTier[tier.ID]
			if len(seats) == 0 {
				continue
			}
			sessionHasSeat = true
			for _, seat := range seats {
				liveSeatIDs = append(liveSeatIDs, seat.ID)
				row := models.SessionSeat{
					SessionID:    session.ID,
					SeatID:       seat.ID,
					TicketTierID: tier.ID,
					Status:       models.SessionSeatAvailable,
				}
				if err := tx.Where("session_id = ? AND seat_id = ?", session.ID, seat.ID).
					Attrs(row).FirstOrCreate(&row).Error; err != nil {
					return err
				}
				generated++
			}
		}
		if !sessionHasSeat {
			return fmt.Errorf("%w: 每个场次都需要至少一个可售座位", ErrInvalidTicketCatalog)
		}
		if len(liveSeatIDs) > 0 {
			if err := tx.Where(
				"session_id = ? AND status = ? AND seat_id NOT IN ?",
				session.ID, models.SessionSeatAvailable, liveSeatIDs,
			).Delete(&models.SessionSeat{}).Error; err != nil {
				return err
			}
		}
		for _, tier := range session.TicketTiers {
			seats := seatsByTier[tier.ID]
			var available int64
			var sold int64
			if err := tx.Model(&models.SessionSeat{}).
				Where("session_id = ? AND ticket_tier_id = ? AND status = ?",
					session.ID, tier.ID, models.SessionSeatAvailable).
				Count(&available).Error; err != nil {
				return err
			}
			if err := tx.Model(&models.SessionSeat{}).
				Where("session_id = ? AND ticket_tier_id = ? AND status = ?",
					session.ID, tier.ID, models.SessionSeatSold).
				Count(&sold).Error; err != nil {
				return err
			}
			status := models.TicketTierStatusOnSale
			if available == 0 && len(seats) > 0 {
				status = models.TicketTierStatusSoldOut
			}
			if err := tx.Model(&models.TicketTier{}).Where("id = ?", tier.ID).
				Updates(map[string]interface{}{
					"total_quota":     len(seats),
					"remaining_quota": int(available),
					"sold_count":      sold,
					"status":          status,
				}).Error; err != nil {
				return err
			}
		}
	}
	if generated == 0 {
		return fmt.Errorf("%w: 选座活动发布前必须配置厅图", ErrInvalidTicketCatalog)
	}
	return nil
}
