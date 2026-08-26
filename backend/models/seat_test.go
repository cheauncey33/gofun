package models

import "testing"

func TestSeatLayoutBoundsOK(t *testing.T) {
	t.Parallel()
	if !SeatLayoutBoundsOK(8, 12) {
		t.Fatal("8x12 should be valid")
	}
	if SeatLayoutBoundsOK(0, 10) || SeatLayoutBoundsOK(17, 10) || SeatLayoutBoundsOK(8, 25) {
		t.Fatal("out of bounds should be rejected")
	}
}

func TestSessionSeatStatusOccupied(t *testing.T) {
	t.Parallel()
	if SessionSeatAvailable.IsOccupied() {
		t.Fatal("available is not occupied")
	}
	if !SessionSeatHeld.IsOccupied() || !SessionSeatSold.IsOccupied() {
		t.Fatal("held and sold are occupied")
	}
}
