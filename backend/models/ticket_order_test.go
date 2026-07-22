package models

import "testing"

func TestTicketOrderStatusTransitions(t *testing.T) {
	tests := []struct {
		name string
		from TicketOrderStatus
		to   TicketOrderStatus
		want bool
	}{
		{"queued to pending payment", TicketOrderStatusQueued, TicketOrderStatusPendingPayment, true},
		{"queued to failed", TicketOrderStatusQueued, TicketOrderStatusFailed, true},
		{"pending payment to paid", TicketOrderStatusPendingPayment, TicketOrderStatusPaid, true},
		{"pending payment to cancelled", TicketOrderStatusPendingPayment, TicketOrderStatusCancelled, true},
		{"paid to cancelled for refund", TicketOrderStatusPaid, TicketOrderStatusCancelled, true},
		{"queued cannot skip payment", TicketOrderStatusQueued, TicketOrderStatusPaid, false},
		{"cancelled is terminal", TicketOrderStatusCancelled, TicketOrderStatusPaid, false},
		{"failed is terminal", TicketOrderStatusFailed, TicketOrderStatusPendingPayment, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.from.CanTransitionTo(tt.to); got != tt.want {
				t.Fatalf("%s -> %s = %v, want %v", tt.from, tt.to, got, tt.want)
			}
		})
	}
}

func TestTicketOrderStatusHasBeenPaid(t *testing.T) {
	if !TicketOrderStatusPaid.HasBeenPaid() {
		t.Fatal("paid order must be treated as paid")
	}
	for _, status := range []TicketOrderStatus{
		TicketOrderStatusQueued,
		TicketOrderStatusPendingPayment,
		TicketOrderStatusCancelled,
		TicketOrderStatusFailed,
	} {
		if status.HasBeenPaid() {
			t.Fatalf("%s must not be treated as paid", status)
		}
	}
}
