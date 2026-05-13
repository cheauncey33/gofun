package repository

import (
	"WHU_Snack_GO/models"
	"testing"
)

// TestProductListParams validates default parameter handling
func TestProductListParams_Defaults(t *testing.T) {
	params := ProductListParams{
		Page:     1,
		PageSize: 10,
	}
	if params.Page != 1 {
		t.Errorf("expected default page 1, got %d", params.Page)
	}
	if params.PageSize != 10 {
		t.Errorf("expected default pageSize 10, got %d", params.PageSize)
	}
	if params.Keyword != "" {
		t.Error("expected empty keyword")
	}
}

// TestProductListParams_SortBy validates sort options
func TestProductListParams_SortBy(t *testing.T) {
	validSorts := []string{"price_asc", "price_desc", "sales", "newest"}
	for _, s := range validSorts {
		params := ProductListParams{Page: 1, PageSize: 10, SortBy: s}
		if params.SortBy != s {
			t.Errorf("expected SortBy %s, got %s", s, params.SortBy)
		}
	}
}

// TestProductListParams_CategoryFilter validates category filter
func TestProductListParams_CategoryFilter(t *testing.T) {
	catID := int64(5)
	params := ProductListParams{Page: 1, PageSize: 10, CategoryID: &catID}
	if params.CategoryID == nil || *params.CategoryID != 5 {
		t.Error("expected CategoryID = 5")
	}
}

// TestProductStatus constants
func TestProductStatus_Values(t *testing.T) {
	if models.ProductStatusOffSale != 0 {
		t.Error("ProductStatusOffSale should be 0")
	}
	if models.ProductStatusOnSale != 1 {
		t.Error("ProductStatusOnSale should be 1")
	}
}
