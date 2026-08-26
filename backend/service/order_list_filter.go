package service

import (
	"strings"
	"unicode/utf8"

	"gofun/models"

	"gorm.io/gorm"
)

// OrderListFilter is the user-facing order list query: status chips + keyword.
// StatusGroup is empty for 全部; pending covers queued + pending_payment;
// cancelled excludes refunded payment_status; refunded covers refunding + refunded.
type OrderListFilter struct {
	StatusGroup string
	Keyword     string
}

func ParseOrderListFilter(status, keyword, q string) OrderListFilter {
	group := strings.ToLower(strings.TrimSpace(status))
	switch group {
	case "pending", "pending_payment", "unpaid", "queued":
		group = "pending"
	case "paid":
		group = "paid"
	case "cancelled", "canceled":
		group = "cancelled"
	case "refunded", "refund", "refunding":
		group = "refunded"
	default:
		group = ""
	}
	kw := strings.TrimSpace(keyword)
	if kw == "" {
		kw = strings.TrimSpace(q)
	}
	if utf8.RuneCountInString(kw) > 64 {
		kw = string([]rune(kw)[:64])
	}
	return OrderListFilter{StatusGroup: group, Keyword: kw}
}

func (f OrderListFilter) Statuses() []models.TicketOrderStatus {
	switch f.StatusGroup {
	case "pending":
		return []models.TicketOrderStatus{
			models.TicketOrderStatusQueued,
			models.TicketOrderStatusPendingPayment,
		}
	case "paid":
		return []models.TicketOrderStatus{models.TicketOrderStatusPaid}
	case "cancelled":
		return []models.TicketOrderStatus{models.TicketOrderStatusCancelled}
	default:
		return nil
	}
}

func (f OrderListFilter) CacheToken() string {
	return f.StatusGroup + "|" + f.Keyword
}

func escapeOrderListLike(value string) string {
	return strings.NewReplacer("%", "", "_", "").Replace(value)
}

func (f OrderListFilter) Apply(query *gorm.DB) *gorm.DB {
	switch f.StatusGroup {
	case "pending":
		query = query.Where("status IN ?", []models.TicketOrderStatus{
			models.TicketOrderStatusQueued,
			models.TicketOrderStatusPendingPayment,
		})
	case "paid":
		query = query.Where("status = ? AND payment_status = ?",
			models.TicketOrderStatusPaid, models.PaymentStatusPaid)
	case "cancelled":
		query = query.Where("status = ? AND payment_status <> ?",
			models.TicketOrderStatusCancelled, models.PaymentStatusRefunded)
	case "refunded":
		query = query.Where("payment_status IN ?", []models.PaymentStatus{
			models.PaymentStatusRefunding,
			models.PaymentStatusRefunded,
		})
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := "%" + escapeOrderListLike(kw) + "%"
		itemMatch := query.Session(&gorm.Session{NewDB: true}).Model(&models.TicketOrderItem{}).
			Select("1").
			Where("ticket_order_item.order_id = ticket_order.id").
			Where(
				"ticket_order_item.event_title_snapshot LIKE ? OR ticket_order_item.venue_name_snapshot LIKE ? OR ticket_order_item.tier_name_snapshot LIKE ?",
				like, like, like,
			)
		query = query.Where("order_no LIKE ? OR EXISTS (?)", like, itemMatch)
	}
	return query
}

func (s *TicketOrderService) applyOrderListFilter(db *gorm.DB, userID int64, filter OrderListFilter) *gorm.DB {
	return filter.Apply(db.Model(&models.TicketOrder{}).Where("user_id = ?", userID))
}
