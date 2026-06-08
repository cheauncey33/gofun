package service

import (
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/repository"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	amqp "github.com/rabbitmq/amqp091-go"
	"gorm.io/gorm"
)

const (
	orderTimeoutExchange = "order_timeout_exchange"
	orderTimeoutQueue    = "order_timeout_queue"
	orderDelayQueue      = "order_delay_queue"
)

type OrderTimeoutMessage struct {
	OrderID int64 `json:"order_id"`
	UserID  int64 `json:"user_id"`
}

type OrderTimeoutService struct {
	db             *gorm.DB
	rdb            *redis.Client
	conn           *amqp.Connection
	orderRepo      repository.OrderRepository
	productRepo    repository.ProductRepository
	timeoutMinutes int
}

func NewOrderTimeoutService(db *gorm.DB, rdb *redis.Client, conn *amqp.Connection,
	orderRepo repository.OrderRepository, productRepo repository.ProductRepository,
	timeoutMinutes int) *OrderTimeoutService {
	return &OrderTimeoutService{
		db:             db,
		rdb:            rdb,
		conn:           conn,
		orderRepo:      orderRepo,
		productRepo:    productRepo,
		timeoutMinutes: timeoutMinutes,
	}
}

func (s *OrderTimeoutService) SetupTimeoutInfrastructure() error {
	ch, err := s.conn.Channel()
	if err != nil {
		return fmt.Errorf("timeout setup: channel create failed: %w", err)
	}
	defer ch.Close()

	err = ch.ExchangeDeclare(orderTimeoutExchange, "direct", true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("timeout setup: exchange declare failed: %w", err)
	}

	ttlMs := int32(s.timeoutMinutes * 60 * 1000)
	args := amqp.Table{
		"x-dead-letter-exchange":    orderTimeoutExchange,
		"x-dead-letter-routing-key": "timeout",
		"x-message-ttl":             ttlMs,
	}
	_, err = ch.QueueDeclare(orderDelayQueue, true, false, false, false, args)
	if err != nil {
		return fmt.Errorf("timeout setup: delay queue declare failed: %w", err)
	}

	_, err = ch.QueueDeclare(orderTimeoutQueue, true, false, false, false, nil)
	if err != nil {
		return fmt.Errorf("timeout setup: timeout queue declare failed: %w", err)
	}

	err = ch.QueueBind(orderTimeoutQueue, "timeout", orderTimeoutExchange, false, nil)
	if err != nil {
		return fmt.Errorf("timeout setup: queue bind failed: %w", err)
	}

	return nil
}

func (s *OrderTimeoutService) PublishDelayedOrderTimeout(orderID, userID int64) error {
	ch, err := s.conn.Channel()
	if err != nil {
		return fmt.Errorf("timeout publish: channel create failed: %w", err)
	}
	defer ch.Close()

	msg := OrderTimeoutMessage{OrderID: orderID, UserID: userID}
	body, _ := json.Marshal(msg)

	return ch.PublishWithContext(
		context.Background(),
		"",
		orderDelayQueue,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
		},
	)
}

func (s *OrderTimeoutService) StartTimeoutConsumer(ctx context.Context, workerCount int) {
	if workerCount <= 0 {
		workerCount = 2
	}
	var wg sync.WaitGroup
	for i := 0; i < workerCount; i++ {
		workerID := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.runTimeoutWorker(ctx, workerID)
		}()
	}
	<-ctx.Done()
	wg.Wait()
	log.Println("all order timeout workers exited")
}

func (s *OrderTimeoutService) runTimeoutWorker(ctx context.Context, workerID int) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			if err := s.consumeTimeoutLoop(ctx, workerID); err != nil {
				log.Printf("timeout worker %d error, reconnecting in 3s: %v\n", workerID, err)
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
		}
	}
}

func (s *OrderTimeoutService) consumeTimeoutLoop(ctx context.Context, workerID int) error {
	ch, err := s.conn.Channel()
	if err != nil {
		return err
	}
	defer ch.Close()

	if err := ch.Qos(1, 0, false); err != nil {
		return err
	}

	msgs, err := ch.Consume(
		orderTimeoutQueue,
		fmt.Sprintf("timeout-worker-%d", workerID),
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("timeout worker %d MQ channel closed", workerID)
			}
			var msg OrderTimeoutMessage
			if err := json.Unmarshal(d.Body, &msg); err != nil {
				d.Ack(false)
				continue
			}
			if err := s.processTimeout(msg); err != nil {
				log.Printf("timeout worker %d order %d timeout processing failed: %v\n", workerID, msg.OrderID, err)
				d.Nack(false, true)
			} else {
				d.Ack(false)
			}
		}
	}
}

func (s *OrderTimeoutService) processTimeout(msg OrderTimeoutMessage) error {
	ctx := context.Background()
	order, err := s.orderRepo.FindByID(ctx, msg.OrderID)
	if err != nil {
		return fmt.Errorf("order %d not found: %w", msg.OrderID, err)
	}

	// 先快速短路:已不是待支付就直接放过(真正的并发安全由下面事务内的条件更新兜底)
	if order.Status != models.OrderStatusPending {
		return nil
	}

	var restoredItems []models.OrderItem
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		// 条件更新:只有订单仍为 Pending 时才取消。
		// 这一步同时挡住两种竞态——用户刚好支付(Pending→Paid)、或并发手动取消;
		// 只有抢到这次更新(RowsAffected==1)的一方才继续退库存/退款,杜绝重复退款。
		res := tx.Model(&models.Order{}).
			Where("id = ? AND status = ?", msg.OrderID, models.OrderStatusPending).
			Updates(map[string]interface{}{
				"status":        models.OrderStatusCancelled,
				"cancel_reason": "订单超时自动取消",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// 订单已被支付或已被取消,什么都不做
			return nil
		}

		var items []models.OrderItem
		if err := tx.Where("order_id = ?", msg.OrderID).Find(&items).Error; err != nil {
			return err
		}
		// 超时取消的订单一定是待支付(从未扣款),因此只回补库存、不退款。
		// 库存回补走 tx,与取消同属一个事务,任一步失败整体回滚,保证原子性。
		for _, item := range items {
			if err := tx.Model(&models.Product{}).Where("id = ?", item.ProductID).
				Update("stock", gorm.Expr("stock + ?", item.Quantity)).Error; err != nil {
				return err
			}
		}

		restoredItems = items
		return nil
	}); err != nil {
		return err
	}

	// 事务提交成功后再回补 Redis 缓存库存:放在事务外避免回滚时 Redis 被多加;
	// 即便这步漏掉,5 分钟的库存补偿任务也会把 Redis 拉回与 MySQL 一致。
	for _, item := range restoredItems {
		redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)
		s.rdb.IncrBy(ctx, redisKey, int64(item.Quantity))
	}
	return nil
}
