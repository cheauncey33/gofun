package models

import (
	"testing"
)

func TestOrderStatus_CanTransitionTo(t *testing.T) {
	tests := []struct {
		name   string
		from   OrderStatus
		to     OrderStatus
		expect bool
	}{
		// 待支付
		{"pending to paid", OrderStatusPending, OrderStatusPaid, true},
		{"pending to cancelled", OrderStatusPending, OrderStatusCancelled, true},
		{"pending to completed", OrderStatusPending, OrderStatusCompleted, false},
		// 已支付
		{"paid to completed", OrderStatusPaid, OrderStatusCompleted, true},
		{"paid to cancelled", OrderStatusPaid, OrderStatusCancelled, true},
		{"paid to pending", OrderStatusPaid, OrderStatusPending, false},
		// 已完成
		{"completed to cancelled", OrderStatusCompleted, OrderStatusCancelled, true},
		{"completed to paid", OrderStatusCompleted, OrderStatusPaid, false},
		{"completed to pending", OrderStatusCompleted, OrderStatusPending, false},
		// 已取消(终态)
		{"cancelled to any", OrderStatusCancelled, OrderStatusPaid, false},
		{"cancelled to completed", OrderStatusCancelled, OrderStatusCompleted, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.from.CanTransitionTo(tt.to)
			if got != tt.expect {
				t.Errorf("CanTransitionTo(%s -> %s) = %v, want %v",
					tt.from.String(), tt.to.String(), got, tt.expect)
			}
		})
	}
}

func TestOrderStatus_String(t *testing.T) {
	tests := []struct {
		status OrderStatus
		str    string
	}{
		{OrderStatusPending, "pending"},
		{OrderStatusPaid, "paid"},
		{OrderStatusCompleted, "completed"},
		{OrderStatusCancelled, "cancelled"},
	}

	for _, tt := range tests {
		if tt.status.String() != tt.str {
			t.Errorf("OrderStatus(%d).String() = %q, want %q", tt.status, tt.status.String(), tt.str)
		}
	}
}

func TestOrderStatus_HasBeenPaid(t *testing.T) {
	tests := []struct {
		status OrderStatus
		paid   bool
	}{
		{OrderStatusPending, false},   // 待支付:从未扣款,取消不退钱
		{OrderStatusCancelled, false}, // 已取消:未付款即取消
		{OrderStatusPaid, true},
		{OrderStatusCompleted, true},
	}

	for _, tt := range tests {
		if got := tt.status.HasBeenPaid(); got != tt.paid {
			t.Errorf("OrderStatus(%s).HasBeenPaid() = %v, want %v", tt.status.String(), got, tt.paid)
		}
	}
}
