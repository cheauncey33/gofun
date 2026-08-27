package service

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var (
	ErrPaymentSignature           = errors.New("payment callback signature invalid")
	ErrPaymentNotFound            = errors.New("payment transaction not found")
	ErrPaymentAmountMismatch      = errors.New("payment amount mismatch")
	ErrPaymentNotRefundable       = errors.New("payment is not refundable")
	ErrPaymentInvalidNotification = errors.New("payment callback notification invalid")
)

type PaymentCreateRequest struct {
	OrderID     int64
	WaitlistID  int64
	UserID      int64
	AmountCents int64
	ExpiresAt   time.Time
	Scenario    string
}

type PaymentIntent struct {
	PaymentNo   string    `json:"payment_no"`
	Provider    string    `json:"provider"`
	Status      string    `json:"status"`
	AmountCents int64     `json:"amount_cents"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type PaymentNotification struct {
	ProviderEventID string `json:"provider_event_id"`
	PaymentNo       string `json:"payment_no"`
	Provider        string `json:"provider"`
	Status          string `json:"status"`
	AmountCents     int64  `json:"amount_cents"`
	FailureReason   string `json:"failure_reason,omitempty"`
	Signature       string `json:"signature"`
}

// PaymentCallbackHandler is deliberately independent from the provider. A
// real Alipay/WeChat adapter can call the same handler from its HTTP webhook.
type PaymentCallbackHandler func(context.Context, PaymentNotification) error

type PaymentGateway interface {
	CreatePayment(context.Context, PaymentCreateRequest) (*PaymentIntent, error)
	ScheduleCallback(paymentNo, scenario string)
	Refund(ctx context.Context, paymentNo string, amountCents int64) error
	VerifyNotification(PaymentNotification) bool
}

// PaymentStateRestorer lets the application rebuild a sandbox provider's
// in-memory view from the durable platform transaction after a restart.
type PaymentStateRestorer interface {
	RestorePayment(PaymentStateRestoreRequest) error
}

type PaymentStateRestoreRequest struct {
	PaymentNo   string
	OrderID     int64
	WaitlistID  int64
	UserID      int64
	AmountCents int64
	Status      string
}

type sandboxPaymentState struct {
	OrderID     int64
	WaitlistID  int64
	UserID      int64
	AmountCents int64
	Status      string
}

// SandboxPaymentGateway emulates an external payment provider. It owns only
// provider-side transaction state in memory; it never reads or changes users'
// balance_cents in the ticketing database.
type SandboxPaymentGateway struct {
	mu       sync.Mutex
	secret   []byte
	delay    time.Duration
	provider string
	byNo       map[string]sandboxPaymentState
	byOrder    map[int64]string
	byWaitlist map[int64]string
	callback   PaymentCallbackHandler
}

func NewSandboxPaymentGateway(secret string, delay time.Duration) *SandboxPaymentGateway {
	if strings.TrimSpace(secret) == "" {
		// An omitted secret is ephemeral, so callbacks from a previous process
		// cannot be replayed after restart.
		secret = uuid.NewString()
	}
	if delay <= 0 {
		delay = 500 * time.Millisecond
	}
	return &SandboxPaymentGateway{
		secret:     []byte(secret),
		delay:      delay,
		provider:   "sandbox",
		byNo:       make(map[string]sandboxPaymentState),
		byOrder:    make(map[int64]string),
		byWaitlist: make(map[int64]string),
	}
}

func (g *SandboxPaymentGateway) SetCallback(callback PaymentCallbackHandler) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.callback = callback
}

func (g *SandboxPaymentGateway) CreatePayment(
	_ context.Context,
	req PaymentCreateRequest,
) (*PaymentIntent, error) {
	if (req.OrderID <= 0 && req.WaitlistID <= 0) || req.UserID <= 0 || req.AmountCents <= 0 {
		return nil, fmt.Errorf("invalid payment request")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if req.WaitlistID > 0 {
		if paymentNo := g.byWaitlist[req.WaitlistID]; paymentNo != "" {
			if state, ok := g.byNo[paymentNo]; ok && state.Status == "pending" {
				return &PaymentIntent{
					PaymentNo: paymentNo, Provider: g.provider, Status: state.Status,
					AmountCents: state.AmountCents, ExpiresAt: req.ExpiresAt,
				}, nil
			}
		}
	} else if paymentNo := g.byOrder[req.OrderID]; paymentNo != "" {
		if state, ok := g.byNo[paymentNo]; ok && state.Status == "pending" {
			return &PaymentIntent{
				PaymentNo: paymentNo, Provider: g.provider, Status: state.Status,
				AmountCents: state.AmountCents, ExpiresAt: req.ExpiresAt,
			}, nil
		}
	}
	paymentNo := "sandbox_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	g.byNo[paymentNo] = sandboxPaymentState{
		OrderID: req.OrderID, WaitlistID: req.WaitlistID, UserID: req.UserID,
		AmountCents: req.AmountCents, Status: "pending",
	}
	if req.WaitlistID > 0 {
		g.byWaitlist[req.WaitlistID] = paymentNo
	} else {
		g.byOrder[req.OrderID] = paymentNo
	}
	return &PaymentIntent{
		PaymentNo: paymentNo, Provider: g.provider, Status: "pending",
		AmountCents: req.AmountCents, ExpiresAt: req.ExpiresAt,
	}, nil
}

func (g *SandboxPaymentGateway) RestorePayment(req PaymentStateRestoreRequest) error {
	if strings.TrimSpace(req.PaymentNo) == "" || req.UserID <= 0 || req.AmountCents <= 0 ||
		(req.OrderID <= 0 && req.WaitlistID <= 0) {
		return fmt.Errorf("invalid payment restore request")
	}
	status := strings.ToLower(strings.TrimSpace(req.Status))
	switch status {
	case "pending", "success", "failed", "closed", "refunded":
	default:
		return fmt.Errorf("invalid payment restore status: %s", req.Status)
	}

	g.mu.Lock()
	defer g.mu.Unlock()
	if existing, ok := g.byNo[req.PaymentNo]; ok {
		if existing.OrderID != req.OrderID || existing.WaitlistID != req.WaitlistID || existing.AmountCents != req.AmountCents {
			return fmt.Errorf("payment restore conflicts with existing provider state")
		}
		return nil
	}
	g.byNo[req.PaymentNo] = sandboxPaymentState{
		OrderID: req.OrderID, WaitlistID: req.WaitlistID, UserID: req.UserID,
		AmountCents: req.AmountCents, Status: status,
	}
	if req.WaitlistID > 0 {
		g.byWaitlist[req.WaitlistID] = req.PaymentNo
	} else {
		g.byOrder[req.OrderID] = req.PaymentNo
	}
	return nil
}

func normalizePaymentScenario(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "failed", "timeout", "manual":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return "success"
	}
}

func (g *SandboxPaymentGateway) ScheduleCallback(paymentNo, scenario string) {
	scenario = normalizePaymentScenario(scenario)
	if scenario == "timeout" || scenario == "manual" {
		return
	}
	time.AfterFunc(g.delay, func() {
		g.emitCallback(paymentNo, scenario)
	})
}

func (g *SandboxPaymentGateway) emitCallback(paymentNo, scenario string) {
	g.mu.Lock()
	state, ok := g.byNo[paymentNo]
	callback := g.callback
	if !ok || state.Status != "pending" || callback == nil {
		g.mu.Unlock()
		return
	}
	status := "success"
	reason := ""
	if scenario == "failed" {
		status = "failed"
		reason = "支付未完成"
	}
	state.Status = status
	g.byNo[paymentNo] = state
	eventID := "sandbox_evt_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	notification := PaymentNotification{
		ProviderEventID: eventID, PaymentNo: paymentNo, Provider: g.provider,
		Status: status, AmountCents: state.AmountCents, FailureReason: reason,
	}
	notification.Signature = g.sign(notification)
	g.mu.Unlock()
	if err := callback(context.Background(), notification); err != nil {
		log.Printf("sandbox payment callback %s: %v", paymentNo, err)
	}
}

func (g *SandboxPaymentGateway) Refund(_ context.Context, paymentNo string, amountCents int64) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	state, ok := g.byNo[paymentNo]
	if !ok {
		return ErrPaymentNotFound
	}
	if state.AmountCents != amountCents {
		return ErrPaymentAmountMismatch
	}
	if state.Status == "refunded" {
		return nil
	}
	if state.Status != "success" {
		return ErrPaymentNotRefundable
	}
	state.Status = "refunded"
	g.byNo[paymentNo] = state
	return nil
}

func (g *SandboxPaymentGateway) VerifyNotification(notification PaymentNotification) bool {
	if notification.Provider != g.provider || notification.ProviderEventID == "" ||
		notification.PaymentNo == "" || notification.AmountCents <= 0 ||
		(notification.Status != "success" && notification.Status != "failed") {
		return false
	}
	return hmac.Equal([]byte(g.sign(notification)), []byte(notification.Signature))
}

func (g *SandboxPaymentGateway) sign(notification PaymentNotification) string {
	mac := hmac.New(sha256.New, g.secret)
	fmt.Fprintf(mac, "%s|%s|%s|%s|%d", notification.Provider,
		notification.ProviderEventID,
		notification.PaymentNo, notification.Status, notification.AmountCents)
	return hex.EncodeToString(mac.Sum(nil))
}
