package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go.uber.org/zap"
	"gofun/metrics"
	"gofun/models"
	"gofun/pkg/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

const stockRecoveryEventType = "ticket.stock.return"
const stockRecoveryCompleted models.TicketOrderOutboxStatus = "completed"

type stockRecoveryPayload struct {
	Tasks       []stockReturnTask `json:"tasks,omitempty"`
	Reservation *stockReservation `json:"reservation,omitempty"`
}

// enqueueStockRecovery records the recovery intent in the business transaction.
func (s *TicketOrderService) enqueueStockRecovery(tx *gorm.DB, orderID int64, payload stockRecoveryPayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	row := models.TicketOrderOutbox{
		ID: s.node.Generate().Int64(), OrderID: orderID, EventType: stockRecoveryEventType,
		Payload: string(body), Status: models.TicketOrderOutboxPending, NextAttemptAt: time.Now(),
	}
	return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
}

func (s *TicketOrderService) enqueueOrderStockReturn(tx *gorm.DB, order *models.TicketOrder, stockBucket, rushBucket *int, skip bool, reason string) error {
	if skip {
		return nil
	}
	if len(order.Items) != 1 {
		return fmt.Errorf("stock return order %d: expected one tier", order.ID)
	}
	item := order.Items[0]
	key := ticketStockKey(item.TicketTierID)
	if s.inventory.Enabled {
		bucket := 0
		if stockBucket != nil {
			bucket = *stockBucket
		}
		key = TicketStockBucketKey(item.TicketTierID, bucket)
	}
	tasks := []stockReturnTask{{Token: stockReturnToken(order.ID, stockReturnKindTicket), StockKey: key, Quantity: item.Quantity, OrderID: order.ID, UserID: order.UserID, Kind: stockReturnKindTicket, Reason: reason}}
	if order.RushSaleCampaignID != nil {
		campaignID := *order.RushSaleCampaignID
		key = rushStockKey(campaignID)
		if s.inventory.Enabled && rushBucket != nil {
			key = RushStockBucketKey(campaignID, *rushBucket)
		}
		tasks = append(tasks,
			stockReturnTask{Token: stockReturnToken(order.ID, stockReturnKindRushStock), StockKey: key, Quantity: item.Quantity, OrderID: order.ID, UserID: order.UserID, Kind: stockReturnKindRushStock, Reason: reason},
			stockReturnTask{Token: stockReturnToken(order.ID, stockReturnKindRushUserCount), StockKey: rushUserCountKey(campaignID, order.UserID), Quantity: -item.Quantity, OrderID: order.ID, UserID: order.UserID, Kind: stockReturnKindRushUserCount, Reason: reason})
	}
	return s.enqueueStockRecovery(tx, order.ID, stockRecoveryPayload{Tasks: tasks})
}

func (s *TicketOrderService) stockReturnExecutor() *stockReturnRetry {
	return &stockReturnRetry{rdb: s.rdb, cfg: NewStockReturnRetrySettings(s.inventoryConfig)}
}

func (s *TicketOrderService) pendingInventoryKeys(ctx context.Context) (map[string]struct{}, error) {
	stockKeys, err := s.pendingStockReservationKeys(ctx)
	if err != nil {
		return nil, err
	}
	var intents []models.TicketOrderOutbox
	if err := s.asyncDB().WithContext(ctx).Where("event_type = ? AND status IN ?", stockRecoveryEventType,
		[]models.TicketOrderOutboxStatus{models.TicketOrderOutboxPending, models.TicketOrderOutboxFailed}).Find(&intents).Error; err != nil {
		return nil, err
	}
	for _, intent := range intents {
		var payload stockRecoveryPayload
		if err := json.Unmarshal([]byte(intent.Payload), &payload); err != nil {
			return nil, err
		}
		for _, task := range payload.Tasks {
			stockKeys[task.StockKey] = struct{}{}
		}
	}
	return stockKeys, nil
}

// RecoverStockReturns locks one due intent at a time. Redis effects are idempotent.
func (s *TicketOrderService) RecoverStockReturns(ctx context.Context) (int, error) {
	executor := s.stockReturnExecutor()
	processed := 0
	var failures []error
	for processed < stockReturnBatch {
		found := false
		var completedTasks []stockReturnTask
		var completedOrderID int64
		err := s.asyncDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var row models.TicketOrderOutbox
			err := tx.Clauses(clause.Locking{Strength: "UPDATE", Options: "SKIP LOCKED"}).
				Where("event_type = ? AND status = ? AND next_attempt_at <= ?", stockRecoveryEventType, models.TicketOrderOutboxPending, time.Now()).
				Order("next_attempt_at ASC, id ASC").First(&row).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			found = true
			var payload stockRecoveryPayload
			actionErr := json.Unmarshal([]byte(row.Payload), &payload)
			if actionErr == nil {
				actionCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
				if payload.Reservation != nil {
					actionErr = s.rollbackLoadedStockReservation(actionCtx, *payload.Reservation)
				}
				for _, task := range payload.Tasks {
					if actionErr != nil {
						break
					}
					_, actionErr = executor.execute(actionCtx, task)
				}
				cancel()
			}
			attempt := row.Attempts + 1
			updates := map[string]interface{}{"attempts": attempt, "status": stockRecoveryCompleted, "last_error": ""}
			if actionErr != nil {
				metrics.StockReturnTotal.WithLabelValues("failed").Inc()
				updates["status"] = models.TicketOrderOutboxPending
				updates["last_error"] = truncateError(actionErr)
				updates["next_attempt_at"] = time.Now().Add(time.Duration(executor.cfg.delayMillis(attempt)) * time.Millisecond)
				if attempt >= executor.cfg.MaxAttempts {
					updates["status"] = models.TicketOrderOutboxFailed
					metrics.StockReturnTotal.WithLabelValues("exhausted").Inc()
				}
				failures = append(failures, fmt.Errorf("order %d: %w", row.OrderID, actionErr))
				logger.FromContext(ctx).Error("Stock recovery failed", zap.Int64("order_id", row.OrderID), zap.Int("attempt", attempt), zap.Error(actionErr))
			} else {
				completedTasks = payload.Tasks
				completedOrderID = row.OrderID
			}
			return tx.Model(&row).Updates(updates).Error
		})
		if err != nil {
			return processed, errors.Join(append(failures, err)...)
		}
		if !found {
			break
		}
		if completedOrderID != 0 {
			metrics.StockReturnTotal.WithLabelValues("applied").Inc()
			logger.FromContext(ctx).Info("Stock recovery completed", zap.Int64("order_id", completedOrderID))
			for _, task := range completedTasks {
				_ = s.rdb.Expire(ctx, stockReturnTaskKey(task.Token), time.Duration(executor.cfg.DedupTTLSec)*time.Second).Err()
			}
		}
		processed++
	}
	return processed, errors.Join(failures...)
}

func (s *TicketOrderService) observeStockReturns(ctx context.Context) {
	for _, entry := range []struct {
		status models.TicketOrderOutboxStatus
		set    func(float64)
	}{
		{models.TicketOrderOutboxPending, metrics.StockReturnQueueDepth.Set},
		{models.TicketOrderOutboxFailed, metrics.StockReturnExhausted.Set},
	} {
		var count int64
		if err := s.asyncDB().WithContext(ctx).Model(&models.TicketOrderOutbox{}).Where("event_type = ? AND status = ?", stockRecoveryEventType, entry.status).Count(&count).Error; err == nil {
			entry.set(float64(count))
		}
	}
}

func truncateError(err error) string {
	text := []rune(err.Error())
	if len(text) > 240 {
		text = text[:240]
	}
	return string(text)
}
