package service

import (
	"errors"
	"testing"

	"gofun/models"
)

func TestValidateEventReadyToSell(t *testing.T) {
	t.Parallel()
	if err := validateEventReadyToSell(&models.Event{}); !errors.Is(err, ErrInvalidTicketCatalog) {
		t.Fatalf("empty sessions should fail, err=%v", err)
	}
	event := &models.Event{
		Sessions: []models.EventSession{{TicketTiers: []models.TicketTier{{Name: "A"}}}},
	}
	if err := validateEventReadyToSell(event); err != nil {
		t.Fatal(err)
	}
}

func TestWrapOrganizerWriteError(t *testing.T) {
	t.Parallel()
	err := wrapOrganizerWriteError(errors.New("Error 1062 (23000): Duplicate entry 'acme' for key 'slug'"))
	if !errors.Is(err, ErrInvalidTicketCatalog) {
		t.Fatalf("duplicate should map to invalid catalog, got %v", err)
	}
}
