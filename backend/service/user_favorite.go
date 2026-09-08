package service

import (
	"context"
	"errors"
	"gofun/models"
	"strconv"
	"time"

	"gorm.io/gorm"
)

type FavoriteView struct {
	models.UserFavorite
	Title       string `json:"title"`
	Subtitle    string `json:"subtitle"`
	CoverURL    string `json:"cover_url"`
	EventID     int64  `json:"event_id,string,omitempty"`
	SaleID      int64  `json:"sale_id,string,omitempty"`
	Href        string `json:"href"`
	KindLabel   string `json:"kind_label"`
	Status      string `json:"status"`
	StatusLabel string `json:"status_label"`
}

type AddFavoriteReq struct {
	TargetType string `json:"target_type"`
	TargetID   int64  `json:"target_id,string"`
}

func parseFavoriteType(raw string) (string, error) {
	switch raw {
	case models.FavoriteTargetEvent, models.FavoriteTargetRushSale:
		return raw, nil
	default:
		return "", errors.New("只能收藏活动或限时开售")
	}
}

func (s *UserService) AddFavorite(userID int64, req AddFavoriteReq) (*FavoriteView, error) {
	targetType, err := parseFavoriteType(req.TargetType)
	if err != nil {
		return nil, err
	}
	if req.TargetID <= 0 {
		return nil, errors.New("收藏目标无效")
	}
	if err := s.ensureFavoriteTarget(targetType, req.TargetID); err != nil {
		return nil, err
	}

	var row models.UserFavorite
	err = s.db.Unscoped().Where("user_id = ? AND target_type = ? AND target_id = ?", userID, targetType, req.TargetID).First(&row).Error
	if err == nil {
		if row.DeleteTime.Valid {
			if err := s.db.Unscoped().Model(&row).Update("delete_time", nil).Error; err != nil {
				return nil, err
			}
			row.DeleteTime = gorm.DeletedAt{}
		}
		return s.hydrateFavorite(row)
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	row = models.UserFavorite{UserID: userID, TargetType: targetType, TargetID: req.TargetID}
	if err := s.db.Create(&row).Error; err != nil {
		if isDuplicateStorageKeyError(err) {
			return s.loadExistingFavorite(userID, targetType, req.TargetID)
		}
		return nil, err
	}
	return s.hydrateFavorite(row)
}

func (s *UserService) loadExistingFavorite(userID int64, targetType string, targetID int64) (*FavoriteView, error) {
	var row models.UserFavorite
	err := s.db.Unscoped().Where("user_id = ? AND target_type = ? AND target_id = ?", userID, targetType, targetID).First(&row).Error
	if err != nil {
		return nil, errors.New("已收藏")
	}
	if row.DeleteTime.Valid {
		if err := s.db.Unscoped().Model(&row).Update("delete_time", nil).Error; err != nil {
			return nil, errors.New("已收藏")
		}
		row.DeleteTime = gorm.DeletedAt{}
	}
	return s.hydrateFavorite(row)
}

func (s *UserService) RemoveFavorite(userID, favoriteID int64) error {
	res := s.db.Where("id = ? AND user_id = ?", favoriteID, userID).Delete(&models.UserFavorite{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("收藏不存在")
	}
	return nil
}

func (s *UserService) ListFavorites(userID int64, targetType string) ([]FavoriteView, error) {
	query := s.db.Where("user_id = ?", userID).Order("id DESC")
	if parsed, err := parseFavoriteType(targetType); err == nil {
		query = query.Where("target_type = ?", parsed)
	}
	var rows []models.UserFavorite
	if err := query.Find(&rows).Error; err != nil {
		return nil, err
	}
	return s.hydrateFavorites(rows), nil
}

func (s *UserService) ensureFavoriteTarget(targetType string, targetID int64) error {
	switch targetType {
	case models.FavoriteTargetEvent:
		var event models.Event
		if err := s.db.First(&event, targetID).Error; err != nil {
			return errors.New("活动不存在")
		}
		if event.Status != models.EventStatusPublished {
			return errors.New("只能收藏已发布的活动")
		}
		return nil
	case models.FavoriteTargetRushSale:
		var campaign models.RushSaleCampaign
		if err := s.db.First(&campaign, targetID).Error; err != nil {
			return errors.New("限时开售不存在")
		}
		if campaign.Status != models.RushSaleStatusScheduled && campaign.Status != models.RushSaleStatusActive {
			return errors.New("这场限时开售已结束")
		}
		if !campaign.EndsAt.After(time.Now()) {
			return errors.New("这场限时开售已结束")
		}
		return nil
	default:
		return errors.New("只能收藏活动或限时开售")
	}
}

func (s *UserService) hydrateFavorite(row models.UserFavorite) (*FavoriteView, error) {
	views := s.hydrateFavorites([]models.UserFavorite{row})
	if len(views) == 0 {
		return nil, errors.New("收藏不存在")
	}
	return &views[0], nil
}

type favoriteLookup struct {
	events          map[int64]models.Event
	campaigns       map[int64]models.RushSaleCampaign
	tiers           map[int64]models.TicketTier
	sessions        map[int64]models.EventSession
	sessionsByEvent map[int64][]models.EventSession
	openRush        map[int64]bool
}

func (s *UserService) hydrateFavorites(rows []models.UserFavorite) []FavoriteView {
	lookup := s.loadFavoriteLookup(rows)
	views := make([]FavoriteView, 0, len(rows))
	for _, row := range rows {
		views = append(views, lookup.view(row))
	}
	return views
}

func (s *UserService) loadFavoriteLookup(rows []models.UserFavorite) favoriteLookup {
	lookup := favoriteLookup{
		events:          map[int64]models.Event{},
		campaigns:       map[int64]models.RushSaleCampaign{},
		tiers:           map[int64]models.TicketTier{},
		sessions:        map[int64]models.EventSession{},
		sessionsByEvent: map[int64][]models.EventSession{},
		openRush:        map[int64]bool{},
	}
	eventIDs := make([]int64, 0, len(rows))
	rushIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		switch row.TargetType {
		case models.FavoriteTargetEvent:
			eventIDs = append(eventIDs, row.TargetID)
		case models.FavoriteTargetRushSale:
			rushIDs = append(rushIDs, row.TargetID)
		}
	}
	eventIDs = uniqueInt64(eventIDs)
	rushIDs = uniqueInt64(rushIDs)

	if len(eventIDs) > 0 {
		var events []models.Event
		if err := s.db.Unscoped().Where("id IN ?", eventIDs).Find(&events).Error; err == nil {
			for _, event := range events {
				lookup.events[event.ID] = event
			}
		}
	}
	if len(rushIDs) > 0 {
		var campaigns []models.RushSaleCampaign
		if err := s.db.Unscoped().Where("id IN ?", rushIDs).Find(&campaigns).Error; err == nil {
			for _, campaign := range campaigns {
				lookup.campaigns[campaign.ID] = campaign
			}
		}
	}

	tierIDs := make([]int64, 0, len(lookup.campaigns))
	for _, campaign := range lookup.campaigns {
		if campaign.TicketTierID > 0 {
			tierIDs = append(tierIDs, campaign.TicketTierID)
		}
	}
	tierIDs = uniqueInt64(tierIDs)
	if len(tierIDs) > 0 {
		var tiers []models.TicketTier
		if err := s.db.Unscoped().Where("id IN ?", tierIDs).Find(&tiers).Error; err == nil {
			for _, tier := range tiers {
				lookup.tiers[tier.ID] = tier
			}
		}
	}

	sessionIDs := make([]int64, 0, len(lookup.tiers))
	for _, tier := range lookup.tiers {
		if tier.SessionID > 0 {
			sessionIDs = append(sessionIDs, tier.SessionID)
		}
	}
	sessionIDs = uniqueInt64(sessionIDs)
	if len(sessionIDs) > 0 {
		var sessions []models.EventSession
		if err := s.db.Unscoped().Where("id IN ?", sessionIDs).Find(&sessions).Error; err == nil {
			for _, session := range sessions {
				lookup.sessions[session.ID] = session
				if _, ok := lookup.events[session.EventID]; !ok {
					eventIDs = append(eventIDs, session.EventID)
				}
			}
		}
	}

	missingEventIDs := uniqueInt64(eventIDs)
	stillMissing := make([]int64, 0, len(missingEventIDs))
	for _, id := range missingEventIDs {
		if _, ok := lookup.events[id]; !ok {
			stillMissing = append(stillMissing, id)
		}
	}
	if len(stillMissing) > 0 {
		var events []models.Event
		if err := s.db.Unscoped().Where("id IN ?", stillMissing).Find(&events).Error; err == nil {
			for _, event := range events {
				lookup.events[event.ID] = event
			}
		}
	}

	s.overlayFavoriteRushRemaining(lookup.campaigns)

	allEventIDs := make([]int64, 0, len(lookup.events))
	for id := range lookup.events {
		allEventIDs = append(allEventIDs, id)
	}
	if len(allEventIDs) > 0 {
		var sessions []models.EventSession
		if err := s.db.Where("event_id IN ?", allEventIDs).Find(&sessions).Error; err == nil {
			for _, session := range sessions {
				lookup.sessionsByEvent[session.EventID] = append(lookup.sessionsByEvent[session.EventID], session)
			}
		}
	}
	lookup.openRush = s.batchOpenRushByEvent(allEventIDs)
	return lookup
}

func (s *UserService) overlayFavoriteRushRemaining(campaigns map[int64]models.RushSaleCampaign) {
	if s.rushSales == nil || len(campaigns) == 0 {
		return
	}
	ctx := context.Background()
	for id, campaign := range campaigns {
		campaign.RemainingQuota = s.rushSales.LiveRemaining(ctx, campaign.ID, campaign.TotalQuota, campaign.RemainingQuota)
		campaigns[id] = campaign
	}
}

func (s *UserService) batchOpenRushByEvent(eventIDs []int64) map[int64]bool {
	open := map[int64]bool{}
	if len(eventIDs) == 0 {
		return open
	}
	var sessions []models.EventSession
	if err := s.db.Select("id", "event_id").Where("event_id IN ?", eventIDs).Find(&sessions).Error; err != nil || len(sessions) == 0 {
		return open
	}
	sessionToEvent := make(map[int64]int64, len(sessions))
	sessionIDs := make([]int64, 0, len(sessions))
	for _, session := range sessions {
		sessionToEvent[session.ID] = session.EventID
		sessionIDs = append(sessionIDs, session.ID)
	}
	var tiers []models.TicketTier
	if err := s.db.Select("id", "session_id").Where("session_id IN ?", sessionIDs).Find(&tiers).Error; err != nil || len(tiers) == 0 {
		return open
	}
	tierToEvent := make(map[int64]int64, len(tiers))
	tierIDs := make([]int64, 0, len(tiers))
	for _, tier := range tiers {
		if eventID, ok := sessionToEvent[tier.SessionID]; ok {
			tierToEvent[tier.ID] = eventID
			tierIDs = append(tierIDs, tier.ID)
		}
	}
	if len(tierIDs) == 0 {
		return open
	}
	var campaigns []models.RushSaleCampaign
	if err := s.db.Where("ticket_tier_id IN ?", tierIDs).
		Where("status IN ?", []models.RushSaleStatus{
			models.RushSaleStatusScheduled,
			models.RushSaleStatusActive,
		}).
		Where("ends_at > ?", time.Now()).
		Find(&campaigns).Error; err != nil {
		return open
	}
	ctx := context.Background()
	for _, campaign := range campaigns {
		remaining := campaign.RemainingQuota
		if s.rushSales != nil {
			remaining = s.rushSales.LiveRemaining(ctx, campaign.ID, campaign.TotalQuota, remaining)
		}
		if remaining <= 0 {
			continue
		}
		if eventID, ok := tierToEvent[campaign.TicketTierID]; ok {
			open[eventID] = true
		}
	}
	return open
}

func (l favoriteLookup) view(row models.UserFavorite) FavoriteView {
	view := FavoriteView{UserFavorite: row}
	switch row.TargetType {
	case models.FavoriteTargetEvent:
		event, ok := l.events[row.TargetID]
		if !ok {
			view.Title = "活动已失效"
			view.KindLabel = "普通购票"
			view.Status, view.StatusLabel = "unavailable", "已下架"
			view.Href = favoriteAccountHref
			return view
		}
		view.Title = event.Title
		view.Subtitle = event.Subtitle
		view.CoverURL = event.CoverURL
		view.EventID = event.ID
		view.Href = favoriteEventHref(event.ID)
		view.KindLabel = "普通购票"
		view.Status, view.StatusLabel = eventFavoriteStatus(event, l.sessionsByEvent[event.ID], l.openRush[event.ID])
	case models.FavoriteTargetRushSale:
		campaign, ok := l.campaigns[row.TargetID]
		if !ok {
			view.Title = "限时开售已失效"
			view.KindLabel = "限时开售"
			view.Status, view.StatusLabel = "unavailable", "已下架"
			view.Href = favoriteAccountHref
			return view
		}
		view.Title = campaign.Name
		view.SaleID = campaign.ID
		view.KindLabel = "限时开售"
		var event *models.Event
		if tier, ok := l.tiers[campaign.TicketTierID]; ok {
			if session, ok := l.sessions[tier.SessionID]; ok {
				if loaded, ok := l.events[session.EventID]; ok {
					copied := loaded
					event = &copied
					view.EventID = loaded.ID
					view.Subtitle = loaded.Title
					view.CoverURL = loaded.CoverURL
				}
			}
		}
		view.Href = favoriteRushHref(view.EventID, campaign.ID)
		view.Status, view.StatusLabel = rushFavoriteStatus(campaign, event)
	}
	return view
}

const favoriteAccountHref = "/account?tab=favorites"

func favoriteEventHref(eventID int64) string {
	return "/events/" + strconv.FormatInt(eventID, 10)
}

func favoriteRushHref(eventID, saleID int64) string {
	if eventID <= 0 {
		return "/rush-sales?sale=" + strconv.FormatInt(saleID, 10)
	}
	href := favoriteEventHref(eventID)
	if saleID > 0 {
		href += "?sale=" + strconv.FormatInt(saleID, 10)
	}
	return href
}

func eventFavoriteStatus(event models.Event, sessions []models.EventSession, hasOpenRush bool) (string, string) {
	if event.DeleteTime.Valid || (event.Status != models.EventStatusPublished && event.Status != models.EventStatusFinished) {
		return "unavailable", "已下架"
	}
	if event.Status == models.EventStatusFinished {
		return "ended", "已结束"
	}
	if len(sessions) == 0 {
		return "on_sale", "售票中"
	}
	now := time.Now()
	hasOnSale := false
	hasUpcoming := false
	hasSoldOut := false
	hasSaleClosed := false
	hasShowOver := false
	hasActive := false
	for _, session := range sessions {
		if session.Status == models.SessionStatusDraft || session.Status == models.SessionStatusCancelled {
			continue
		}
		hasActive = true
		if session.Status == models.SessionStatusFinished || now.After(session.EndsAt) {
			hasShowOver = true
			continue
		}
		if session.Status == models.SessionStatusSoldOut {
			hasSoldOut = true
			continue
		}
		if now.Before(session.SaleStartsAt) {
			hasUpcoming = true
			continue
		}
		if now.After(session.SaleEndsAt) {
			hasSaleClosed = true
			continue
		}
		hasOnSale = true
	}
	if hasOnSale {
		return "on_sale", "售票中"
	}
	if hasOpenRush {
		return "live", "抢票中"
	}
	if hasUpcoming {
		return "scheduled", "未开售"
	}
	if hasSoldOut {
		return "sold_out", "已售罄"
	}
	if hasSaleClosed {
		return "sale_closed", "已停售"
	}
	if !hasActive {
		return "unavailable", "已下架"
	}
	if hasShowOver {
		return "ended", "已结束"
	}
	return "ended", "已结束"
}

func rushFavoriteStatus(campaign models.RushSaleCampaign, event *models.Event) (string, string) {
	if event != nil && (event.DeleteTime.Valid || (event.Status != models.EventStatusPublished && event.Status != models.EventStatusFinished)) {
		return "unavailable", "已下架"
	}
	now := time.Now()
	if campaign.DeleteTime.Valid || campaign.Status == models.RushSaleStatusCancelled {
		return "ended", "已结束"
	}
	if campaign.RemainingQuota <= 0 {
		return "sold_out", "已抢光"
	}
	if campaign.Status == models.RushSaleStatusEnded || (!campaign.EndsAt.IsZero() && !campaign.EndsAt.After(now)) {
		return "ended", "已结束"
	}
	if !campaign.StartsAt.IsZero() && now.Before(campaign.StartsAt) {
		return "scheduled", "即将开售"
	}
	if campaign.TotalQuota > 0 && float64(campaign.RemainingQuota)/float64(campaign.TotalQuota) <= 0.25 {
		return "ending", "即将售罄"
	}
	return "live", "抢票中"
}

func uniqueInt64(ids []int64) []int64 {
	if len(ids) == 0 {
		return ids
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out
}
