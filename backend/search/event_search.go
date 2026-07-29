package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esapi"
)

// EventDoc 写入 ES 的活动检索投影。MySQL 仍是权威数据源。
type EventDoc struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	Category    string    `json:"category"`
	Status      string    `json:"status"`
	City        []string  `json:"city"`
	VenueText   string    `json:"venue_text"`
	SearchText  string    `json:"search_text"`
	PublishedAt time.Time `json:"published_at"`
}

type Query struct {
	Keyword    string
	City       string
	Categories []string
	Page       int
	PageSize   int
}

// EventSearcher 活动全文检索。disabled 时 Enabled()=false，调用方走 MySQL LIKE。
type EventSearcher interface {
	Enabled() bool
	EnsureIndex(ctx context.Context) error
	ResetIndex(ctx context.Context) error
	IndexEvent(ctx context.Context, doc EventDoc, refresh bool) error
	RefreshIndex(ctx context.Context) error
	DeleteEvent(ctx context.Context, eventID int64, refresh bool) error
	ListEventIDs(ctx context.Context) ([]int64, error)
	Search(ctx context.Context, q Query) (ids []int64, total int64, err error)
}

type NoopEventSearcher struct{}

func (NoopEventSearcher) Enabled() bool                                    { return false }
func (NoopEventSearcher) EnsureIndex(context.Context) error                { return nil }
func (NoopEventSearcher) ResetIndex(context.Context) error                 { return nil }
func (NoopEventSearcher) IndexEvent(context.Context, EventDoc, bool) error { return nil }
func (NoopEventSearcher) RefreshIndex(context.Context) error               { return nil }
func (NoopEventSearcher) DeleteEvent(context.Context, int64, bool) error   { return nil }
func (NoopEventSearcher) ListEventIDs(context.Context) ([]int64, error)    { return nil, nil }
func (NoopEventSearcher) Search(context.Context, Query) ([]int64, int64, error) {
	return nil, 0, fmt.Errorf("elasticsearch disabled")
}

type ESEventSearcher struct {
	client *elasticsearch.Client
	index  string
}

func NewESEventSearcher(addresses []string, username, password, index string) (*ESEventSearcher, error) {
	if index == "" {
		index = "fuchang_events"
	}
	cfg := elasticsearch.Config{Addresses: addresses}
	if username != "" {
		cfg.Username = username
		cfg.Password = password
	}
	client, err := elasticsearch.NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return &ESEventSearcher{client: client, index: index}, nil
}

func (s *ESEventSearcher) Enabled() bool { return s != nil && s.client != nil }

func (s *ESEventSearcher) EnsureIndex(ctx context.Context) error {
	res, err := s.client.Indices.Exists([]string{s.index}, s.client.Indices.Exists.WithContext(ctx))
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 200 {
		return nil
	}
	body := map[string]interface{}{
		"settings": map[string]interface{}{
			"analysis": map[string]interface{}{
				"analyzer": map[string]interface{}{
					// 无 IK 插件时用 ngram 覆盖中文子串，行为接近 MySQL LIKE。
					"fuchang_ngram": map[string]interface{}{
						"tokenizer": "fuchang_ngram_tokenizer",
						"filter":    []string{"lowercase"},
					},
				},
				"tokenizer": map[string]interface{}{
					"fuchang_ngram_tokenizer": map[string]interface{}{
						"type":     "ngram",
						"min_gram": 1,
						"max_gram": 2,
					},
				},
			},
		},
		"mappings": map[string]interface{}{
			"properties": map[string]interface{}{
				"id":           map[string]string{"type": "keyword"},
				"title":        map[string]string{"type": "text", "analyzer": "fuchang_ngram"},
				"subtitle":     map[string]string{"type": "text", "analyzer": "fuchang_ngram"},
				"category":     map[string]string{"type": "keyword"},
				"status":       map[string]string{"type": "keyword"},
				"city":         map[string]string{"type": "keyword"},
				"venue_text":   map[string]string{"type": "text", "analyzer": "fuchang_ngram"},
				"search_text":  map[string]string{"type": "text", "analyzer": "fuchang_ngram"},
				"published_at": map[string]string{"type": "date"},
			},
		},
	}
	raw, _ := json.Marshal(body)
	create, err := s.client.Indices.Create(
		s.index,
		s.client.Indices.Create.WithContext(ctx),
		s.client.Indices.Create.WithBody(bytes.NewReader(raw)),
	)
	if err != nil {
		return err
	}
	defer create.Body.Close()
	if create.IsError() {
		b, _ := io.ReadAll(create.Body)
		// 并发创建时 400 resource_already_exists 可忽略
		if create.StatusCode == 400 && strings.Contains(string(b), "resource_already_exists") {
			return nil
		}
		return fmt.Errorf("create index: %s", string(b))
	}
	return nil
}

func (s *ESEventSearcher) ResetIndex(ctx context.Context) error {
	req := esapi.IndicesDeleteRequest{Index: []string{s.index}}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	if res.StatusCode != 404 && res.IsError() {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return fmt.Errorf("delete index: %s", string(b))
	}
	res.Body.Close()
	return s.EnsureIndex(ctx)
}

func (s *ESEventSearcher) IndexEvent(ctx context.Context, doc EventDoc, refresh bool) error {
	raw, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	refreshMode := "false"
	if refresh {
		refreshMode = "true"
	}
	req := esapi.IndexRequest{
		Index:      s.index,
		DocumentID: doc.ID,
		Body:       bytes.NewReader(raw),
		Refresh:    refreshMode,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("index event: %s", string(b))
	}
	return nil
}

func (s *ESEventSearcher) RefreshIndex(ctx context.Context) error {
	req := esapi.IndicesRefreshRequest{Index: []string{s.index}}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("refresh index: %s", string(b))
	}
	return nil
}

func (s *ESEventSearcher) DeleteEvent(ctx context.Context, eventID int64, refresh bool) error {
	refreshMode := "false"
	if refresh {
		refreshMode = "true"
	}
	req := esapi.DeleteRequest{
		Index:      s.index,
		DocumentID: strconv.FormatInt(eventID, 10),
		Refresh:    refreshMode,
	}
	res, err := req.Do(ctx, s.client)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return nil
	}
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return fmt.Errorf("delete event: %s", string(b))
	}
	return nil
}

func (s *ESEventSearcher) ListEventIDs(ctx context.Context) ([]int64, error) {
	const pageSize = 500
	var (
		ids         []int64
		searchAfter []interface{}
	)
	for {
		body := map[string]interface{}{
			"size":    pageSize,
			"_source": false,
			"query":   map[string]interface{}{"match_all": map[string]interface{}{}},
			"sort":    []map[string]string{{"id": "asc"}},
		}
		if len(searchAfter) > 0 {
			body["search_after"] = searchAfter
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		res, err := s.client.Search(
			s.client.Search.WithContext(ctx),
			s.client.Search.WithIndex(s.index),
			s.client.Search.WithBody(bytes.NewReader(raw)),
		)
		if err != nil {
			return nil, err
		}
		if res.IsError() {
			b, _ := io.ReadAll(res.Body)
			res.Body.Close()
			return nil, fmt.Errorf("list event ids: %s", string(b))
		}
		var parsed struct {
			Hits struct {
				Hits []struct {
					ID   string        `json:"_id"`
					Sort []interface{} `json:"sort"`
				} `json:"hits"`
			} `json:"hits"`
		}
		if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
			res.Body.Close()
			return nil, err
		}
		res.Body.Close()
		if len(parsed.Hits.Hits) == 0 {
			return ids, nil
		}
		for _, hit := range parsed.Hits.Hits {
			id, err := strconv.ParseInt(hit.ID, 10, 64)
			if err == nil {
				ids = append(ids, id)
			}
		}
		lastSort := parsed.Hits.Hits[len(parsed.Hits.Hits)-1].Sort
		if len(lastSort) == 0 {
			return nil, fmt.Errorf("list event ids: missing search_after sort value")
		}
		searchAfter = lastSort
		if len(parsed.Hits.Hits) < pageSize {
			return ids, nil
		}
	}
}

func (s *ESEventSearcher) Search(ctx context.Context, q Query) ([]int64, int64, error) {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 {
		q.PageSize = 12
	}
	must := make([]map[string]interface{}, 0, 4)
	if kw := strings.TrimSpace(q.Keyword); kw != "" {
		must = append(must, map[string]interface{}{
			"multi_match": map[string]interface{}{
				"query":  kw,
				"fields": []string{"title^3", "subtitle^2", "search_text", "venue_text", "category"},
			},
		})
	}
	filter := []map[string]interface{}{
		{"term": map[string]interface{}{"status": "published"}},
	}
	if q.City != "" {
		filter = append(filter, map[string]interface{}{
			"term": map[string]interface{}{"city": q.City},
		})
	}
	if len(q.Categories) == 1 {
		filter = append(filter, map[string]interface{}{
			"term": map[string]interface{}{"category": q.Categories[0]},
		})
	} else if len(q.Categories) > 1 {
		filter = append(filter, map[string]interface{}{
			"terms": map[string]interface{}{"category": q.Categories},
		})
	}
	boolQuery := map[string]interface{}{}
	if len(must) > 0 {
		boolQuery["must"] = must
	}
	if len(filter) > 0 {
		boolQuery["filter"] = filter
	}
	if len(must) == 0 {
		boolQuery["must"] = []map[string]interface{}{{"match_all": map[string]interface{}{}}}
	}
	body := map[string]interface{}{
		"from": (q.Page - 1) * q.PageSize,
		"size": q.PageSize,
		"query": map[string]interface{}{
			"bool": boolQuery,
		},
		"sort": []map[string]interface{}{
			{"_score": map[string]string{"order": "desc"}},
			{"published_at": map[string]string{"order": "desc"}},
		},
	}
	raw, _ := json.Marshal(body)
	res, err := s.client.Search(
		s.client.Search.WithContext(ctx),
		s.client.Search.WithIndex(s.index),
		s.client.Search.WithBody(bytes.NewReader(raw)),
		s.client.Search.WithTrackTotalHits(true),
	)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	if res.IsError() {
		b, _ := io.ReadAll(res.Body)
		return nil, 0, fmt.Errorf("search: %s", string(b))
	}
	var parsed struct {
		Hits struct {
			Total struct {
				Value int64 `json:"value"`
			} `json:"total"`
			Hits []struct {
				ID string `json:"_id"`
			} `json:"hits"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return nil, 0, err
	}
	ids := make([]int64, 0, len(parsed.Hits.Hits))
	for _, hit := range parsed.Hits.Hits {
		id, err := strconv.ParseInt(hit.ID, 10, 64)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, parsed.Hits.Total.Value, nil
}

// BuildEventDoc 从已 Preload Sessions.Venue 的活动构造索引文档。
func BuildEventDoc(eventID int64, title, subtitle, category, status string, publishedAt *time.Time, cities, venues []string) EventDoc {
	venueText := strings.Join(venues, " ")
	parts := []string{title, subtitle, category, strings.Join(cities, " "), venueText}
	doc := EventDoc{
		ID:         strconv.FormatInt(eventID, 10),
		Title:      title,
		Subtitle:   subtitle,
		Category:   category,
		Status:     status,
		City:       append([]string(nil), cities...),
		VenueText:  venueText,
		SearchText: strings.TrimSpace(strings.Join(parts, " ")),
	}
	if publishedAt != nil {
		doc.PublishedAt = *publishedAt
	}
	return doc
}
