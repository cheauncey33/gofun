package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gofun/models"

	"gorm.io/gorm"
)

type CreateHallInput struct {
	Name string `json:"name" binding:"required"`
}

type HallLayoutInput struct {
	Name     string              `json:"name" binding:"required"`
	RowCount int                 `json:"row_count" binding:"required"`
	ColCount int                 `json:"col_count" binding:"required"`
	Seats    []PhysicalSeatInput `json:"seats" binding:"required"`
}

type PhysicalSeatInput struct {
	RowNo   int    `json:"row_no"`
	ColNo   int    `json:"col_no"`
	Label   string `json:"label"`
	ZoneKey string `json:"zone_key"`
}

type SessionSeatMapInput struct {
	ZoneTiers map[string]FlexibleID `json:"zone_tiers" binding:"required"`
}

func (s *TicketCatalogService) CreateHall(ctx context.Context, userID, organizerID, venueID int64, input CreateHallInput) (*models.Hall, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	venue, err := s.repo.FindVenueByID(ctx, venueID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	name := strings.TrimSpace(input.Name)
	if venue.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if name == "" {
		return nil, ErrInvalidTicketCatalog
	}
	hall := &models.Hall{VenueID: venueID, Name: name, Status: models.OrganizerStatusActive}
	if err := s.db.WithContext(ctx).Create(hall).Error; err != nil {
		return nil, err
	}
	return hall, nil
}

func (s *TicketCatalogService) ListHalls(ctx context.Context, userID, organizerID, venueID int64) ([]models.Hall, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	venue, err := s.repo.FindVenueByID(ctx, venueID)
	if err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if venue.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	var halls []models.Hall
	err = s.db.WithContext(ctx).Where("venue_id = ?", venueID).Order("create_time ASC").Find(&halls).Error
	return halls, err
}

func (s *TicketCatalogService) ListHallLayouts(ctx context.Context, userID, organizerID, hallID int64) ([]models.SeatLayout, error) {
	if _, err := s.ownedHall(ctx, userID, organizerID, hallID); err != nil {
		return nil, err
	}
	var layouts []models.SeatLayout
	err := s.db.WithContext(ctx).Preload("Seats").Where("hall_id = ?", hallID).Order("version DESC").Find(&layouts).Error
	return layouts, err
}

func (s *TicketCatalogService) CreateHallLayout(ctx context.Context, userID, organizerID, hallID int64, input HallLayoutInput) (*models.SeatLayout, error) {
	if _, err := s.ownedHall(ctx, userID, organizerID, hallID); err != nil {
		return nil, err
	}
	if err := validatePhysicalLayout(input); err != nil {
		return nil, err
	}
	var saved models.SeatLayout
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var maxVersion int
		if err := tx.Model(&models.SeatLayout{}).Where("hall_id = ?", hallID).Select("COALESCE(MAX(version), 0)").Scan(&maxVersion).Error; err != nil {
			return err
		}
		layout := models.SeatLayout{HallID: &hallID, Version: maxVersion + 1, Status: models.SeatLayoutDraft, Name: strings.TrimSpace(input.Name), RowCount: input.RowCount, ColCount: input.ColCount}
		if err := tx.Create(&layout).Error; err != nil {
			return err
		}
		seats := physicalSeats(layout.ID, input.Seats)
		if err := tx.Create(&seats).Error; err != nil {
			return err
		}
		return tx.Preload("Seats").First(&saved, layout.ID).Error
	})
	return &saved, err
}

func (s *TicketCatalogService) UpdateHallLayout(ctx context.Context, userID, organizerID, layoutID int64, input HallLayoutInput) (*models.SeatLayout, error) {
	if err := validatePhysicalLayout(input); err != nil {
		return nil, err
	}
	var saved models.SeatLayout
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var layout models.SeatLayout
		if err := tx.First(&layout, layoutID).Error; err != nil {
			return normalizeTicketNotFound(err)
		}
		if layout.HallID == nil {
			return fmt.Errorf("%w: 历史活动厅图不能作为场馆模板编辑", ErrInvalidTicketCatalog)
		}
		if _, err := s.ownedHallWithDB(ctx, tx, userID, organizerID, *layout.HallID); err != nil {
			return err
		}
		if layout.Status != models.SeatLayoutDraft {
			return fmt.Errorf("%w: 已发布厅图不可修改，请创建新版本", ErrInvalidTicketCatalog)
		}
		layout.Name, layout.RowCount, layout.ColCount = strings.TrimSpace(input.Name), input.RowCount, input.ColCount
		if err := tx.Save(&layout).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("layout_id = ?", layout.ID).Delete(&models.Seat{}).Error; err != nil {
			return err
		}
		seats := physicalSeats(layout.ID, input.Seats)
		if err := tx.Create(&seats).Error; err != nil {
			return err
		}
		return tx.Preload("Seats").First(&saved, layout.ID).Error
	})
	return &saved, err
}

func (s *TicketCatalogService) PublishHallLayout(ctx context.Context, userID, organizerID, layoutID int64) (*models.SeatLayout, error) {
	var layout models.SeatLayout
	if err := s.db.WithContext(ctx).Preload("Seats").First(&layout, layoutID).Error; err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if layout.HallID == nil {
		return nil, ErrInvalidTicketCatalog
	}
	if _, err := s.ownedHall(ctx, userID, organizerID, *layout.HallID); err != nil {
		return nil, err
	}
	if len(layout.Seats) == 0 {
		return nil, fmt.Errorf("%w: 空厅图不能发布", ErrInvalidTicketCatalog)
	}
	if layout.Status == models.SeatLayoutPublished {
		return &layout, nil
	}
	now := time.Now()
	if err := s.db.WithContext(ctx).Model(&layout).Updates(map[string]interface{}{"status": models.SeatLayoutPublished, "published_at": now}).Error; err != nil {
		return nil, err
	}
	layout.Status, layout.PublishedAt = models.SeatLayoutPublished, &now
	return &layout, nil
}

func (s *TicketCatalogService) ConfigureSessionSeatMap(ctx context.Context, userID, organizerID, sessionID int64, input SessionSeatMapInput) ([]models.SessionSeat, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	var session models.EventSession
	if err := s.db.WithContext(ctx).First(&session, sessionID).Error; err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	var event models.Event
	if err := s.db.WithContext(ctx).First(&event, session.EventID).Error; err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if event.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	if event.Status != models.EventStatusDraft || !event.SaleMode.IsSeated() {
		return nil, fmt.Errorf("%w: 只能配置草稿选座活动", ErrInvalidTicketCatalog)
	}
	if session.SeatLayoutID == nil {
		return nil, fmt.Errorf("%w: 场次未选择厅图", ErrInvalidTicketCatalog)
	}
	var layout models.SeatLayout
	if err := s.db.WithContext(ctx).Preload("Seats").First(&layout, *session.SeatLayoutID).Error; err != nil {
		return nil, normalizeTicketNotFound(err)
	}
	if layout.Status != models.SeatLayoutPublished {
		return nil, fmt.Errorf("%w: 场次只能使用已发布厅图", ErrInvalidTicketCatalog)
	}
	var tiers []models.TicketTier
	if err := s.db.WithContext(ctx).Where("session_id = ?", sessionID).Find(&tiers).Error; err != nil {
		return nil, err
	}
	tierSet := make(map[int64]struct{}, len(tiers))
	for _, t := range tiers {
		tierSet[t.ID] = struct{}{}
	}
	rows := make([]models.SessionSeat, 0, len(layout.Seats))
	for _, seat := range layout.Seats {
		tierID := int64(input.ZoneTiers[seat.ZoneKey])
		if tierID == 0 {
			tierID = int64(input.ZoneTiers["general"])
		}
		if _, ok := tierSet[tierID]; !ok {
			return nil, fmt.Errorf("%w: 分区 %s 未配置本场有效票档", ErrInvalidTicketCatalog, seat.ZoneKey)
		}
		rows = append(rows, models.SessionSeat{SessionID: sessionID, SeatID: seat.ID, TicketTierID: tierID, Status: models.SessionSeatAvailable})
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var occupied int64
		if err := tx.Model(&models.SessionSeat{}).Where("session_id = ? AND status <> ?", sessionID, models.SessionSeatAvailable).Count(&occupied).Error; err != nil {
			return err
		}
		if occupied > 0 {
			return fmt.Errorf("%w: 已占用座位的场次不能重新配置", ErrInvalidTicketCatalog)
		}
		if err := tx.Unscoped().Where("session_id = ?", sessionID).Delete(&models.SessionSeat{}).Error; err != nil {
			return err
		}
		if len(rows) > 0 {
			return tx.Create(&rows).Error
		}
		return nil
	})
	return rows, err
}

func (s *TicketCatalogService) validatePublishedLayout(ctx context.Context, organizerID, venueID, hallID, layoutID int64) error {
	var count int64
	err := s.db.WithContext(ctx).Table("seat_layout sl").Joins("JOIN hall h ON h.id = sl.hall_id").Joins("JOIN venue v ON v.id = h.venue_id").Where("sl.id = ? AND sl.hall_id = ? AND sl.status = ? AND h.venue_id = ? AND v.organizer_id = ?", layoutID, hallID, models.SeatLayoutPublished, venueID, organizerID).Count(&count).Error
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("%w: 厅图与场馆不匹配或尚未发布", ErrInvalidTicketCatalog)
	}
	return nil
}

func (s *TicketCatalogService) ownedHall(ctx context.Context, userID, organizerID, hallID int64) (*models.Hall, error) {
	return s.ownedHallWithDB(ctx, s.db, userID, organizerID, hallID)
}
func (s *TicketCatalogService) ownedHallWithDB(ctx context.Context, db *gorm.DB, userID, organizerID, hallID int64) (*models.Hall, error) {
	if err := s.requireOrganizerAccess(ctx, organizerID, userID); err != nil {
		return nil, err
	}
	var hall models.Hall
	err := db.WithContext(ctx).Preload("Venue").First(&hall, hallID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrTicketResourceNotFound
	}
	if err != nil {
		return nil, err
	}
	if hall.Venue.OrganizerID != organizerID {
		return nil, ErrOrganizerForbidden
	}
	return &hall, nil
}

func validatePhysicalLayout(input HallLayoutInput) error {
	if strings.TrimSpace(input.Name) == "" || !models.SeatLayoutBoundsOK(input.RowCount, input.ColCount) || len(input.Seats) == 0 {
		return ErrInvalidTicketCatalog
	}
	seen := map[string]struct{}{}
	for _, cell := range input.Seats {
		if cell.RowNo < 1 || cell.RowNo > input.RowCount || cell.ColNo < 1 || cell.ColNo > input.ColCount {
			return ErrInvalidTicketCatalog
		}
		key := fmt.Sprintf("%d:%d", cell.RowNo, cell.ColNo)
		if _, ok := seen[key]; ok {
			return ErrInvalidTicketCatalog
		}
		seen[key] = struct{}{}
	}
	return nil
}

func physicalSeats(layoutID int64, input []PhysicalSeatInput) []models.Seat {
	rows := make([]models.Seat, 0, len(input))
	for _, cell := range input {
		zone := strings.TrimSpace(cell.ZoneKey)
		if zone == "" {
			zone = "general"
		}
		rows = append(rows, models.Seat{LayoutID: layoutID, RowNo: cell.RowNo, ColNo: cell.ColNo, Label: normalizeSeatLabel(cell.Label, cell.RowNo, cell.ColNo), ZoneKey: zone})
	}
	return rows
}
