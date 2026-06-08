package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/repository"
	"context"
	"fmt"

	gocache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
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

type AdminService struct {
	db          *gorm.DB
	rdb         *redis.Client
	localCache  *gocache.Cache
	productRepo repository.ProductRepository
}

func NewAdminService(c *container.Container) *AdminService {
	return &AdminService{
		db:          c.DB,
		rdb:         c.RDB,
		localCache:  c.LocalCache,
		productRepo: c.ProductRepo,
	}
}

func (s *AdminService) GetDashboard() (*DashboardStats, error) {
	stats := &DashboardStats{}

	if err := s.db.Model(&models.User{}).Count(&stats.TotalUsers).Error; err != nil {
		return nil, err
	}

	if err := s.db.Model(&models.Order{}).Count(&stats.TotalOrders).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&models.Order{}).Select("COALESCE(SUM(total_price), 0)").Scan(&stats.TotalRevenue).Error; err != nil {
		return nil, err
	}

	if err := s.db.Model(&models.Order{}).Where("DATE(create_time) = CURDATE()").Count(&stats.TodayOrders).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&models.Order{}).Where("DATE(create_time) = CURDATE()").Select("COALESCE(SUM(total_price), 0)").Scan(&stats.TodayRevenue).Error; err != nil {
		return nil, err
	}

	if err := s.db.Model(&models.Product{}).Where("status = ?", models.ProductStatusOnSale).Count(&stats.ProductsOnSale).Error; err != nil {
		return nil, err
	}

	if err := s.db.Model(&models.SeckillActivity{}).Where("status = ?", models.SeckillStatusActive).Count(&stats.ActiveSeckills).Error; err != nil {
		return nil, err
	}

	var topProducts []TopProduct
	if err := s.db.Model(&models.OrderItem{}).
		Select("product_id, product.name as product_name, SUM(order_item.quantity) as sales_count, SUM(order_item.snapshot_price * order_item.quantity) as revenue").
		Joins("JOIN product ON product.id = order_item.product_id").
		Group("product_id, product.name").
		Order("sales_count DESC").
		Limit(5).
		Scan(&topProducts).Error; err != nil {
		return nil, err
	}
	stats.TopProducts = topProducts

	var trend []OrderTrendDay
	if err := s.db.Raw(`
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

func (s *AdminService) AdminCreateProduct(product *models.Product) error {
	if err := s.db.Create(product).Error; err != nil {
		return err
	}
	s.refreshProductCache(product.ID)
	return nil
}

func (s *AdminService) AdminUpdateProduct(id int64, updates map[string]interface{}) error {
	result := s.db.Model(&models.Product{}).Where("id = ?", id).Updates(updates)
	if result.RowsAffected == 0 {
		return fmt.Errorf("商品不存在")
	}
	if result.Error != nil {
		return result.Error
	}
	s.refreshProductCache(id)
	return nil
}

func (s *AdminService) AdminDeleteProduct(id int64) error {
	result := s.db.Delete(&models.Product{}, id)
	if result.RowsAffected == 0 {
		return fmt.Errorf("商品不存在")
	}
	if result.Error != nil {
		return result.Error
	}
	s.clearProductCache()
	if s.rdb != nil {
		s.rdb.Del(context.Background(), fmt.Sprintf("snack:stock:%d", id))
	}
	return nil
}

func (s *AdminService) AdminUpdateProductStatus(id int64, status models.ProductStatus) error {
	result := s.db.Model(&models.Product{}).Where("id = ?", id).Update("status", status)
	if result.RowsAffected == 0 {
		return fmt.Errorf("商品不存在")
	}
	if result.Error != nil {
		return result.Error
	}
	s.refreshProductCache(id)
	return nil
}

func (s *AdminService) refreshProductCache(productID int64) {
	s.clearProductCache()
	if s.rdb == nil {
		return
	}

	var product models.Product
	if err := s.db.Unscoped().First(&product, productID).Error; err != nil {
		s.rdb.Del(context.Background(), fmt.Sprintf("snack:stock:%d", productID))
		return
	}

	stockKey := fmt.Sprintf("snack:stock:%d", productID)
	if product.DeleteTime.Valid || product.Status != models.ProductStatusOnSale {
		s.rdb.Set(context.Background(), stockKey, 0, 0)
		return
	}
	s.rdb.Set(context.Background(), stockKey, product.Stock, 0)
}

func (s *AdminService) clearProductCache() {
	if s.localCache != nil {
		s.localCache.Flush()
	}
	if s.rdb == nil {
		return
	}

	iter := s.rdb.Scan(context.Background(), 0, "product:list:*", 0).Iterator()
	var keys []string
	for iter.Next(context.Background()) {
		keys = append(keys, iter.Val())
	}
	if len(keys) > 0 {
		s.rdb.Del(context.Background(), keys...)
	}
}

func (s *AdminService) AdminGetAllOrders(page, pageSize int, status *models.OrderStatus) ([]models.Order, int64, error) {
	var orders []models.Order
	var total int64

	query := s.db.Model(&models.Order{}).Preload("OrderItem.Product").Preload("User")
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	query.Count(&total)

	offset := (page - 1) * pageSize
	err := query.Order("create_time DESC").Limit(pageSize).Offset(offset).Find(&orders).Error
	return orders, total, err
}

func (s *AdminService) AdminUpdateOrderStatus(orderID int64, targetStatus models.OrderStatus) error {
	var order models.Order
	if err := s.db.First(&order, orderID).Error; err != nil {
		return fmt.Errorf("订单不存在")
	}
	if !order.Status.CanTransitionTo(targetStatus) {
		return fmt.Errorf("状态转换不允许: %s -> %s", order.Status.String(), targetStatus.String())
	}
	if targetStatus == models.OrderStatusCancelled || targetStatus == models.OrderStatusRefunded {
		// 只有取消前已扣款的订单才退款;待支付订单被取消只退库存不退钱。
		refund := order.Status.HasBeenPaid()
		return s.db.Transaction(func(tx *gorm.DB) error {
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
			if refund {
				tx.Model(&models.User{}).Where("id = ?", order.UserID).
					Update("balance", gorm.Expr("balance + ?", order.TotalPrice))
			}
			return nil
		})
	}
	return s.db.Model(&order).Update("status", targetStatus).Error
}

func (s *AdminService) AdminGetUserList(page, pageSize int) ([]models.User, int64, error) {
	var users []models.User
	var total int64

	s.db.Model(&models.User{}).Count(&total)

	offset := (page - 1) * pageSize
	err := s.db.Select("id", "username", "balance", "phone", "role", "dorm_id", "create_time", "last_login_at").
		Order("id DESC").Limit(pageSize).Offset(offset).Find(&users).Error
	return users, total, err
}

func (s *AdminService) AdminUpdateUserRole(userID int64, role string) error {
	if role != "user" && role != "admin" {
		return fmt.Errorf("无效的角色")
	}
	result := s.db.Model(&models.User{}).Where("id = ?", userID).Update("role", role)
	if result.RowsAffected == 0 {
		return fmt.Errorf("用户不存在")
	}
	return result.Error
}
