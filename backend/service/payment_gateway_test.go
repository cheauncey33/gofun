package service

import (
	"context"
	"testing"
	"time"
)

func TestSandboxPaymentGatewaySignsProviderAndRejectsTampering(t *testing.T) {
	gateway := NewSandboxPaymentGateway("integration-payment-secret", time.Millisecond)
	notification := PaymentNotification{
		ProviderEventID: "evt_1",
		PaymentNo:       "sandbox_1",
		Provider:        "sandbox",
		Status:          "success",
		AmountCents:     6800,
	}
	notification.Signature = gateway.sign(notification)
	if !gateway.VerifyNotification(notification) {
		t.Fatal("expected valid sandbox signature")
	}
	notification.AmountCents++
	if gateway.VerifyNotification(notification) {
		t.Fatal("expected amount tampering to invalidate signature")
	}
}

func TestSandboxPaymentGatewayUsesEphemeralSecretWhenUnset(t *testing.T) {
	first := NewSandboxPaymentGateway("", time.Millisecond)
	second := NewSandboxPaymentGateway("", time.Millisecond)
	notification := PaymentNotification{
		ProviderEventID: "evt_1",
		PaymentNo:       "sandbox_1",
		Provider:        "sandbox",
		Status:          "failed",
		AmountCents:     1,
	}
	notification.Signature = first.sign(notification)
	if second.VerifyNotification(notification) {
		t.Fatal("expected restart with an unset secret to reject old callbacks")
	}
}

func TestSandboxPaymentGatewayRestoresStateAndSchedulesCallback(t *testing.T) {
	gateway := NewSandboxPaymentGateway("restart-secret", time.Millisecond)
	notifications := make(chan PaymentNotification, 1)
	gateway.SetCallback(func(_ context.Context, notification PaymentNotification) error {
		notifications <- notification
		return nil
	})

	if err := gateway.RestorePayment(PaymentStateRestoreRequest{
		PaymentNo: "sandbox_restart_1", OrderID: 101, UserID: 202,
		AmountCents: 6800, Status: "pending",
	}); err != nil {
		t.Fatalf("RestorePayment() error = %v", err)
	}
	gateway.ScheduleCallback("sandbox_restart_1", "success")

	select {
	case notification := <-notifications:
		if notification.PaymentNo != "sandbox_restart_1" || notification.Status != "success" {
			t.Fatalf("unexpected restored callback: %+v", notification)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("expected callback after restoring pending payment")
	}
}
