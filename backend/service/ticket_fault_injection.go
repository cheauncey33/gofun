package service

import (
	"context"
	"fmt"
)

type TicketFaultPoint string

const (
	FaultAfterRedisReserve   TicketFaultPoint = "after_redis_reserve"
	FaultAfterOrderCommit    TicketFaultPoint = "after_order_commit"
	FaultAfterConsumerCommit TicketFaultPoint = "after_consumer_commit"
)

// TicketFaultInjector 只作为进程内测试接缝使用，不通过配置或 HTTP 暴露。
// nil injector 表示生产路径完全关闭故障注入。
type TicketFaultInjector interface {
	Inject(context.Context, TicketFaultPoint, int64) error
}

type TicketFaultInjectorFunc func(context.Context, TicketFaultPoint, int64) error

func (f TicketFaultInjectorFunc) Inject(
	ctx context.Context,
	point TicketFaultPoint,
	orderID int64,
) error {
	return f(ctx, point, orderID)
}

func (s *TicketOrderService) injectTicketFault(
	ctx context.Context,
	point TicketFaultPoint,
	orderID int64,
) error {
	if s == nil || s.faultInjector == nil {
		return nil
	}
	if err := s.faultInjector.Inject(ctx, point, orderID); err != nil {
		return fmt.Errorf("injected ticket fault %s: %w", point, err)
	}
	return nil
}
