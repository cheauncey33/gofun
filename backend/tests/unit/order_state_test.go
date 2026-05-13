package unit

import (
	"WHU_Snack_GO/models"
	"testing"
)

// TestOrderStateMachine_FullPathway 测试完整订单生命周期
func TestOrderStateMachine_FullPathway(t *testing.T) {
	// 正常流程: pending → paid → delivering → delivered → refunding → refunded
	pathway := []models.OrderStatus{
		models.OrderStatusPaid,
		models.OrderStatusDelivering,
		models.OrderStatusDelivered,
		models.OrderStatusRefunding,
		models.OrderStatusRefunded,
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

// TestOrderStateMachine_CancelFromDelivering 配送中可直接取消
func TestOrderStateMachine_CancelFromDelivering(t *testing.T) {
	if !models.OrderStatusDelivering.CanTransitionTo(models.OrderStatusCancelled) {
		t.Error("delivering should be cancellable")
	}
}

// TestOrderStateMachine_CancelFromDelivered 已完成不可直接取消, 只能走退款
func TestOrderStateMachine_CancelFromDelivered(t *testing.T) {
	if models.OrderStatusDelivered.CanTransitionTo(models.OrderStatusCancelled) {
		t.Error("delivered should NOT be cancellable directly, must go through refunding")
	}
	if !models.OrderStatusDelivered.CanTransitionTo(models.OrderStatusRefunding) {
		t.Error("delivered should be transitionable to refunding")
	}
}

// TestOrderStateMachine_TerminalStates 终态不可再转换
func TestOrderStateMachine_TerminalStates(t *testing.T) {
	terminalStates := []models.OrderStatus{
		models.OrderStatusCancelled,
		models.OrderStatusRefunded,
	}

	allStates := []models.OrderStatus{
		models.OrderStatusPending,
		models.OrderStatusPaid,
		models.OrderStatusDelivering,
		models.OrderStatusDelivered,
		models.OrderStatusCancelled,
		models.OrderStatusRefunding,
		models.OrderStatusRefunded,
	}

	for _, terminal := range terminalStates {
		for _, target := range allStates {
			if terminal.CanTransitionTo(target) {
				t.Errorf("terminal state %s should not transition to any state, but allowed %s",
					terminal.String(), target.String())
			}
		}
	}
}

// TestOrderStateMachine_DeliveredOnlyRefunding 已完成只能走退款流程
func TestOrderStateMachine_DeliveredOnlyRefunding(t *testing.T) {
	allowed := []models.OrderStatus{models.OrderStatusRefunding}
	for _, s := range allStatuses() {
		expect := contains(allowed, s)
		got := models.OrderStatusDelivered.CanTransitionTo(s)
		if got != expect {
			t.Errorf("OrderStatusDelivered.CanTransitionTo(%s) = %v, want %v",
				s.String(), got, expect)
		}
	}
}

func allStatuses() []models.OrderStatus {
	return []models.OrderStatus{
		models.OrderStatusPending,
		models.OrderStatusPaid,
		models.OrderStatusDelivering,
		models.OrderStatusDelivered,
		models.OrderStatusCancelled,
		models.OrderStatusRefunding,
		models.OrderStatusRefunded,
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
