package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"context"
	"encoding/json"
	"fmt"

	"gorm.io/gorm"
)

type CreateOrderItemInput struct {
	ProductID int64 `json:"product_id" binding:"required,gt=0"`
	Num       int   `json:"num" binding:"required,gt=0"`
}

type CreateOrderInput struct {
	Items []CreateOrderItemInput `json:"items" binding:"required,min=1,dive"`
}
type OrderMessage struct {
	OrderID           int64            `json:"order_id"`
	UserID            int64            `json:"user_id"`
	SeckillActivityID int64            `json:"seckill_activity_id,omitempty"`
	SeckillPrice      float64          `json:"seckill_price"` // 0=use product.Price
	Items             CreateOrderInput `json:"items"`
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

func CreateOrder(user_id int64, input CreateOrderInput) error {
	normalizedInput, err := normalizeCreateOrderInput(input)
	if err != nil {
		return err
	}

	var keys []string
	var args []any

	for _, item := range normalizedInput.Items {
		keys = append(keys, fmt.Sprintf("snack:stock:%d", item.ProductID))
		args = append(args, item.Num)
	}
	result, err := common.RDB.Eval(common.Ctx, deductStockLua, keys, args...).Result()
	if err != nil {
		return fmt.Errorf("系统繁忙，拦截器熔断")
	}
	resNum := result.(int64)
	if resNum < 0 {
		return fmt.Errorf("手慢了！商品已被抢空")
	}
	orderID := common.Node.Generate().Int64()
	msg := OrderMessage{
		OrderID: orderID,
		UserID:  user_id,
		Items:   normalizedInput,
	}
	body, _ := json.Marshal(msg)
	err = common.PublishPersistent(context.Background(), body)
	if err != nil {
		for _, item := range normalizedInput.Items {
			redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)
			common.RDB.IncrBy(common.Ctx, redisKey, int64(item.Num))
		}
		return fmt.Errorf("订单排队失败，请稍后再试")
	}
	return nil
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

func ProcessOrderTask(msg OrderMessage) error {
	if msg.OrderID <= 0 {
		return fmt.Errorf("订单ID无效")
	}
	if msg.UserID <= 0 {
		return fmt.Errorf("用户ID无效")
	}
	normalizedInput, err := normalizeCreateOrderInput(msg.Items)
	if err != nil {
		return err
	}
	msg.Items = normalizedInput

	var existing int64
	if err := common.DB.Model(&models.Order{}).Where("id = ?", msg.OrderID).Count(&existing).Error; err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}

	txErr := common.DB.Transaction(func(tx *gorm.DB) error {
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
			return fmt.Errorf("部分商品不存在")
		}

		productMap := make(map[int64]models.Product, len(products))
		for _, product := range products {
			productMap[product.ID] = product
		}

		orderItems := make([]models.OrderItem, 0, len(msg.Items.Items))
		for _, item := range msg.Items.Items {
			product, ok := productMap[item.ProductID]
			if !ok {
				return fmt.Errorf("商品ID %d 不存在", item.ProductID)
			}
			if product.Status != models.ProductStatusOnSale {
				return fmt.Errorf("商品%s已下架", product.Name)
			}

			res := tx.Model(&models.Product{}).
				Where("id = ? AND stock >= ?", item.ProductID, item.Num).
				Update("stock", gorm.Expr("stock - ?", item.Num))
			if res.Error != nil || res.RowsAffected == 0 {
				return fmt.Errorf("商品 %s 库存扣减失败，可能已经被抢光", product.Name)
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

		balanceResult := tx.Model(&models.User{}).
			Where("id = ? AND balance >= ?", msg.UserID, totalAmount).
			Update("balance", gorm.Expr("balance - ?", totalAmount))
		if balanceResult.Error != nil {
			return fmt.Errorf("余额扣减失败")
		}
		if balanceResult.RowsAffected == 0 {
			return fmt.Errorf("余额不足或用户不存在，总价 %.2f", totalAmount)
		}

		newOrder := models.Order{
			Base:       models.Base{ID: msg.OrderID},
			UserID:     msg.UserID,
			TotalPrice: totalAmount,
			Status:     models.OrderStatusPaid,
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
	return txErr
}

func RollbackReservedStock(msg OrderMessage) {
	if msg.SeckillActivityID > 0 {
		stockKey := fmt.Sprintf("seckill:stock:%d", msg.SeckillActivityID)
		userKey := fmt.Sprintf("seckill:user_count:%d", msg.SeckillActivityID)
		for _, item := range msg.Items.Items {
			common.RDB.IncrBy(common.Ctx, stockKey, int64(item.Num))
			common.RDB.HIncrBy(common.Ctx, userKey, fmt.Sprintf("%d", msg.UserID), int64(-item.Num))
		}
		return
	}

	for _, item := range msg.Items.Items {
		redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)
		common.RDB.IncrBy(common.Ctx, redisKey, int64(item.Num))
	}
}

// GetOrderList 用户订单列表
func GetOrderList(userID int64, page, pageSize int, status *int) ([]models.Order, int64, error) {
	var orders []models.Order
	var total int64

	query := common.DB.Model(&models.Order{}).Where("user_id = ?", userID)
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

// GetOrderDetail 订单详情
func GetOrderDetail(orderID, userID int64) (*models.Order, error) {
	var order models.Order
	err := common.DB.Preload("OrderItem.Product").
		Where("id = ? AND user_id = ?", orderID, userID).
		First(&order).Error
	if err != nil {
		return nil, err
	}
	return &order, nil
}

// CancelOrder 取消订单
func CancelOrder(orderID, userID int64, reason string) error {
	var order models.Order
	if err := common.DB.Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error; err != nil {
		return fmt.Errorf("订单不存在")
	}

	if !order.Status.CanTransitionTo(models.OrderStatusCancelled) {
		return fmt.Errorf("当前订单状态不允许取消")
	}

	return common.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&order).Updates(map[string]interface{}{
			"status":        models.OrderStatusCancelled,
			"cancel_reason": reason,
		}).Error; err != nil {
			return err
		}

		var items []models.OrderItem
		if err := tx.Where("order_id = ?", orderID).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			tx.Model(&models.Product{}).Where("id = ?", item.ProductID).
				Update("stock", gorm.Expr("stock + ?", item.Quantity))
			redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)
			common.RDB.IncrBy(common.Ctx, redisKey, int64(item.Quantity))
		}

		tx.Model(&models.User{}).Where("id = ?", order.UserID).
			Update("balance", gorm.Expr("balance + ?", order.TotalPrice))

		return nil
	})
}

// RequestRefund 退款申请
func RequestRefund(orderID, userID int64, reason string) error {
	var order models.Order
	if err := common.DB.Where("id = ? AND user_id = ?", orderID, userID).First(&order).Error; err != nil {
		return fmt.Errorf("订单不存在")
	}

	if !order.Status.CanTransitionTo(models.OrderStatusRefunding) {
		return fmt.Errorf("当前订单状态不允许退款")
	}

	return common.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&order).Update("status", models.OrderStatusRefunding).Error; err != nil {
			return err
		}

		var items []models.OrderItem
		if err := tx.Where("order_id = ?", orderID).Find(&items).Error; err != nil {
			return err
		}
		for _, item := range items {
			tx.Model(&models.Product{}).Where("id = ?", item.ProductID).
				Update("stock", gorm.Expr("stock + ?", item.Quantity))
			redisKey := fmt.Sprintf("snack:stock:%d", item.ProductID)
			common.RDB.IncrBy(common.Ctx, redisKey, int64(item.Quantity))
		}

		tx.Model(&models.User{}).Where("id = ?", order.UserID).
			Update("balance", gorm.Expr("balance + ?", order.TotalPrice))

		tx.Model(&order).Updates(map[string]interface{}{
			"status":        models.OrderStatusRefunded,
			"cancel_reason": reason,
		})

		return nil
	})
}
