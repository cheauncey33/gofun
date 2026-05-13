package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"fmt"

	"gorm.io/gorm"
)

type DashboardStats struct {
	TotalUsers     int64           `json:"total_users"`
	TotalOrders    int64           `json:"total_orders"`
	TotalRevenue   float64         `json:"total_revenue"`
	TodayOrders    int64           `json:"today_orders"`
	TodayRevenue   float64         `json:"today_revenue"`
	ActiveSeckills int64           `json:"active_seckills"`
	ProductsOnSale int64           `json:"products_on_sale"`
	TopProducts    []TopProduct    `json:"top_products"`
	OrderTrend     []OrderTrendDay `json:"order_trend"`
}

type TopProduct struct {
	ProductID   int64   `json:"product_id"`
	ProductName string  `json:"product_name"`
	SalesCount  int64   `json:"sales_count"`
	Revenue     float64 `json:"revenue"`
}

type OrderTrendDay struct {
	Date   string  `json:"date"`
	Count  int64   `json:"count"`
	Amount float64 `json:"amount"`
}

func GetDashboard() (*DashboardStats, error) {
	stats := &DashboardStats{}

	// 总用户
	if err := common.DB.Model(&models.User{}).Count(&stats.TotalUsers).Error; err != nil {
		return nil, err
	}

	// 总订单 + 总营收
	if err := common.DB.Model(&models.Order{}).Count(&stats.TotalOrders).Error; err != nil {
		return nil, err
	}
	if err := common.DB.Model(&models.Order{}).Select("COALESCE(SUM(total_price), 0)").Scan(&stats.TotalRevenue).Error; err != nil {
		return nil, err
	}

	// 今日订单 + 今日营收
	if err := common.DB.Model(&models.Order{}).Where("DATE(create_time) = CURDATE()").Count(&stats.TodayOrders).Error; err != nil {
		return nil, err
	}
	if err := common.DB.Model(&models.Order{}).Where("DATE(create_time) = CURDATE()").Select("COALESCE(SUM(total_price), 0)").Scan(&stats.TodayRevenue).Error; err != nil {
		return nil, err
	}

	// 在售商品数
	if err := common.DB.Model(&models.Product{}).Where("status = ?", models.ProductStatusOnSale).Count(&stats.ProductsOnSale).Error; err != nil {
		return nil, err
	}

	// 活跃秒杀数
	if err := common.DB.Model(&models.SeckillActivity{}).Where("status = ?", models.SeckillStatusActive).Count(&stats.ActiveSeckills).Error; err != nil {
		return nil, err
	}

	// 热门商品 Top 5
	var topProducts []TopProduct
	if err := common.DB.Model(&models.OrderItem{}).
		Select("product_id, product.name as product_name, SUM(order_item.quantity) as sales_count, SUM(order_item.snapshot_price * order_item.quantity) as revenue").
		Joins("JOIN product ON product.id = order_item.product_id").
		Group("product_id, product.name").
		Order("sales_count DESC").
		Limit(5).
		Scan(&topProducts).Error; err != nil {
		return nil, err
	}
	stats.TopProducts = topProducts

	// 最近 7 天订单趋势
	var trend []OrderTrendDay
	if err := common.DB.Raw(`
		SELECT DATE(create_time) as date, COUNT(*) as count, COALESCE(SUM(total_price), 0) as amount
		FROM ` + "`order`" + `
		WHERE create_time >= DATE_SUB(CURDATE(), INTERVAL 7 DAY)
		GROUP BY DATE(create_time)
		ORDER BY date ASC
	`).Scan(&trend).Error; err != nil {
		return nil, err
	}
	stats.OrderTrend = trend

	return stats, nil
}

// ===== Admin Product Management =====

func AdminCreateProduct(product *models.Product) error {
	if err := common.DB.Create(product).Error; err != nil {
		return err
	}
	refreshProductCache(product.ID)
	return nil
}

func AdminUpdateProduct(id int64, updates map[string]interface{}) error {
	result := common.DB.Model(&models.Product{}).Where("id = ?", id).Updates(updates)
	if result.RowsAffected == 0 {
		return fmt.Errorf("商品不存在")
	}
	if result.Error != nil {
		return result.Error
	}
	refreshProductCache(id)
	return nil
}

func AdminDeleteProduct(id int64) error {
	result := common.DB.Delete(&models.Product{}, id)
	if result.RowsAffected == 0 {
		return fmt.Errorf("商品不存在")
	}
	if result.Error != nil {
		return result.Error
	}
	clearProductCache()
	if common.RDB != nil {
		common.RDB.Del(common.Ctx, fmt.Sprintf("snack:stock:%d", id))
	}
	return nil
}

func AdminUpdateProductStatus(id int64, status models.ProductStatus) error {
	result := common.DB.Model(&models.Product{}).Where("id = ?", id).Update("status", status)
	if result.RowsAffected == 0 {
		return fmt.Errorf("商品不存在")
	}
	if result.Error != nil {
		return result.Error
	}
	refreshProductCache(id)
	return nil
}

func refreshProductCache(productID int64) {
	clearProductCache()
	if common.RDB == nil {
		return
	}

	var product models.Product
	if err := common.DB.Unscoped().First(&product, productID).Error; err != nil {
		common.RDB.Del(common.Ctx, fmt.Sprintf("snack:stock:%d", productID))
		return
	}

	stockKey := fmt.Sprintf("snack:stock:%d", productID)
	if product.DeleteTime.Valid || product.Status != models.ProductStatusOnSale {
		common.RDB.Set(common.Ctx, stockKey, 0, 0)
		return
	}
	common.RDB.Set(common.Ctx, stockKey, product.Stock, 0)
}

func clearProductCache() {
	if common.LocalCache != nil {
		common.LocalCache.Flush()
	}
	if common.RDB == nil {
		return
	}

	iter := common.RDB.Scan(common.Ctx, 0, "product:list:*", 0).Iterator()
	var keys []string
	for iter.Next(common.Ctx) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		common.RDB.Del(common.Ctx, keys...)
	}
}

// ===== Admin Order Management =====

func AdminGetAllOrders(page, pageSize int, status *models.OrderStatus) ([]models.Order, int64, error) {
	var orders []models.Order
	var total int64

	query := common.DB.Model(&models.Order{}).Preload("OrderItem.Product").Preload("User")
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	query.Count(&total)

	offset := (page - 1) * pageSize
	err := query.Order("create_time DESC").Limit(pageSize).Offset(offset).Find(&orders).Error
	return orders, total, err
}

func AdminUpdateOrderStatus(orderID int64, targetStatus models.OrderStatus) error {
	var order models.Order
	if err := common.DB.First(&order, orderID).Error; err != nil {
		return fmt.Errorf("订单不存在")
	}
	if !order.Status.CanTransitionTo(targetStatus) {
		return fmt.Errorf("状态转换不允许: %s -> %s", order.Status.String(), targetStatus.String())
	}
	// 取消或退款需执行退款退库存
	if targetStatus == models.OrderStatusCancelled || targetStatus == models.OrderStatusRefunded {
		return common.DB.Transaction(func(tx *gorm.DB) error {
			if err := tx.Model(&order).Update("status", targetStatus).Error; err != nil {
				return err
			}
			var items []models.OrderItem
			if err := tx.Where("order_id = ?", orderID).Find(&items).Error; err != nil {
				return err
			}
			for _, item := range items {
				tx.Model(&models.Product{}).Where("id = ?", item.ProductID).
					Update("stock", gorm.Expr("stock + ?", item.Quantity))
			}
			tx.Model(&models.User{}).Where("id = ?", order.UserID).
				Update("balance", gorm.Expr("balance + ?", order.TotalPrice))
			return nil
		})
	}
	return common.DB.Model(&order).Update("status", targetStatus).Error
}

// ===== Admin User Management =====

func AdminGetUserList(page, pageSize int) ([]models.User, int64, error) {
	var users []models.User
	var total int64

	common.DB.Model(&models.User{}).Count(&total)

	offset := (page - 1) * pageSize
	err := common.DB.Select("id", "username", "balance", "phone", "role", "dorm_id", "create_time", "last_login_at").
		Order("id DESC").Limit(pageSize).Offset(offset).Find(&users).Error
	return users, total, err
}

func AdminUpdateUserRole(userID int64, role string) error {
	if role != "user" && role != "admin" {
		return fmt.Errorf("无效的角色")
	}
	result := common.DB.Model(&models.User{}).Where("id = ?", userID).Update("role", role)
	if result.RowsAffected == 0 {
		return fmt.Errorf("用户不存在")
	}
	return result.Error
}
