package service

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestTicketTimeoutInfraDefaults(t *testing.T) {
	s := &TicketOrderService{}
	infra := s.timeoutInfra()
	if infra.exchange != defaultTicketTimeoutExchange {
		t.Fatalf("exchange = %q", infra.exchange)
	}
	if infra.delayQueue != defaultTicketDelayQueue {
		t.Fatalf("delayQueue = %q", infra.delayQueue)
	}
	if infra.timeoutQ != defaultTicketTimeoutQueue {
		t.Fatalf("timeoutQ = %q", infra.timeoutQ)
	}
	if infra.routingKey != defaultTicketTimeoutRK {
		t.Fatalf("routingKey = %q", infra.routingKey)
	}
}

func TestPaymentTimeoutExpirationMs(t *testing.T) {
	if got := paymentTimeoutExpirationMs(15 * time.Minute); got != "900000" {
		t.Fatalf("15m expiration = %q, want 900000", got)
	}
	if got := paymentTimeoutExpirationMs(500 * time.Millisecond); got != "1000" {
		t.Fatalf("sub-second expiration = %q, want 1000", got)
	}
}

func TestShouldRequeuePaymentTimeout(t *testing.T) {
	t.Parallel()
	if shouldRequeuePaymentTimeout(nil) {
		t.Fatal("nil error must not be requeued")
	}
	if !shouldRequeuePaymentTimeout(errors.New("temporary database error")) {
		t.Fatal("transient error should be requeued")
	}
	permanent := fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
	if shouldRequeuePaymentTimeout(permanent) {
		t.Fatal("permanent error must be acknowledged instead of requeued")
	}
	if got := classifyPaymentTimeoutError(permanent); got != paymentTimeoutErrorPermanent {
		t.Fatalf("permanent classification = %q", got)
	}
	if got := classifyPaymentTimeoutError(errors.New("redis unavailable")); got != paymentTimeoutErrorTransient {
		t.Fatalf("transient classification = %q", got)
	}
}
