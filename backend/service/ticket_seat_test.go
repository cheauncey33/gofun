package service

import (
	"encoding/json"
	"errors"
	"testing"

	"gofun/models"
)

func TestParseSeatIDs(t *testing.T) {
	t.Parallel()
	ids, err := parseSeatIDs([]FlexibleID{3, 1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != 1 || ids[2] != 3 {
		t.Fatalf("sorted ids = %v", ids)
	}
	if _, err := parseSeatIDs(nil); !errors.Is(err, ErrInvalidTicketCatalog) {
		t.Fatalf("empty seats: %v", err)
	}
	if _, err := parseSeatIDs([]FlexibleID{1, 1}); !errors.Is(err, ErrInvalidTicketCatalog) {
		t.Fatalf("duplicate seats: %v", err)
	}
}

func TestFlexibleIDJSON(t *testing.T) {
	t.Parallel()
	var fromNumber FlexibleID
	if err := json.Unmarshal([]byte(`17`), &fromNumber); err != nil || fromNumber != 17 {
		t.Fatalf("number: %v %d", err, fromNumber)
	}
	var fromString FlexibleID
	if err := json.Unmarshal([]byte(`"19"`), &fromString); err != nil || fromString != 19 {
		t.Fatalf("string: %v %d", err, fromString)
	}
}

func TestSeatRowLabel(t *testing.T) {
	t.Parallel()
	if got := defaultSeatLabel(1, 12); got != "A12" {
		t.Fatalf("label = %q", got)
	}
	if got := seatRowLabel(27); got != "AA" {
		t.Fatalf("row 27 = %q", got)
	}
}

func TestOverlaySessionSeatStatus(t *testing.T) {
	t.Parallel()
	if got := overlaySessionSeatStatus(models.SessionSeatAvailable, true); got != models.SessionSeatAvailable {
		t.Fatalf("on-sale available = %q", got)
	}
	if got := overlaySessionSeatStatus(models.SessionSeatAvailable, false); got != models.SessionSeatOffSale {
		t.Fatalf("disabled available should overlay off_sale, got %q", got)
	}
	if got := overlaySessionSeatStatus(models.SessionSeatHeld, false); got != models.SessionSeatHeld {
		t.Fatalf("held seats stay held, got %q", got)
	}
	if got := overlaySessionSeatStatus(models.SessionSeatSold, false); got != models.SessionSeatSold {
		t.Fatalf("sold seats stay sold, got %q", got)
	}
}
