package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/pkg/lock"
	"WHU_Snack_GO/repository"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CreateOrderItemInput struct {
	ProductID int64 `json:"product_id" binding:"required,gt=0"`
	Num       int   `json:"num" binding:"required,gt=0"`
}

type CreateOrderInput struct {
	Items          []CreateOrderItemInput `json:"items" binding:"required,min=1,dive"`
	IdempotencyKey string                 `json:"idempotency_key" binding:"omitempty,max=128"`
}

type OrderMessage struct {
	OrderID           int64            `json:"order_id"`
	UserID            int64            `json:"user_id"`
	SeckillActivityID int64            `json:"seckill_activity_id,omitempty"`
	SeckillPrice      float64          `json:"seckill_price"`
	Items             CreateOrderInput `json:"items"`
}

var ErrOrderNonRetryable = errors.New("non-retryable order error")

func nonRetryableOrderError(message string) error {
	return fmt.Errorf("%w: %s", ErrOrderNonRetryable, message)
}

func nonRetryableOrderErrorf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrOrderNonRetryable, fmt.Sprintf(format, args...))
}

var deductStockLua = `
	local keys = KEYS
	local args=ARGV

	for i=1, #keys do
		local stock = tonumber(redis.call('GET',keys[i]))
		local reqNum =tonumber(args[i])

		if reqNum == nil or reqNum <= 0 then
			return -1000
		end

		if stock ==nil or stock < reqNum then
			return -i
		end
	end

	for i=1, #keys do
		redis.call('DECRBY',keys[i],args[i])
	end

	return 1
	`

// 普通订单库存回滚：原子执行 幂等检查 + 设置回滚标记 + 恢复所有商品库存
var rollbackNormalStockLua = `
		local rollbackKey = KEYS[1]
		-- ARGV[1]: rollback key TTL (seconds)
		-- ARGV[2..2+N-1]: quantities to restore, matching KEYS[2..1+N]

		if redis.call('EXISTS', rollbackKey) == 1 then
			return 0
		end

		redis.call('SET', rollbackKey, '1', 'EX', ARGV[1])

		for i = 2, #KEYS do
			redis.call('INCRBY', KEYS[i], ARGV[i])
		end

		return 1
	`

// 秒杀订单库存回滚：原子执行 幂等检查 + 设置回滚标记 + 恢复活动库存 + 扣减用户已购计数
var rollbackSeckillStockLua = `
		local rollbackKey  = KEYS[1]
		local stockKey     = KEYS[2]
		local userCountKey = KEYS[3]
		-- ARGV[1]: rollback key TTL (seconds)
		-- ARGV[2]: userID (用作 HINCRBY 的 field)
		-- ARGV[3..]: quantities per item (多个 item 的购买数量)

		if redis.call('EXISTS', rollbackKey) == 1 then
			return 0
		end

		redis.call('SET', rollbackKey, '1', 'EX', ARGV[1])

		local totalQty = 0
		for i = 3, #ARGV do
			totalQty = totalQty + tonumber(ARGV[i])
		end

		redis.call('INCRBY', stockKey, totalQty)
		redis.call('HINCRBY', userCountKey, ARGV[2], -totalQty)

		return 1
	`

type OrderService struct {
	db             *gorm.DB
	rdb            *redis.Client
	snowflakeNode  *snowflake.Node
	publish        func(body []byte) error
	publishTimeout func(orderID, userID int64) error
	notify         func(userID int64, ev OrderStatusEvent)
	orderRepo      repository.OrderRepository
	productRepo    repository.ProductRepository
	lockTimeout    time.Duration
}

// OrderStatusEvent 是通过 WebSocket 推送给前端的订单状态变更事件。
// Status 取 models.OrderStatus 的整数值（下单失败时为 0），StatusText 为其英文标识。
// Event 标识触发场景：created/paid/cancelled/completed/timeout_cancelled/failed。
type OrderStatusEvent struct {
	OrderID    int64  `json:"order_id,string"`
	Status     int    `json:"status"`
	StatusText string `json:"status_text"`
	Event      string `json:"event"`
	Message    string `json:"message"`
}

func NewOrderService(c *container.Container, lockTimeoutSec int) *OrderService {
	return &OrderService{
		db:            c.DB,
		rdb:           c.RDB,
		snowflakeNode: c.SnowflakeNode,
		publish:       c.PublishPersistent,
		orderRepo:     c.OrderRepo,
		productRepo:   c.ProductRepo,
		lockTimeout:   time.Duration(lockTimeoutSec) * time.Second,
	}
}

func (s *OrderService) SetPublishTimeout(fn func(orderID, userID int64) error) {
	s.publishTimeout = fn
}

// SetNotifier 注入订单状态推送回调（通常由 WebSocket Hub 提供）。
func (s *OrderService) SetNotifier(fn func(userID int64, ev OrderStatusEvent)) {
	s.notify = fn
}

// emitOrderEvent 向指定用户推送一条订单状态事件，未注入 notifier 时静默跳过。
func (s *OrderService) emitOrderEvent(userID, orderID int64, status models.OrderStatus, event, message string) {
	if s.notify == nil {
		return
	}
	s.notify(userID, OrderStatusEvent{
		OrderID:    orderID,
		Status:     int(status),
		StatusText: status.String(),
		Event:      event,
		Message:    message,
	})
}

// NotifyOrderFailed 在消费端遇到不可重试错误（订单最终未能创建）时推送失败事件。
func (s *OrderService) NotifyOrderFailed(userID, orderID int64, message string) {
	if s.notify == nil {
		return
	}
	s.notify(userID, OrderStatusEvent{
		OrderID:    orderID,
		Status:     0,
		StatusText: "failed",
		Event:      "failed",
		Message:    message,
	})
}

func (s *OrderService) CreateOrder(userID int64, input CreateOrderInput) error {
	normalizedInput, err := normalizeCreateOrderInput(input)
	if err != nil {
		return err
	}
	normalizedInput.IdempotencyKey = input.IdempotencyKey

	ctx := context.Background()
	lockKey := fmt.Sprintf("lock:order:user:%d", userID)
	return lock.WithLock(ctx, s.rdb, lockKey, s.lockTimeout, func() (err error) {
		var idempotencyRedisKey string
		if normalizedInput.IdempotencyKey != "" {
			idempotencyRedisKey = fmt.Sprintf("order:idempotency:%d:%s", userID, normalizedInput.IdempotencyKey)
			ok, setErr := s.rdb.SetNX(ctx, idempotencyRedisKey, "processing", 10*time.Minute).Result()
			if setErr != nil {
				return fmt.Errorf("系统繁忙，幂等校验失败")
			}
			if !ok {
				return fmt.Errorf("订单正在处理中，请勿重复提交")
			}
			// 下单同步阶段一旦失败就释放幂等键，否则用户在失败后 10 分钟内无法用同一 key 重试
			defer func() {
				if err != nil {
					s.rdb.Del(ctx, idempotencyRedisKey)
				}
			}()
		}

		var keys []string
		var args []any

		for _, item := range normalizedInput.Items {
			keys = append(keys, fmt.Sprintf("snack:stock:%d", item.ProductID))
			args = append(args, item.Num)
		}

		result, evalErr := s.rdb.Eval(ctx, deductStockLua, keys, args...).Result()
		if evalErr != nil {
			return fmt.Errorf("系统繁忙，拦截器熔断")
		}
		resNum := result.(int64)
		if resNum < 0 {
			return fmt.Errorf("手慢了！商品已被抢空")
		}

		orderID := s.snowflakeNode.Generate().Int64()
		msg := OrderMessage{
			OrderID: orderID,
			UserID:  userID,
			Items:   normalizedInput,
		}
		body, _ := json.Marshal(msg)
		if pubErr := s.publish(body); pubErr != nil {
			for _, item := range normalizedInput.Items {
				redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)
				s.rdb.IncrBy(ctx, redisKey, int64(item.Num))
			}
			return fmt.Errorf("订单排队失败，请稍后再试")
		}
		return nil
	})
}

func validateCreateOrderInput(input CreateOrderInput) error {
	_, err := normalizeCreateOrderInput(input)
	return err
}

func normalizeCreateOrderInput(input CreateOrderInput) (CreateOrderInput, error) {
	if len(input.Items) == 0 {
		return CreateOrderInput{}, fmt.Errorf("订单商品不能为空")
	}

	quantities := make(map[int64]int, len(input.Items))
	productIDs := make([]int64, 0, len(input.Items))
	for _, item := range input.Items {
		if item.ProductID <= 0 {
			return CreateOrderInput{}, fmt.Errorf("商品ID无效")
		}
		if item.Num <= 0 {
			return CreateOrderInput{}, fmt.Errorf("商品数量必须大于0")
		}
		if _, exists := quantities[item.ProductID]; !exists {
			productIDs = append(productIDs, item.ProductID)
		}
		quantities[item.ProductID] += item.Num
	}

	normalized := CreateOrderInput{Items: make([]CreateOrderItemInput, 0, len(productIDs))}
	for _, productID := range productIDs {
		normalized.Items = append(normalized.Items, CreateOrderItemInput{
			ProductID: productID,
			Num:       quantities[productID],
		})
	}
	return normalized, nil
}

func (s *OrderService) ProcessOrderTask(msg OrderMessage) error {
	if msg.OrderID <= 0 {
		return nonRetryableOrderError("订单ID无效")
	}
	if msg.UserID <= 0 {
		return nonRetryableOrderError("用户ID无效")
	}
	normalizedInput, err := normalizeCreateOrderInput(msg.Items)
	if err != nil {
		return nonRetryableOrderError(err.Error())
	}
	msg.Items = normalizedInput

	var existing int64
	if err := s.db.Model(&models.Order{}).Where("id = ?", msg.OrderID).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	txErr := s.db.Transaction(func(tx *gorm.DB) error {
		var totalAmount float64

		productIDs := make([]int64, 0, len(msg.Items.Items))
		for _, item := range msg.Items.Items {
			productIDs = append(productIDs, item.ProductID)
		}

		var products []models.Product
		if err := tx.Where("id IN ?", productIDs).Find(&products).Error; err != nil {
			return err
		}
		if len(products) != len(productIDs) {
			return nonRetryableOrderError("部分商品不存在")
		}

		productMap := make(map[int64]models.Product, len(products))
		for _, product := range products {
			productMap[product.ID] = product
		}

		orderItems := make([]models.OrderItem, 0, len(msg.Items.Items))
		for _, item := range msg.Items.Items {
			product, ok := productMap[item.ProductID]
			if !ok {
				return nonRetryableOrderErrorf("商品ID %d 不存在", item.ProductID)
			}
			if product.Status != models.ProductStatusOnSale {
				return nonRetryableOrderErrorf("商品%s已下架", product.Name)
			}

			res := tx.Model(&models.Product{}).
				Where("id = ? AND stock >= ?", item.ProductID, item.Num).
				Update("stock", gorm.Expr("stock - ?", item.Num))
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return nonRetryableOrderErrorf("商品 %s 库存扣减失败，可能已经被抢光", product.Name)
			}

			price := product.Price
			if msg.SeckillPrice > 0 {
				price = msg.SeckillPrice
			}
			totalAmount += price * float64(item.Num)

			orderItems = append(orderItems, models.OrderItem{
				OrderID:       msg.OrderID,
				ProductID:     item.ProductID,
				Quantity:      item.Num,
				SnapshotPrice: price,
			})
		}

		// 「支付时扣款」模型:下单阶段只占库存、不扣余额,余额在 PayOrder 时才扣。
		// 因此这里不再做余额校验,允许用户先下单后支付。
		newOrder := models.Order{
			Base:       models.Base{ID: msg.OrderID},
			UserID:     msg.UserID,
			TotalPrice: totalAmount,
			Status:     models.OrderStatusPending,
		}
		if err := tx.Create(&newOrder).Error; err != nil {
			return err
		}

		if err := tx.Create(&orderItems).Error; err != nil {
			return err
		}

		for _, item := range msg.Items.Items {
			if err := tx.Model(&models.Product{}).Where("id = ?", item.ProductID).
				Update("sales_count", gorm.Expr("sales_count + ?", item.Num)).Error; err != nil {
				return err
			}
		}

		if msg.SeckillActivityID > 0 {
			seckillOrder := models.SeckillOrder{
				UserID:     msg.UserID,
				ActivityID: msg.SeckillActivityID,
				OrderID:    newOrder.ID,
				Amount:     totalAmount,
			}
			if err := tx.Create(&seckillOrder).Error; err != nil {
				return err
			}
		}

		return nil
	})
	if txErr == nil {
		if s.publishTimeout != nil {
			_ = s.publishTimeout(msg.OrderID, msg.UserID)
		}
		// 订单已落库（待支付），主动推送给前端，替代轮询。
		s.emitOrderEvent(msg.UserID, msg.OrderID, models.OrderStatusPending, "created", "订单已创建，请尽快支付")
	}
	return txErr
}

func (s *OrderService) PayOrder(orderID, userID int64) error {
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var order models.Order
		if err := tx.Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error; err != nil {
			return fmt.Errorf("订单不存在")
		}

		// 条件更新:仅当订单仍为待支付时才置为已支付。
		// RowsAffected==0 说明已被并发支付或被超时取消,直接拒绝,避免重复扣款。
		statusRes := tx.Model(&models.Order{}).
			Where("id = ? AND status = ?", orderID, models.OrderStatusPending).
			Update("status", models.OrderStatusPaid)
		if statusRes.Error != nil {
			return statusRes.Error
		}
		if statusRes.RowsAffected == 0 {
			return fmt.Errorf("当前订单状态不允许支付")
		}

		// 抢到支付权后再扣余额;余额不足则整个事务回滚,订单退回待支付状态。
		balanceRes := tx.Model(&models.User{}).
			Where("id = ? AND balance >= ?", userID, order.TotalPrice).
			Update("balance", gorm.Expr("balance - ?", order.TotalPrice))
		if balanceRes.Error != nil {
			return balanceRes.Error
		}
		if balanceRes.RowsAffected == 0 {
			return fmt.Errorf("余额不足")
		}
		return nil
	}); err != nil {
		return err
	}
	s.emitOrderEvent(userID, orderID, models.OrderStatusPaid, "paid", "支付成功")
	return nil
}

func (s *OrderService) RollbackReservedStock(msg OrderMessage) {
	ctx := context.Background()

	if msg.OrderID <= 0 {
		log.Println("[回滚] 订单ID无效，跳过库存回滚")
		return
	}

	rollbackKey := fmt.Sprintf("order:stock_rollback:%d", msg.OrderID)
	rollbackTTL := int64((24 * time.Hour).Seconds())

	if msg.SeckillActivityID > 0 {
		stockKey := fmt.Sprintf("seckill:stock:%d", msg.SeckillActivityID)
		userKey := fmt.Sprintf("seckill:user_count:%d", msg.SeckillActivityID)

		keys := []string{rollbackKey, stockKey, userKey}
		args := []interface{}{rollbackTTL, strconv.FormatInt(msg.UserID, 10)}
		for _, item := range msg.Items.Items {
			args = append(args, item.Num)
		}

		result, err := s.rdb.Eval(ctx, rollbackSeckillStockLua, keys, args...).Result()
		if err != nil {
			log.Printf("[回滚] 秒杀订单 %d Lua 回滚失败: %v", msg.OrderID, err)
			return
		}
		if result.(int64) == 0 {
			log.Printf("[回滚] 秒杀订单 %d 已回滚过，跳过重复回滚", msg.OrderID)
			return
		}
		log.Printf("[回滚] 秒杀订单 %d 库存已原子回滚: stock+%s", msg.OrderID, formatRollbackQuantities(msg.Items.Items))
	} else {
		keys := []string{rollbackKey}
		args := []interface{}{rollbackTTL}
		for _, item := range msg.Items.Items {
			keys = append(keys, fmt.Sprintf("snack:stock:%d", item.ProductID))
			args = append(args, item.Num)
		}

		result, err := s.rdb.Eval(ctx, rollbackNormalStockLua, keys, args...).Result()
		if err != nil {
			log.Printf("[回滚] 订单 %d Lua 回滚失败: %v", msg.OrderID, err)
			return
		}
		if result.(int64) == 0 {
			log.Printf("[回滚] 订单 %d 已回滚过，跳过重复回滚", msg.OrderID)
			return
		}
		log.Printf("[回滚] 订单 %d 库存已原子回滚: %s", msg.OrderID, formatRollbackQuantities(msg.Items.Items))
	}

	// 订单最终失败，释放幂等键，允许用户用同一 key 重新下单（秒杀单不带 key，自动跳过）
	if msg.Items.IdempotencyKey != "" {
		idempotencyRedisKey := fmt.Sprintf("order:idempotency:%d:%s", msg.UserID, msg.Items.IdempotencyKey)
		s.rdb.Del(ctx, idempotencyRedisKey)
	}
}

func formatRollbackQuantities(items []CreateOrderItemInput) string {
	parts := make([]string, 0, len(items))
	for _, item := range items {
		parts = append(parts, fmt.Sprintf("product[%d]+%d", item.ProductID, item.Num))
	}
	return strings.Join(parts, ", ")
}

func (s *OrderService) GetOrderList(userID int64, page, pageSize int, status *int) ([]models.Order, int64, error) {
	var orders []models.Order
	var total int64

	query := s.db.Model(&models.Order{}).Where("user_id = ?", userID)
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("OrderItem.Product").
		Order("create_time DESC").
		Limit(pageSize).Offset(offset).
		Find(&orders).Error

	return orders, total, err
}

func (s *OrderService) GetOrderDetail(orderID, userID int64) (*models.Order, error) {
	var order models.Order
	err := s.db.Preload("OrderItem.Product").
		Where("id = ? AND user_id = ?", orderID, userID).
		First(&order).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *OrderService) CancelOrder(orderID, userID int64, reason string) error {
	var restoredItems []models.OrderItem
	var refund bool
	var totalPrice float64

	if err := s.db.Transaction(func(tx *gorm.DB) error {
		// SELECT FOR UPDATE：加行锁后再读状态，消除事务外读与事务内写之间的竞态窗口。
		var order models.Order
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error; err != nil {
			return fmt.Errorf("订单不存在")
		}

		if !order.Status.CanTransitionTo(models.OrderStatusCancelled) {
			return fmt.Errorf("当前订单状态不允许取消")
		}

		// 是否退款取决于取消前是否已扣款：待支付订单从未扣款，只退库存不退钱。
		refund = order.Status.HasBeenPaid()
		totalPrice = order.TotalPrice

		res := tx.Model(&models.Order{}).
			Where("id = ? AND status = ?", orderID, order.Status).
			Updates(map[string]interface{}{
				"status":        models.OrderStatusCancelled,
				"cancel_reason": reason,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("当前订单状态不允许取消")
		}

		var items []models.OrderItem
		if err := tx.Where("order_id = ?", orderID).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			if err := tx.Model(&models.Product{}).Where("id = ?", item.ProductID).
				Update("stock", gorm.Expr("stock + ?", item.Quantity)).Error; err != nil {
				return err
			}
		}

		if refund {
			if err := tx.Model(&models.User{}).Where("id = ?", userID).
				Update("balance", gorm.Expr("balance + ?", totalPrice)).Error; err != nil {
				return err
			}
		}

		restoredItems = items
		return nil
	}); err != nil {
		return err
	}

	// 事务提交后再回补 Redis 缓存库存,避免回滚时 Redis 多加。
	for _, item := range restoredItems {
		redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)
		s.rdb.IncrBy(context.Background(), redisKey, int64(item.Quantity))
	}

	message := "订单已取消"
	if refund {
		message = "订单已取消，款项已退回余额"
	}
	s.emitOrderEvent(userID, orderID, models.OrderStatusCancelled, "cancelled", message)
	return nil
}

func (s *OrderService) RequestRefund(orderID, userID int64, reason string) error {
	// 退款就是 Completed → Cancelled（退库存+退款），直接复用 CancelOrder。
	// CancelOrder 已根据 HasBeenPaid() 自动处理退款逻辑。
	return s.CancelOrder(orderID, userID, reason)
}

func (s *OrderService) ConfirmOrder(orderID, userID int64) error {
	res := s.db.Model(&models.Order{}).
		Where("id = ? AND user_id = ? AND status = ?", orderID, userID, models.OrderStatusPaid).
		Update("status", models.OrderStatusCompleted)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("当前订单状态不允许确认收货")
	}
	s.emitOrderEvent(userID, orderID, models.OrderStatusCompleted, "completed", "已确认收货，订单完成")
	return nil
}
