package service

import "testing"

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
