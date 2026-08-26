package models

import "testing"

func TestParseEventSaleMode(t *testing.T) {
	t.Parallel()
	counter, err := ParseEventSaleMode("")
	if err != nil || counter != EventSaleModeCounter {
		t.Fatalf("empty => %q %v", counter, err)
	}
	seated, err := ParseEventSaleMode("seated")
	if err != nil || !seated.IsSeated() {
		t.Fatalf("seated => %q %v", seated, err)
	}
	if _, err := ParseEventSaleMode("mix"); err == nil {
		t.Fatal("expected invalid sale mode")
	}
}
