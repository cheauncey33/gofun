package service

import (
	"errors"
	"fmt"
	"testing"
)

func TestClassifyPaymentTimeoutError(t *testing.T) {
	t.Parallel()
	if classifyPaymentTimeoutError(nil) != paymentTimeoutErrorNone {
		t.Fatal("nil error classification")
	}
	if classifyPaymentTimeoutError(errors.New("temporary database error")) != paymentTimeoutErrorTransient {
		t.Fatal("transient error classification")
	}
	permanent := fmt.Errorf("%w: 订单明细异常", ErrTicketOrderNonRetryable)
	if classifyPaymentTimeoutError(permanent) != paymentTimeoutErrorPermanent {
		t.Fatal("permanent error classification")
	}
	if got := classifyPaymentTimeoutError(permanent); got != paymentTimeoutErrorPermanent {
		t.Fatalf("permanent classification = %q", got)
	}
	if got := classifyPaymentTimeoutError(errors.New("redis unavailable")); got != paymentTimeoutErrorTransient {
		t.Fatalf("transient classification = %q", got)
	}
}
