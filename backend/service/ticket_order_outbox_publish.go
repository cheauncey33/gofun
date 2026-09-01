package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"gofun/config"
	"gofun/metrics"
	"gofun/models"
	apptelemetry "gofun/pkg/telemetry"
	"log"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"

	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// StartOutboxPublisher 启动多个 outbox 投递协程；每人独立 amqp channel，从 MySQL 抢占 pending 行。
func (s *TicketOrderService) StartOutboxPublisher(ctx context.Context, cfg config.OrderOutboxConfig) {
	workers := cfg.PublishWorkers
	if workers <= 0 {
		workers = 4
	}
	batch := cfg.PublishBatch
	if batch <= 0 {
		batch = ticketOutboxPublishBatch
	}
	tick := time.Duration(cfg.TickIntervalMS) * time.Millisecond
	if tick <= 0 {
		tick = ticketOutboxTickInterval
	}

	if err := s.resetStuckOutboxPublishing(ctx); err != nil {
		log.Printf("ticket outbox reset publishing rows: %v", err)
	}

	var wg sync.WaitGroup
	for workerID := 1; workerID <= workers; workerID++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			s.runOutboxPublisher(ctx, id, batch, tick)
		}(workerID)
	}
	<-ctx.Done()
	wg.Wait()
}

func (s *TicketOrderService) runOutboxPublisher(
	ctx context.Context,
	workerID, batch int,
	tick time.Duration,
) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	var ch *amqp.Channel
	defer func() {
		if ch != nil {
			_ = ch.Close()
		}
	}()

	ensureChannel := func() error {
		if ch != nil {
			return nil
		}
		opened, err := s.newOutboxPublishChannel()
		if err != nil {
			return err
		}
		ch = opened
		log.Printf("ticket outbox publisher %d channel ready", workerID)
		return nil
	}

	drain := func() {
		if err := ensureChannel(); err != nil {
			log.Printf("ticket outbox publisher %d open channel: %v", workerID, err)
			return
		}
		for ctx.Err() == nil {
			n, err := s.publishClaimedOutboxBatch(ctx, ch, batch)
			if err != nil {
				log.Printf("ticket outbox publisher %d: %v", workerID, err)
				_ = ch.Close()
				ch = nil
				return
			}
			if n == 0 {
				return
			}
		}
	}

	drain()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			drain()
		case <-s.outboxNotify:
			drain()
		}
	}
}

func (s *TicketOrderService) newOutboxPublishChannel() (*amqp.Channel, error) {
	if s.newMQChannel == nil {
		return nil, fmt.Errorf("rabbitMQ channel factory is not initialized")
	}
	ch, err := s.newMQChannel()
	if err != nil {
		return nil, err
	}
	if err := ch.Confirm(false); err != nil {
		_ = ch.Close()
		return nil, fmt.Errorf("开启 outbox 发布确认失败: %w", err)
	}
	return ch, nil
}

func (s *TicketOrderService) resetStuckOutboxPublishing(ctx context.Context) error {
	return s.asyncDB().WithContext(ctx).Model(&models.TicketOrderOutbox{}).
		Where("status = ?", models.TicketOrderOutboxPublishing).
		Update("status", models.TicketOrderOutboxPending).Error
}

func (s *TicketOrderService) claimOutboxBatch(
	ctx context.Context,
	batch int,
) ([]models.TicketOrderOutbox, error) {
	var rows []models.TicketOrderOutbox
	err := s.asyncDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
			Where("status = ?", models.TicketOrderOutboxPending).
			Order("create_time ASC").
			Limit(batch).
			Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		ids := make([]int64, 0, len(rows))
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		res := tx.Model(&models.TicketOrderOutbox{}).
			Where("id IN ? AND status = ?", ids, models.TicketOrderOutboxPending).
			Update("status", models.TicketOrderOutboxPublishing)
		if res.Error != nil {
			return res.Error
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Status = models.TicketOrderOutboxPublishing
	}
	return rows, nil
}

func (s *TicketOrderService) publishClaimedOutboxBatch(
	ctx context.Context,
	ch *amqp.Channel,
	batch int,
) (int, error) {
	rows, err := s.claimOutboxBatch(ctx, batch)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 {
		return 0, nil
	}

	type pendingConfirm struct {
		row     models.TicketOrderOutbox
		confirm *amqp.DeferredConfirmation
	}
	pending := make([]pendingConfirm, 0, len(rows))
	for _, row := range rows {
		var message TicketOrderMessage
		parentCtx := ctx
		if err := json.Unmarshal([]byte(row.Payload), &message); err != nil {
			return 0, fmt.Errorf("解析 outbox event=%d: %w", row.ID, err)
		}
		if message.EventID != row.ID || message.OrderID != row.OrderID || message.EventType != row.EventType {
			return 0, fmt.Errorf("outbox event=%d 载荷身份不匹配", row.ID)
		}
		parentCtx = apptelemetry.ExtractMap(ctx, message.TraceContext)
		confirm, pubErr := ch.PublishWithDeferredConfirmWithContext(
			parentCtx,
			"",
			s.mqQueueName,
			true,
			false,
			amqp.Publishing{
				ContentType:   "application/json",
				Body:          []byte(row.Payload),
				DeliveryMode:  amqp.Persistent,
				Headers:       apptelemetry.InjectAMQP(parentCtx),
				MessageId:     strconv.FormatInt(message.EventID, 10),
				CorrelationId: strconv.FormatInt(message.OrderID, 10),
				Type:          message.EventType,
			},
		)
		if pubErr != nil {
			_ = s.markOutboxPublishResult(ctx, row, pubErr)
			_ = s.releaseClaimedOutboxRows(ctx, rows)
			return 0, pubErr
		}
		if confirm == nil {
			err := fmt.Errorf("rabbitMQ publish confirm is not enabled")
			_ = s.markOutboxPublishResult(ctx, row, err)
			return 0, err
		}
		pending = append(pending, pendingConfirm{row: row, confirm: confirm})
	}

	for _, item := range pending {
		waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		acked, waitErr := item.confirm.WaitContext(waitCtx)
		cancel()
		var publishErr error
		if waitErr != nil {
			publishErr = waitErr
		} else if !acked {
			publishErr = fmt.Errorf("rabbitMQ publish not acknowledged")
		}
		if err := s.markOutboxPublishResult(ctx, item.row, publishErr); err != nil {
			return 0, err
		}
		if publishErr != nil {
			_ = s.releaseClaimedOutboxRows(ctx, rows)
			return 0, publishErr
		}
		metrics.MQMessagesPublished.Inc()
	}
	return len(rows), nil
}

func (s *TicketOrderService) releaseClaimedOutboxRows(
	ctx context.Context,
	rows []models.TicketOrderOutbox,
) error {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	if len(ids) == 0 {
		return nil
	}
	return s.asyncDB().WithContext(ctx).Model(&models.TicketOrderOutbox{}).
		Where("id IN ? AND status = ?", ids, models.TicketOrderOutboxPublishing).
		Update("status", models.TicketOrderOutboxPending).Error
}

func (s *TicketOrderService) markOutboxPublishResult(
	ctx context.Context,
	row models.TicketOrderOutbox,
	publishErr error,
) error {
	attempts := row.Attempts + 1
	if publishErr == nil {
		now := time.Now()
		return s.asyncDB().WithContext(ctx).Model(&models.TicketOrderOutbox{}).
			Where("id = ? AND status = ?", row.ID, models.TicketOrderOutboxPublishing).
			Updates(map[string]interface{}{
				"status":       models.TicketOrderOutboxPublished,
				"attempts":     attempts,
				"last_error":   "",
				"published_at": &now,
			}).Error
	}
	lastError := publishErr.Error()
	if utf8.RuneCountInString(lastError) > 512 {
		lastError = string([]rune(lastError)[:512])
	}
	updates := map[string]interface{}{
		"attempts":   attempts,
		"last_error": lastError,
		"status":     models.TicketOrderOutboxPending,
	}
	if attempts >= ticketOutboxMaxAttempts {
		updates["status"] = models.TicketOrderOutboxFailed
	}
	if err := s.asyncDB().WithContext(ctx).Model(&models.TicketOrderOutbox{}).
		Where("id = ? AND status = ?", row.ID, models.TicketOrderOutboxPublishing).
		Updates(updates).Error; err != nil {
		return err
	}
	if attempts >= ticketOutboxMaxAttempts {
		var failed TicketOrderMessage
		if err := json.Unmarshal([]byte(row.Payload), &failed); err == nil {
			s.FinalizeFailedMessage(ctx, failed, "outbox 投递超过最大重试次数: "+lastError)
		}
	}
	return nil
}
