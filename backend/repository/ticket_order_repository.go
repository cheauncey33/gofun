// Package repository 承载票务下单链的纯数据访问：只做读写映射，不含业务规则与事务编排。
// service 层保留事务边界、Redis 一致性与业务校验；本包只依赖 models 与 gorm，
// 不得反向 import service/controller，也不得感知 Redis/MQ。
package repository

import (
	"context"

	"gofun/models"

	"gorm.io/gorm"
)

// TicketOrderRepository 是订单/票档/电子票的持久化入口。
// 方法一律以 ctx 贯通链路追踪；错误原样返回 gorm 语义（含 ErrRecordNotFound），
// 由调用方翻译为业务错误，避免持久化层提前绑定 HTTP 语义。
type TicketOrderRepository struct {
	db *gorm.DB
}

func NewTicketOrderRepository(db *gorm.DB) *TicketOrderRepository {
	return &TicketOrderRepository{db: db}
}

// FirstTier / FirstSession / FirstEvent / FirstVenue 是下单上下文的逐级查找链。
func (r *TicketOrderRepository) FirstTier(ctx context.Context, id int64) (*models.TicketTier, error) {
	var tier models.TicketTier
	if err := r.db.WithContext(ctx).First(&tier, id).Error; err != nil {
		return nil, err
	}
	return &tier, nil
}

func (r *TicketOrderRepository) FirstSession(ctx context.Context, id int64) (*models.EventSession, error) {
	var session models.EventSession
	if err := r.db.WithContext(ctx).First(&session, id).Error; err != nil {
		return nil, err
	}
	return &session, nil
}

func (r *TicketOrderRepository) FirstEvent(ctx context.Context, id int64) (*models.Event, error) {
	var event models.Event
	if err := r.db.WithContext(ctx).First(&event, id).Error; err != nil {
		return nil, err
	}
	return &event, nil
}

func (r *TicketOrderRepository) FirstVenue(ctx context.Context, id int64) (*models.Venue, error) {
	var venue models.Venue
	if err := r.db.WithContext(ctx).First(&venue, id).Error; err != nil {
		return nil, err
	}
	return &venue, nil
}

// LoadOrderDetail 加载用户视角的订单详情（含条目与出行人），未支付时不带电子票。
// 未找到返回 gorm.ErrRecordNotFound。
func (r *TicketOrderRepository) LoadOrderDetail(
	ctx context.Context,
	userID, orderID int64,
) (*models.TicketOrder, error) {
	var order models.TicketOrder
	if err := r.db.WithContext(ctx).
		Preload("Items").
		Preload("Attendees").
		Where("id = ? AND user_id = ?", orderID, userID).
		First(&order).Error; err != nil {
		return nil, err
	}
	return &order, nil
}

// ListOrderSeats 返回订单关联的选座记录（预加载座位明细）。
func (r *TicketOrderRepository) ListOrderSeats(ctx context.Context, orderID int64) ([]models.SessionSeat, error) {
	var seats []models.SessionSeat
	err := r.db.WithContext(ctx).Preload("Seat").
		Where("order_id = ?", orderID).
		Order("id ASC").Find(&seats).Error
	return seats, err
}

// ListPaidTicketsByOrder 返回订单的电子票（仅在已支付后存在）。
func (r *TicketOrderRepository) ListPaidTicketsByOrder(ctx context.Context, orderID int64) ([]models.AdmissionTicket, error) {
	var tickets []models.AdmissionTicket
	err := r.db.WithContext(ctx).
		Preload("OrderItem").
		Where("order_id = ?", orderID).
		Find(&tickets).Error
	return tickets, err
}

// CountUserTickets 统计用户名下电子票总数。
func (r *TicketOrderRepository) CountUserTickets(ctx context.Context, userID int64) (int64, error) {
	var total int64
	err := r.db.WithContext(ctx).Model(&models.AdmissionTicket{}).
		Where("user_id = ?", userID).Count(&total).Error
	return total, err
}

// ListUserTickets 按签发时间倒序分页加载用户电子票。
func (r *TicketOrderRepository) ListUserTickets(
	ctx context.Context,
	userID int64,
	limit, offset int,
) ([]models.AdmissionTicket, error) {
	var tickets []models.AdmissionTicket
	err := r.db.WithContext(ctx).
		Preload("OrderItem").
		Where("user_id = ?", userID).
		Order("issued_at DESC, id DESC").
		Limit(limit).
		Offset(offset).
		Find(&tickets).Error
	return tickets, err
}

// ListAttendeesByOrders 批量加载订单出行人，按 sequence_no 升序。
func (r *TicketOrderRepository) ListAttendeesByOrders(
	ctx context.Context,
	orderIDs []int64,
) ([]models.TicketOrderAttendee, error) {
	var attendees []models.TicketOrderAttendee
	err := r.db.WithContext(ctx).
		Where("order_id IN ?", orderIDs).
		Order("sequence_no ASC").
		Find(&attendees).Error
	return attendees, err
}

// MarkQueuedOrderFailed 将仍处于 queued 的订单置为 failed；条件更新保证幂等，
// 已被并发流转的订单不会被回退。
func (r *TicketOrderRepository) MarkQueuedOrderFailed(ctx context.Context, orderID int64, reason string) error {
	return r.db.WithContext(ctx).Model(&models.TicketOrder{}).
		Where("id = ? AND status = ?", orderID, models.TicketOrderStatusQueued).
		Updates(map[string]interface{}{
			"status":        models.TicketOrderStatusFailed,
			"cancel_reason": reason,
		}).Error
}
