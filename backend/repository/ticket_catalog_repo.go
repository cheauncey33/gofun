package repository

import (
	"WHU_Snack_GO/models"
	"context"
	"strings"

	"gorm.io/gorm"
)

// TicketCatalogRepository 只负责票务目录的数据读写。
// 权限判断、状态流转和时间规则仍放在 service，避免把业务规则藏进 SQL 层。
type TicketCatalogRepository interface {
	CreateOrganizerWithOwner(ctx context.Context, organizer *models.Organizer, ownerUserID int64) error
	FindOrganizerByID(ctx context.Context, id int64) (*models.Organizer, error)
	ListOrganizers(ctx context.Context, page, pageSize int) ([]models.Organizer, int64, error)
	FindActiveMembership(ctx context.Context, organizerID, userID int64) (*models.OrganizerMember, error)
	ListActiveMembershipsByUser(ctx context.Context, userID int64) ([]models.OrganizerMember, error)

	CreateVenue(ctx context.Context, venue *models.Venue) error
	FindVenueByID(ctx context.Context, id int64) (*models.Venue, error)
	ListVenuesByOrganizer(ctx context.Context, organizerID int64) ([]models.Venue, error)

	CreateEvent(ctx context.Context, event *models.Event) error
	SaveEvent(ctx context.Context, event *models.Event) error
	FindEventByID(ctx context.Context, id int64) (*models.Event, error)
	FindEventDetail(ctx context.Context, id int64) (*models.Event, error)
	ListPublishedEvents(ctx context.Context, city string, categories []string, keyword string, page, pageSize int) ([]models.Event, int64, error)
	ListPublishedEventsByIDs(ctx context.Context, ids []int64) ([]models.Event, error)
	ListPublishedFacets(ctx context.Context) (cities []string, categories []string, err error)
	ListEventsByOrganizer(ctx context.Context, organizerID int64, page, pageSize int) ([]models.Event, int64, error)

	CreateSession(ctx context.Context, session *models.EventSession) error
	FindSessionByID(ctx context.Context, id int64) (*models.EventSession, error)
	CreateTicketTier(ctx context.Context, tier *models.TicketTier) error
	FindTicketTierByID(ctx context.Context, id int64) (*models.TicketTier, error)
}

type ticketCatalogRepo struct {
	db *gorm.DB
}

func NewTicketCatalogRepository(db *gorm.DB) TicketCatalogRepository {
	return &ticketCatalogRepo{db: db}
}

func (r *ticketCatalogRepo) CreateOrganizerWithOwner(
	ctx context.Context,
	organizer *models.Organizer,
	ownerUserID int64,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(organizer).Error; err != nil {
			return err
		}
		member := models.OrganizerMember{
			OrganizerID: organizer.ID,
			UserID:      ownerUserID,
			Role:        models.OrganizerRoleOwner,
			Status:      models.OrganizerStatusActive,
		}
		return tx.Create(&member).Error
	})
}

func (r *ticketCatalogRepo) FindOrganizerByID(ctx context.Context, id int64) (*models.Organizer, error) {
	var organizer models.Organizer
	err := r.db.WithContext(ctx).First(&organizer, id).Error
	return &organizer, err
}

func (r *ticketCatalogRepo) ListOrganizers(
	ctx context.Context,
	page, pageSize int,
) ([]models.Organizer, int64, error) {
	var organizers []models.Organizer
	var total int64
	query := r.db.WithContext(ctx).Model(&models.Organizer{})
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Order("create_time DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&organizers).Error
	return organizers, total, err
}

func (r *ticketCatalogRepo) FindActiveMembership(
	ctx context.Context,
	organizerID, userID int64,
) (*models.OrganizerMember, error) {
	var member models.OrganizerMember
	err := r.db.WithContext(ctx).
		Where("organizer_id = ? AND user_id = ? AND status = ?",
			organizerID, userID, models.OrganizerStatusActive).
		First(&member).Error
	return &member, err
}

func (r *ticketCatalogRepo) ListActiveMembershipsByUser(
	ctx context.Context,
	userID int64,
) ([]models.OrganizerMember, error) {
	var memberships []models.OrganizerMember
	err := r.db.WithContext(ctx).
		Preload("Organizer").
		Where("user_id = ? AND status = ?", userID, models.OrganizerStatusActive).
		Order("create_time ASC").
		Find(&memberships).Error
	return memberships, err
}

func (r *ticketCatalogRepo) CreateVenue(ctx context.Context, venue *models.Venue) error {
	return r.db.WithContext(ctx).Create(venue).Error
}

func (r *ticketCatalogRepo) FindVenueByID(ctx context.Context, id int64) (*models.Venue, error) {
	var venue models.Venue
	err := r.db.WithContext(ctx).First(&venue, id).Error
	return &venue, err
}

func (r *ticketCatalogRepo) ListVenuesByOrganizer(ctx context.Context, organizerID int64) ([]models.Venue, error) {
	var venues []models.Venue
	err := r.db.WithContext(ctx).
		Where("organizer_id = ?", organizerID).
		Order("create_time DESC").
		Find(&venues).Error
	return venues, err
}

func (r *ticketCatalogRepo) CreateEvent(ctx context.Context, event *models.Event) error {
	return r.db.WithContext(ctx).Create(event).Error
}

func (r *ticketCatalogRepo) SaveEvent(ctx context.Context, event *models.Event) error {
	return r.db.WithContext(ctx).Save(event).Error
}

func (r *ticketCatalogRepo) FindEventByID(ctx context.Context, id int64) (*models.Event, error) {
	var event models.Event
	err := r.db.WithContext(ctx).First(&event, id).Error
	return &event, err
}

func (r *ticketCatalogRepo) FindEventDetail(ctx context.Context, id int64) (*models.Event, error) {
	var event models.Event
	err := r.db.WithContext(ctx).
		Preload("Organizer").
		Preload("Sessions", func(db *gorm.DB) *gorm.DB {
			return db.Order("starts_at ASC")
		}).
		Preload("Sessions.Venue").
		Preload("Sessions.TicketTiers", func(db *gorm.DB) *gorm.DB {
			return db.Order("price_cents ASC")
		}).
		First(&event, id).Error
	return &event, err
}

func (r *ticketCatalogRepo) ListPublishedEvents(
	ctx context.Context,
	city string,
	categories []string,
	keyword string,
	page, pageSize int,
) ([]models.Event, int64, error) {
	var events []models.Event
	var total int64
	query := r.db.WithContext(ctx).Model(&models.Event{}).
		Where("status = ?", models.EventStatusPublished)
	if len(categories) == 1 {
		query = query.Where("category = ?", categories[0])
	} else if len(categories) > 1 {
		query = query.Where("category IN ?", categories)
	}
	if city != "" {
		matchingSessions := r.db.Model(&models.EventSession{}).
			Select("1").
			Joins("JOIN venue ON venue.id = event_session.venue_id AND venue.delete_time IS NULL").
			Where("event_session.event_id = event.id").
			Where("venue.city = ?", city)
		query = query.Where("EXISTS (?)", matchingSessions)
	}
	if keyword != "" {
		like := "%" + escapeLikePattern(keyword) + "%"
		venueMatch := r.db.Model(&models.EventSession{}).
			Select("1").
			Joins("JOIN venue ON venue.id = event_session.venue_id AND venue.delete_time IS NULL").
			Where("event_session.event_id = event.id").
			Where("venue.name LIKE ? OR venue.city LIKE ?", like, like)
		query = query.Where(
			"title LIKE ? OR subtitle LIKE ? OR category LIKE ? OR EXISTS (?)",
			like, like, like, venueMatch,
		)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.Preload("Organizer").
		Preload("Sessions", func(db *gorm.DB) *gorm.DB {
			return db.Where("status IN ?", []models.SessionStatus{
				models.SessionStatusOnSale,
				models.SessionStatusSoldOut,
			}).Order("starts_at ASC")
		}).
		Preload("Sessions.Venue").
		Preload("Sessions.TicketTiers", "status IN ?", []models.TicketTierStatus{
			models.TicketTierStatusOnSale,
			models.TicketTierStatusSoldOut,
		}).
		Order("published_at DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&events).Error
	return events, total, err
}

func (r *ticketCatalogRepo) ListPublishedEventsByIDs(
	ctx context.Context,
	ids []int64,
) ([]models.Event, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var events []models.Event
	err := r.db.WithContext(ctx).
		Where("id IN ? AND status = ?", ids, models.EventStatusPublished).
		Preload("Organizer").
		Preload("Sessions", func(db *gorm.DB) *gorm.DB {
			return db.Where("status IN ?", []models.SessionStatus{
				models.SessionStatusOnSale,
				models.SessionStatusSoldOut,
			}).Order("starts_at ASC")
		}).
		Preload("Sessions.Venue").
		Preload("Sessions.TicketTiers", "status IN ?", []models.TicketTierStatus{
			models.TicketTierStatusOnSale,
			models.TicketTierStatusSoldOut,
		}).
		Find(&events).Error
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]models.Event, len(events))
	for _, event := range events {
		byID[event.ID] = event
	}
	ordered := make([]models.Event, 0, len(ids))
	for _, id := range ids {
		if event, ok := byID[id]; ok {
			ordered = append(ordered, event)
		}
	}
	return ordered, nil
}

func (r *ticketCatalogRepo) ListPublishedFacets(
	ctx context.Context,
) (cities []string, categories []string, err error) {
	err = r.db.WithContext(ctx).Model(&models.Event{}).
		Where("status = ?", models.EventStatusPublished).
		Distinct().
		Order("category ASC").
		Pluck("category", &categories).Error
	if err != nil {
		return nil, nil, err
	}

	err = r.db.WithContext(ctx).
		Table("venue").
		Select("DISTINCT venue.city").
		Joins("JOIN event_session ON event_session.venue_id = venue.id AND event_session.delete_time IS NULL").
		Joins("JOIN event ON event.id = event_session.event_id AND event.delete_time IS NULL").
		Where("venue.delete_time IS NULL").
		Where("event.status = ?", models.EventStatusPublished).
		Where("venue.city <> ''").
		Order("venue.city ASC").
		Pluck("venue.city", &cities).Error
	if err != nil {
		return nil, nil, err
	}
	if cities == nil {
		cities = []string{}
	}
	if categories == nil {
		categories = []string{}
	}
	return cities, categories, nil
}

func escapeLikePattern(value string) string {
	// 去掉通配符，避免用户输入 %/_ 放大匹配面；其余原样模糊匹配。
	replacer := strings.NewReplacer("%", "", "_", "")
	return replacer.Replace(value)
}

func (r *ticketCatalogRepo) ListEventsByOrganizer(
	ctx context.Context,
	organizerID int64,
	page, pageSize int,
) ([]models.Event, int64, error) {
	var events []models.Event
	var total int64
	query := r.db.WithContext(ctx).Model(&models.Event{}).
		Where("organizer_id = ?", organizerID)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := query.
		Preload("Sessions", func(db *gorm.DB) *gorm.DB {
			return db.Order("starts_at ASC")
		}).
		Preload("Sessions.Venue").
		Preload("Sessions.TicketTiers", func(db *gorm.DB) *gorm.DB {
			return db.Order("price_cents ASC")
		}).
		Order("create_time DESC").
		Limit(pageSize).Offset((page - 1) * pageSize).
		Find(&events).Error
	return events, total, err
}

func (r *ticketCatalogRepo) CreateSession(ctx context.Context, session *models.EventSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

func (r *ticketCatalogRepo) FindSessionByID(ctx context.Context, id int64) (*models.EventSession, error) {
	var session models.EventSession
	err := r.db.WithContext(ctx).Preload("Venue").First(&session, id).Error
	return &session, err
}

func (r *ticketCatalogRepo) CreateTicketTier(ctx context.Context, tier *models.TicketTier) error {
	return r.db.WithContext(ctx).Create(tier).Error
}

func (r *ticketCatalogRepo) FindTicketTierByID(ctx context.Context, id int64) (*models.TicketTier, error) {
	var tier models.TicketTier
	err := r.db.WithContext(ctx).First(&tier, id).Error
	return &tier, err
}
