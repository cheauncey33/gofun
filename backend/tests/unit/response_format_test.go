package unit

import (
	"WHU_Snack_GO/models"
	"encoding/json"
	"testing"
)

// TestResponse_ProductJSON verifies product serialization includes new fields
func TestResponse_ProductJSON(t *testing.T) {
	p := models.Product{
		Name:        "Test Snack",
		Description: "Delicious snack",
		Price:       9.99,
		Stock:       100,
		ImageURL:    "http://example.com/img.png",
		Status:      models.ProductStatusOnSale,
		SalesCount:  42,
	}

	bytes, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(bytes, &m)

	// Verify new fields exist in JSON output
	fields := []string{"name", "description", "price", "stock", "image_url", "status", "sales_count"}
	for _, f := range fields {
		if _, ok := m[f]; !ok {
			t.Errorf("expected field %q in product JSON", f)
		}
	}
}

// TestResponse_OrderJSON verifies order status serializes as int
func TestResponse_OrderJSON(t *testing.T) {
	o := models.Order{
		UserID:     1,
		TotalPrice: 99.9,
		Status:     models.OrderStatusPending,
	}

	bytes, err := json.Marshal(o)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(bytes, &m)

	if status, ok := m["status"].(float64); !ok || int(status) != 1 {
		t.Errorf("expected status 1 in order JSON, got %v", m["status"])
	}
}

// TestResponse_UserHidesPassword verifies password is not in JSON
func TestResponse_UserHidesPassword(t *testing.T) {
	u := models.User{
		Username: "testuser",
		Password: "secret",
		Balance:  100.0,
		Role:     "user",
	}

	bytes, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var m map[string]interface{}
	json.Unmarshal(bytes, &m)

	if _, ok := m["password"]; ok {
		t.Error("password field must not appear in JSON (json:\"-\" tag)")
	}
}

// TestResponse_AddressJSON verifies address JSON structure
func TestResponse_AddressJSON(t *testing.T) {
	a := models.Address{
		UserID:       1,
		ReceiverName: "John",
		Phone:        "13800138000",
		Province:     "Hubei",
		City:         "Wuhan",
		District:     "Wuchang",
		Detail:       "WHU Campus",
		IsDefault:    true,
	}

	bytes, _ := json.Marshal(a)
	var m map[string]interface{}
	json.Unmarshal(bytes, &m)

	if m["receiver_name"] != "John" {
		t.Error("address JSON receiver_name mismatch")
	}
	if m["is_default"] != true {
		t.Error("address JSON is_default mismatch")
	}
}
