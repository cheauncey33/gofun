package service

import (
	"context"
	"errors"
	"testing"

	"gofun/models"
	"gofun/repository"
	"gofun/search"
)

type catalogListRepoStub struct {
	repository.TicketCatalogRepository
	listPublished     func(context.Context, string, []string, string, int, int) ([]models.Event, int64, error)
	listPublishedByID func(context.Context, []int64) ([]models.Event, error)
	findEvent         func(context.Context, int64) (*models.Event, error)
}

func (r catalogListRepoStub) FindEventByID(ctx context.Context, id int64) (*models.Event, error) {
	if r.findEvent == nil {
		return nil, errors.New("unexpected FindEventByID call")
	}
	return r.findEvent(ctx, id)
}

func (r catalogListRepoStub) ListPublishedEvents(
	ctx context.Context,
	city string,
	categories []string,
	keyword string,
	page, pageSize int,
) ([]models.Event, int64, error) {
	return r.listPublished(ctx, city, categories, keyword, page, pageSize)
}

func (r catalogListRepoStub) ListPublishedEventsByIDs(
	ctx context.Context,
	ids []int64,
) ([]models.Event, error) {
	return r.listPublishedByID(ctx, ids)
}

type catalogSearcherStub struct {
	search.EventSearcher
	ids   []int64
	total int64
}

func (s catalogSearcherStub) Enabled() bool { return true }
func (s catalogSearcherStub) Search(context.Context, search.Query) ([]int64, int64, error) {
	return s.ids, s.total, nil
}

type reindexSearcherStub struct {
	search.EventSearcher
	resetCount   int
	refreshCount int
	indexedDocs  []search.EventDoc
	refreshFlags []bool
	indexErr     error
	eventIDs     []int64
	deletedIDs   []int64
}

func (s *reindexSearcherStub) Enabled() bool { return true }
func (s *reindexSearcherStub) ResetIndex(context.Context) error {
	s.resetCount++
	s.eventIDs = nil
	return nil
}
func (s *reindexSearcherStub) IndexEvent(_ context.Context, doc search.EventDoc, refresh bool) error {
	s.indexedDocs = append(s.indexedDocs, doc)
	s.refreshFlags = append(s.refreshFlags, refresh)
	return s.indexErr
}
func (s *reindexSearcherStub) RefreshIndex(context.Context) error {
	s.refreshCount++
	return nil
}
func (s *reindexSearcherStub) ListEventIDs(context.Context) ([]int64, error) {
	return append([]int64(nil), s.eventIDs...), nil
}
func (s *reindexSearcherStub) DeleteEvent(_ context.Context, eventID int64, _ bool) error {
	s.deletedIDs = append(s.deletedIDs, eventID)
	return nil
}

func TestTicketCatalogListQueryNormalize(t *testing.T) {
	query := TicketCatalogListQuery{
		Page:     -2,
		PageSize: 500,
		City:     " 武汉 ",
		Category: " 音乐现场 , 脱口秀 , 音乐现场 ",
		Keyword:  " 夏夜 ",
	}
	query.Normalize()
	if query.Page != 1 || query.PageSize != 12 {
		t.Fatalf("unexpected pagination: page=%d pageSize=%d", query.Page, query.PageSize)
	}
	if query.City != "武汉" || query.Keyword != "夏夜" {
		t.Fatalf("query text was not trimmed: %#v", query)
	}
	got := query.Categories()
	if len(got) != 2 || got[0] != "音乐现场" || got[1] != "脱口秀" {
		t.Fatalf("unexpected categories: %#v", got)
	}
}

func TestOrganizerSlugPattern(t *testing.T) {
	valid := []string{"livehouse-01", "fuchang", "abc"}
	invalid := []string{"A-Team", "-abc", "abc-", "a", "中文主办方", "has space"}
	for _, slug := range valid {
		if !organizerSlugPattern.MatchString(slug) {
			t.Fatalf("expected valid slug: %s", slug)
		}
	}
	for _, slug := range invalid {
		if organizerSlugPattern.MatchString(slug) {
			t.Fatalf("expected invalid slug: %s", slug)
		}
	}
}

func TestListPublishedEventsFallsBackWhenESProjectionIncomplete(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ids        []int64
		mysqlByIDs []models.Event
	}{
		{
			name:       "stale document is filtered by MySQL",
			ids:        []int64{1, 2},
			mysqlByIDs: []models.Event{{Base: models.Base{ID: 1}}},
		},
		{
			name: "empty ES page",
			ids:  nil,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fallbackCalls := 0
			repo := catalogListRepoStub{
				listPublishedByID: func(context.Context, []int64) ([]models.Event, error) {
					return tt.mysqlByIDs, nil
				},
				listPublished: func(context.Context, string, []string, string, int, int) ([]models.Event, int64, error) {
					fallbackCalls++
					return []models.Event{{Base: models.Base{ID: 99}}}, 1, nil
				},
			}
			svc := &TicketCatalogService{
				repo:     repo,
				searcher: catalogSearcherStub{ids: tt.ids, total: int64(len(tt.ids))},
				preferES: true,
			}

			events, total, err := svc.ListPublishedEvents(context.Background(), TicketCatalogListQuery{
				Keyword:  "音乐",
				Page:     1,
				PageSize: 12,
			})
			if err != nil {
				t.Fatalf("ListPublishedEvents() error = %v", err)
			}
			if fallbackCalls != 1 || total != 1 || len(events) != 1 || events[0].ID != 99 {
				t.Fatalf("expected MySQL fallback result, calls=%d total=%d events=%#v", fallbackCalls, total, events)
			}
		})
	}
}

func TestReindexPublishedEventsResetsAndRefreshesOnce(t *testing.T) {
	t.Parallel()
	event := models.Event{
		Base:     models.Base{ID: 7},
		Title:    "双城巡演",
		Category: "音乐现场",
		Status:   models.EventStatusPublished,
		Sessions: []models.EventSession{
			{Venue: models.Venue{City: "武汉", Name: "A 场馆"}},
			{Venue: models.Venue{City: "上海", Name: "B 场馆"}},
		},
	}
	repo := catalogListRepoStub{
		listPublished: func(context.Context, string, []string, string, int, int) ([]models.Event, int64, error) {
			return []models.Event{event}, 1, nil
		},
		listPublishedByID: func(context.Context, []int64) ([]models.Event, error) {
			return nil, nil
		},
	}
	searcher := &reindexSearcherStub{}
	svc := &TicketCatalogService{repo: repo, searcher: searcher}

	if err := svc.ReindexPublishedEvents(context.Background()); err != nil {
		t.Fatalf("ReindexPublishedEvents() error = %v", err)
	}
	if searcher.resetCount != 1 || searcher.refreshCount != 1 {
		t.Fatalf("reset=%d refresh=%d, want one each", searcher.resetCount, searcher.refreshCount)
	}
	if len(searcher.indexedDocs) != 1 || len(searcher.refreshFlags) != 1 || searcher.refreshFlags[0] {
		t.Fatalf("expected one unrefreshed document write, docs=%#v flags=%#v", searcher.indexedDocs, searcher.refreshFlags)
	}
	if got := searcher.indexedDocs[0].City; len(got) != 2 || got[0] != "武汉" || got[1] != "上海" {
		t.Fatalf("expected multi-city document, got %#v", got)
	}
}

func TestReindexPublishedEventsDisablesESAfterPartialFailure(t *testing.T) {
	t.Parallel()
	repo := catalogListRepoStub{
		listPublished: func(context.Context, string, []string, string, int, int) ([]models.Event, int64, error) {
			return []models.Event{{
				Base:   models.Base{ID: 8},
				Title:  "重建失败活动",
				Status: models.EventStatusPublished,
			}}, 1, nil
		},
		listPublishedByID: func(context.Context, []int64) ([]models.Event, error) {
			return nil, nil
		},
	}
	searcher := &reindexSearcherStub{indexErr: errors.New("ES write failed")}
	svc := &TicketCatalogService{repo: repo, searcher: searcher, preferES: true}

	if err := svc.ReindexPublishedEvents(context.Background()); err == nil {
		t.Fatal("expected reindex error")
	}
	if svc.preferES {
		t.Fatal("partial rebuild must disable ES reads for this process")
	}
}

func TestSyncPublishedEventsRepairsMissingAndDeletesOrphans(t *testing.T) {
	t.Parallel()
	published := models.Event{
		Base:   models.Base{ID: 7},
		Title:  "仍在售活动",
		Status: models.EventStatusPublished,
	}
	repo := catalogListRepoStub{
		listPublished: func(context.Context, string, []string, string, int, int) ([]models.Event, int64, error) {
			return []models.Event{published}, 1, nil
		},
		listPublishedByID: func(context.Context, []int64) ([]models.Event, error) {
			return nil, nil
		},
		findEvent: func(_ context.Context, id int64) (*models.Event, error) {
			return &models.Event{Base: models.Base{ID: id}, Status: models.EventStatusCancelled}, nil
		},
	}
	searcher := &reindexSearcherStub{eventIDs: []int64{9}}
	svc := &TicketCatalogService{repo: repo, searcher: searcher, preferES: true}

	result, err := svc.SyncPublishedEvents(context.Background())
	if err != nil {
		t.Fatalf("SyncPublishedEvents() error = %v", err)
	}
	if result.Indexed != 1 || result.Missing != 1 || result.Deleted != 1 {
		t.Fatalf("unexpected sync result: %#v", result)
	}
	if len(searcher.deletedIDs) != 1 || searcher.deletedIDs[0] != 9 {
		t.Fatalf("unexpected deleted IDs: %#v", searcher.deletedIDs)
	}
	if searcher.refreshCount != 1 {
		t.Fatalf("refresh count = %d, want 1", searcher.refreshCount)
	}
}
