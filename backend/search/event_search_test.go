package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/elastic/go-elasticsearch/v8"
)

func TestBuildEventDocConcatenatesSearchText(t *testing.T) {
	t.Parallel()
	doc := BuildEventDoc(
		9, "夏夜音乐节", "江滩", "音乐现场", "published", nil,
		[]string{"武汉", "上海"}, []string{"客厅里", "光谷"},
	)
	if doc.ID != "9" || doc.Status != "published" {
		t.Fatalf("unexpected doc: %#v", doc)
	}
	if len(doc.City) != 2 || doc.City[0] != "武汉" || doc.City[1] != "上海" {
		t.Fatalf("expected all cities to be indexed, got %#v", doc.City)
	}
	if doc.SearchText == "" || doc.VenueText == "" {
		t.Fatalf("expected searchable text, got %#v", doc)
	}
	if !strings.Contains(doc.SearchText, "武汉 上海") {
		t.Fatalf("expected every city in search text, got %q", doc.SearchText)
	}
}

func TestNoopSearcherDisabled(t *testing.T) {
	t.Parallel()
	var s EventSearcher = NoopEventSearcher{}
	if s.Enabled() {
		t.Fatal("noop should be disabled")
	}
}

func TestSearchFiltersPublishedStatusAndAnyIndexedCity(t *testing.T) {
	t.Parallel()
	var requestBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Errorf("decode search body: %v", err)
		}
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hits":{"total":{"value":0},"hits":[]}}`))
	}))
	defer server.Close()

	client, err := elasticsearch.NewClient(elasticsearch.Config{Addresses: []string{server.URL}})
	if err != nil {
		t.Fatalf("new ES client: %v", err)
	}
	searcher := &ESEventSearcher{client: client, index: "events"}
	if _, _, err := searcher.Search(context.Background(), Query{
		Keyword: "巡演",
		City:    "上海",
		Page:    1,
		PageSize: 12,
	}); err != nil {
		t.Fatalf("Search() error = %v", err)
	}

	query, ok := requestBody["query"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing query body: %#v", requestBody)
	}
	boolQuery, ok := query["bool"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing bool query: %#v", query)
	}
	filters, ok := boolQuery["filter"].([]interface{})
	if !ok {
		t.Fatalf("missing filters: %#v", boolQuery)
	}
	terms := make(map[string]interface{})
	for _, rawFilter := range filters {
		filter, _ := rawFilter.(map[string]interface{})
		term, _ := filter["term"].(map[string]interface{})
		for field, value := range term {
			terms[field] = value
		}
	}
	if terms["status"] != "published" || terms["city"] != "上海" {
		t.Fatalf("unexpected term filters: %#v", terms)
	}
}
