package service

import (
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/metrics"
	"WHU_Snack_GO/models"
	apptelemetry "WHU_Snack_GO/pkg/telemetry"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

type outboxDraft struct {
	OrderID int64
	Message TicketOrderMessage
}

type outboxWriteBuffer struct {
	mu            sync.Mutex
	items         []outboxDraft
	batchSize     int
	maxBuffer     int
	flushInterval time.Duration
	wake          chan struct{}
	svc           *TicketOrderService
}

func newOutboxWriteBuffer(svc *TicketOrderService, cfg config.OrderOutboxConfig) *outboxWriteBuffer {
	batch := cfg.BufferBatchSize
	if batch <= 0 {
		batch = 50
	}
	maxBuf := cfg.MaxBuffer
	if maxBuf <= 0 {
		maxBuf = 4000
	}
	interval := time.Duration(cfg.FlushIntervalMS) * time.Millisecond
	if interval <= 0 {
		interval = 8 * time.Millisecond
	}
	return &outboxWriteBuffer{
		items:         make([]outboxDraft, 0, batch),
		batchSize:     batch,
		maxBuffer:     maxBuf,
		flushInterval: interval,
		wake:          make(chan struct{}, 1),
		svc:           svc,
	}
}

func (b *outboxWriteBuffer) notify() {
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

func (b *outboxWriteBuffer) containsOrder(orderID int64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, item := range b.items {
		if item.OrderID == orderID {
			return true
		}
	}
	return false
}

// Enqueue 将草稿放入缓冲；达到 batch 或缓冲满时由调用方同步刷盘。
func (b *outboxWriteBuffer) Enqueue(ctx context.Context, draft outboxDraft) error {
	for {
		b.mu.Lock()
		if len(b.items) >= b.maxBuffer {
			batch := b.takeLocked(b.batchSize)
			b.mu.Unlock()
			if len(batch) == 0 {
				return fmt.Errorf("outbox buffer full but empty after lock")
			}
			if err := b.flushRows(ctx, batch); err != nil {
				return err
			}
			continue
		}
		b.items = append(b.items, draft)
		n := len(b.items)
		metrics.OutboxBufferLength.Set(float64(n))
		shouldFlush := n >= b.batchSize
		var batch []outboxDraft
		if shouldFlush {
			batch = b.takeLocked(b.batchSize)
		}
		b.mu.Unlock()
		if shouldFlush {
			return b.flushRows(ctx, batch)
		}
		b.notify()
		return nil
	}
}

func (b *outboxWriteBuffer) takeLocked(limit int) []outboxDraft {
	if limit <= 0 || len(b.items) == 0 {
		return nil
	}
	if limit > len(b.items) {
		limit = len(b.items)
	}
	batch := append([]outboxDraft(nil), b.items[:limit]...)
	b.items = append([]outboxDraft(nil), b.items[limit:]...)
	metrics.OutboxBufferLength.Set(float64(len(b.items)))
	return batch
}

func (b *outboxWriteBuffer) flushAll(ctx context.Context) error {
	for {
		b.mu.Lock()
		batch := b.takeLocked(len(b.items))
		b.mu.Unlock()
		if len(batch) == 0 {
			return nil
		}
		if err := b.flushRows(ctx, batch); err != nil {
			return err
		}
	}
}

func (b *outboxWriteBuffer) flushDue(ctx context.Context) error {
	b.mu.Lock()
	batch := b.takeLocked(b.batchSize)
	b.mu.Unlock()
	if len(batch) == 0 {
		return nil
	}
	return b.flushRows(ctx, batch)
}

func (b *outboxWriteBuffer) flushRows(ctx context.Context, batch []outboxDraft) error {
	if len(batch) == 0 {
		return nil
	}
	rows := make([]models.TicketOrderOutbox, 0, len(batch))
	for _, draft := range batch {
		message := draft.Message
		message.TraceContext = apptelemetry.InjectMap(ctx)
		payload, err := json.Marshal(message)
		if err != nil {
			return err
		}
		rows = append(rows, models.TicketOrderOutbox{
			ID:      b.svc.node.Generate().Int64(),
			OrderID: draft.OrderID,
			Payload: string(payload),
			Status:  models.TicketOrderOutboxPending,
		})
	}
	if err := b.svc.db.WithContext(ctx).CreateInBatches(rows, len(rows)).Error; err != nil {
		b.mu.Lock()
		b.items = append(batch, b.items...)
		if len(b.items) > b.maxBuffer {
			b.items = b.items[:b.maxBuffer]
		}
		metrics.OutboxBufferLength.Set(float64(len(b.items)))
		b.mu.Unlock()
		return err
	}
	metrics.OutboxFlushBatchSize.Observe(float64(len(rows)))
	b.svc.notifyOutboxPublisher()
	return nil
}

func (b *outboxWriteBuffer) Start(ctx context.Context) {
	ticker := time.NewTicker(b.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			_ = b.flushAll(flushCtx)
			cancel()
			return
		case <-ticker.C:
			if err := b.flushDue(ctx); err != nil {
				log.Printf("outbox batch flush: %v", err)
			}
		case <-b.wake:
			if err := b.flushDue(ctx); err != nil {
				log.Printf("outbox batch flush: %v", err)
			}
		}
	}
}

func (s *TicketOrderService) ConfigureOutboxWriter(cfg config.OrderOutboxConfig) {
	mode := strings.ToLower(strings.TrimSpace(cfg.WriteMode))
	if mode == "" {
		mode = "sync"
	}
	s.outboxWriteMode = mode
	s.outboxRecoverEvery = time.Duration(cfg.RecoverIntervalSec) * time.Second
	if mode == "batch" {
		s.outboxBuffer = newOutboxWriteBuffer(s, cfg)
	}
}

func (s *TicketOrderService) StartOutboxWriter(ctx context.Context) {
	if s.outboxWriteMode != "batch" || s.outboxBuffer == nil {
		return
	}
	go s.outboxBuffer.Start(ctx)
}

func (s *TicketOrderService) StartOutboxRecoverScanner(ctx context.Context) {
	interval := s.outboxRecoverEvery
	if interval <= 0 {
		interval = 2 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.RecoverQueuedOrders(ctx); err != nil {
				log.Printf("outbox recover scan: %v", err)
			}
		}
	}
}

func (s *TicketOrderService) enqueueOutboxDraft(ctx context.Context, orderID int64, message TicketOrderMessage) error {
	if s.outboxWriteMode != "batch" || s.outboxBuffer == nil {
		return s.enqueueOutbox(ctx, orderID, message)
	}
	if err := s.outboxBuffer.Enqueue(ctx, outboxDraft{OrderID: orderID, Message: message}); err != nil {
		log.Printf("outbox buffer enqueue failed, fallback sync order=%d: %v", orderID, err)
		return s.enqueueOutbox(ctx, orderID, message)
	}
	return nil
}
