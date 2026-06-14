package unit

import (
	"WHU_Snack_GO/models"
	"testing"
)

// TestOrderStateMachine_FullPathway 测试完整订单生命周期
func TestOrderStateMachine_FullPathway(t *testing.T) {
	// 正常正向流程: pending → paid → completed → cancelled (退款)
	pathway := []models.OrderStatus{
		models.OrderStatusPaid,
		models.OrderStatusCompleted,
		models.OrderStatusCancelled,
	}

	current := models.OrderStatusPending
	for _, next := range pathway {
		if !current.CanTransitionTo(next) {
			t.Errorf("transition %s -> %s should be allowed, but was blocked",
				current.String(), next.String())
		}
		current = next
	}
}

// TestOrderStateMachine_CancelFromPending 待支付可直接取消
func TestOrderStateMachine_CancelFromPending(t *testing.T) {
	if !models.OrderStatusPending.CanTransitionTo(models.OrderStatusCancelled) {
		t.Error("pending should be cancellable directly")
	}
}

// TestOrderStateMachine_CancelFromPaid 已支付可直接取消
func TestOrderStateMachine_CancelFromPaid(t *testing.T) {
	if !models.OrderStatusPaid.CanTransitionTo(models.OrderStatusCancelled) {
		t.Error("paid should be cancellable directly")
	}
}

// TestOrderStateMachine_CancelFromCompleted 已完成可以取消（即退款）
func TestOrderStateMachine_CancelFromCompleted(t *testing.T) {
	if !models.OrderStatusCompleted.CanTransitionTo(models.OrderStatusCancelled) {
		t.Error("completed should be cancellable (refund path)")
	}
}

// TestOrderStateMachine_TerminalState 终态(Cancelled)不可再转换
func TestOrderStateMachine_TerminalState(t *testing.T) {
	allStates := allStatuses()

	for _, target := range allStates {
		if models.OrderStatusCancelled.CanTransitionTo(target) {
			t.Errorf("terminal state cancelled should not transition to %s", target.String())
		}
	}
}

// TestOrderStateMachine_CompletedOnlyCancelled 已完成只能取消
func TestOrderStateMachine_CompletedOnlyCancelled(t *testing.T) {
	for _, s := range allStatuses() {
		expect := s == models.OrderStatusCancelled
		got := models.OrderStatusCompleted.CanTransitionTo(s)
		if got != expect {
			t.Errorf("Completed.CanTransitionTo(%s) = %v, want %v",
				s.String(), got, expect)
		}
	}
}

// TestOrderStateMachine_HasBeenPaid_OnlyPaidAndCompleted 只有 Paid 和 Completed 算已扣款
func TestOrderStateMachine_HasBeenPaid_OnlyPaidAndCompleted(t *testing.T) {
	tests := []struct {
		status models.OrderStatus
		paid   bool
	}{
		{models.OrderStatusPending, false},
		{models.OrderStatusPaid, true},
		{models.OrderStatusCompleted, true},
		{models.OrderStatusCancelled, false},
	}

	for _, tt := range tests {
		if got := tt.status.HasBeenPaid(); got != tt.paid {
			t.Errorf("%s.HasBeenPaid() = %v, want %v", tt.status.String(), got, tt.paid)
		}
	}
}

func allStatuses() []models.OrderStatus {
	return []models.OrderStatus{
		models.OrderStatusPending,
		models.OrderStatusPaid,
		models.OrderStatusCompleted,
		models.OrderStatusCancelled,
	}
}

func contains(slice []models.OrderStatus, item models.OrderStatus) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
