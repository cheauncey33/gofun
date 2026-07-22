package service

import (
	"WHU_Snack_GO/models"
	"context"
	"fmt"

	"gorm.io/gorm"
)

// PaymentGateway 抽象票务余额扣款/退款，默认走站内 balance_cents。
// 后续可替换为真实渠道适配器，而不改动订单状态机。
type PaymentGateway interface {
	Debit(ctx context.Context, tx *gorm.DB, userID, amountCents int64) error
	Credit(ctx context.Context, tx *gorm.DB, userID, amountCents int64) error
}

type BalancePaymentGateway struct{}

func NewBalancePaymentGateway() *BalancePaymentGateway {
	return &BalancePaymentGateway{}
}

func (g *BalancePaymentGateway) Debit(
	ctx context.Context,
	tx *gorm.DB,
	userID, amountCents int64,
) error {
	if amountCents <= 0 {
		return fmt.Errorf("%w: 扣款金额无效", ErrInvalidTicketCatalog)
	}
	result := tx.WithContext(ctx).Model(&models.User{}).
		Where("id = ? AND balance_cents >= ?", userID, amountCents).
		Update("balance_cents", gorm.Expr("balance_cents - ?", amountCents))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrTicketBalance
	}
	return nil
}

func (g *BalancePaymentGateway) Credit(
	ctx context.Context,
	tx *gorm.DB,
	userID, amountCents int64,
) error {
	if amountCents <= 0 {
		return nil
	}
	return tx.WithContext(ctx).Model(&models.User{}).Where("id = ?", userID).
		Update("balance_cents", gorm.Expr("balance_cents + ?", amountCents)).Error
}
