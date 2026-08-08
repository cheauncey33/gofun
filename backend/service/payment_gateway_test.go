package service

import (
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
