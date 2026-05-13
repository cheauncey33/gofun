package repository

import (
	"WHU_Snack_GO/models"
	"context"
	"time"

	"gorm.io/gorm"
)

type OrderRepository interface {
	Create(ctx context.Context, order *models.Order) error
	FindByID(ctx context.Context, id int64) (*models.Order, error)
	FindByIDWithItems(ctx context.Context, id int64) (*models.Order, error)
	ListByUser(ctx context.Context, userID int64, page, pageSize int) ([]models.Order, int64, error)
	ListAll(ctx context.Context, page, pageSize int, status *models.OrderStatus) ([]models.Order, int64, error)
	UpdateStatus(ctx context.Context, id int64, status models.OrderStatus, reason string) error
	FindTimeoutOrders(ctx context.Context, status models.OrderStatus, before time.Time) ([]models.Order, error)
}

type orderRepoImpl struct {
	db *gorm.DB
}

func NewOrderRepository(db *gorm.DB) OrderRepository {
	return &orderRepoImpl{db: db}
}

func (r *orderRepoImpl) Create(ctx context.Context, order *models.Order) error {
	return r.db.WithContext(ctx).Create(order).Error
}

func (r *orderRepoImpl) FindByID(ctx context.Context, id int64) (*models.Order, error) {
	var order models.Order
	err := r.db.WithContext(ctx).First(&order, id).Error
	return &order, err
}

func (r *orderRepoImpl) FindByIDWithItems(ctx context.Context, id int64) (*models.Order, error) {
	var order models.Order
	err := r.db.WithContext(ctx).
		Preload("OrderItem.Product").
		Preload("User").
		First(&order, id).Error
	return &order, err
}

func (r *orderRepoImpl) ListByUser(ctx context.Context, userID int64, page, pageSize int) ([]models.Order, int64, error) {
	var orders []models.Order
	var total int64

	query := r.db.WithContext(ctx).Model(&models.Order{}).Where("user_id = ?", userID)

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

func (r *orderRepoImpl) ListAll(ctx context.Context, page, pageSize int, status *models.OrderStatus) ([]models.Order, int64, error) {
	var orders []models.Order
	var total int64

	query := r.db.WithContext(ctx).Model(&models.Order{})
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	offset := (page - 1) * pageSize
	err := query.Preload("OrderItem.Product").Preload("User").
		Order("create_time DESC").
		Limit(pageSize).Offset(offset).
		Find(&orders).Error

	return orders, total, err
}

func (r *orderRepoImpl) UpdateStatus(ctx context.Context, id int64, status models.OrderStatus, reason string) error {
	updates := map[string]interface{}{
		"status": status,
	}
	if reason != "" {
		updates["cancel_reason"] = reason
	}
	return r.db.WithContext(ctx).Model(&models.Order{}).Where("id = ?", id).Updates(updates).Error
}

func (r *orderRepoImpl) FindTimeoutOrders(ctx context.Context, status models.OrderStatus, before time.Time) ([]models.Order, error) {
	var orders []models.Order
	err := r.db.WithContext(ctx).
		Where("status = ? AND create_time < ?", status, before).
		Find(&orders).Error
	return orders, err
}
