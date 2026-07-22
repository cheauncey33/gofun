package service

import (
	"errors"
	"testing"
)

func TestTicketCredentialSignerRoundTrip(t *testing.T) {
	signer := NewTicketCredentialSigner([]byte("test-ticket-qr-secret"))
	credential := signer.Sign(123456789)
	got, err := signer.Parse(credential)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if got != 123456789 {
		t.Fatalf("ticket id = %d, want 123456789", got)
	}
}

func TestTicketCredentialAcceptsPreviousSecret(t *testing.T) {
	oldSigner := NewTicketCredentialSigner([]byte("old-ticket-qr-secret"))
	credential := oldSigner.Sign(42)
	rotated := NewTicketCredentialSigner(
		[]byte("new-ticket-qr-secret"),
		[]byte("old-ticket-qr-secret"),
	)
	got, err := rotated.Parse(credential)
	if err != nil {
		t.Fatalf("Parse() with previous secret error = %v", err)
	}
	if got != 42 {
		t.Fatalf("ticket id = %d, want 42", got)
	}
}

func TestTicketCredentialSignerRejectsTampering(t *testing.T) {
	signer := NewTicketCredentialSigner([]byte("test-ticket-qr-secret"))
	credential := signer.Sign(123456789)
	credential = credential[:len(credential)-1] + "x"
	if _, err := signer.Parse(credential); !errors.Is(err, ErrTicketCredentialInvalid) {
		t.Fatalf("Parse() error = %v, want ErrTicketCredentialInvalid", err)
	}
}
