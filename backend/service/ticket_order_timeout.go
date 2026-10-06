package service

import (
	"errors"
)

type paymentTimeoutErrorClass string

const (
	paymentTimeoutErrorNone      paymentTimeoutErrorClass = "none"
	paymentTimeoutErrorPermanent paymentTimeoutErrorClass = "permanent"
	paymentTimeoutErrorTransient paymentTimeoutErrorClass = "transient"
)

func classifyPaymentTimeoutError(err error) paymentTimeoutErrorClass {
	if err == nil {
		return paymentTimeoutErrorNone
	}
	if errors.Is(err, ErrTicketOrderNonRetryable) {
		return paymentTimeoutErrorPermanent
	}
	return paymentTimeoutErrorTransient
}
