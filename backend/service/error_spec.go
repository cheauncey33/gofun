package service

import (
	"errors"
	"log"
	"net/http"

	"gofun/metrics"
	"gofun/pkg/response"
)

// errorSpec 描述一个业务哨兵错误如何转换为面向调用方的响应。
// HTTPStatus 必须显式给出：业务码 6xxxx 无法可靠地反推出 HTTP 语义
// （例如「订单状态不允许」应是 400，但 60009/100 会落到 500）。
type errorSpec struct {
	Sentinels  []error
	HTTPStatus int
	Code       int
	Message    string
}

// errorSpecs 是哨兵错误的唯一翻译表。
// 新增业务哨兵时必须在这里登记，否则请求会退化成通用文案并在日志里留下告警。
var errorSpecs = []errorSpec{
	// 票务订单
	{
		Sentinels:  []error{ErrTicketOrderNotFound},
		HTTPStatus: http.StatusNotFound,
		Code:       response.CodeOrderNotFound,
		Message:    "订单不存在",
	},
	{
		Sentinels:  []error{ErrTicketOrderUnavailable},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeInvalidOrderStatus,
		Message:    "当前票档不可购买",
	},
	{
		Sentinels:  []error{ErrTicketQuotaInsufficient},
		HTTPStatus: http.StatusConflict,
		Code:       response.CodeInsufficientStock,
		Message:    "剩余票额不足",
	},
	{
		Sentinels:  []error{ErrTicketOrderState, ErrTicketOrderNonRetryable},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeInvalidOrderStatus,
		Message:    "当前订单状态不允许此操作",
	},
	{
		Sentinels:  []error{ErrTicketBalance},
		HTTPStatus: http.StatusConflict,
		Code:       response.CodeInsufficientBalance,
		Message:    "余额不足",
	},
	{
		Sentinels:  []error{ErrTicketAlreadyUsed},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeInvalidOrderStatus,
		Message:    "订单中已有电子票完成核销，不能退款",
	},

	// 候补
	{
		Sentinels:  []error{ErrWaitlistNotFound},
		HTTPStatus: http.StatusNotFound,
		Code:       response.CodeOrderNotFound,
		Message:    "候补单不存在",
	},
	{
		Sentinels:  []error{ErrWaitlistUnavailable, ErrWaitlistState, ErrWaitlistSeated},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeInvalidOrderStatus,
		Message:    "当前候补状态不允许此操作",
	},

	// 票务目录 / 主办方
	{
		Sentinels:  []error{ErrTicketResourceNotFound},
		HTTPStatus: http.StatusNotFound,
		Code:       response.CodeNotFound,
		Message:    "票务资源不存在",
	},
	{
		Sentinels:  []error{ErrOrganizerForbidden},
		HTTPStatus: http.StatusForbidden,
		Code:       response.CodeForbidden,
		Message:    "无权管理该主办方",
	},
	{
		Sentinels:  []error{ErrOrganizerUnavailable},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeBadRequest,
		Message:    "主办方尚未通过审核或已停用",
	},
	{
		Sentinels:  []error{ErrInvalidTicketCatalog},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeBadRequest,
		Message:    "票务目录参数不合法",
	},

	// 支付网关
	{
		Sentinels:  []error{ErrPaymentSignature},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeBadRequest,
		Message:    "支付回调签名校验失败",
	},
	{
		Sentinels:  []error{ErrPaymentNotFound, ErrPaymentNotRefundable},
		HTTPStatus: http.StatusConflict,
		Code:       response.CodeInvalidOrderStatus,
		Message:    "支付流水状态不允许此操作",
	},
	{
		Sentinels:  []error{ErrPaymentAmountMismatch, ErrPaymentInvalidNotification},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeInvalidOrderStatus,
		Message:    "支付回调参数不合法",
	},

	// 核销
	{
		Sentinels:  []error{ErrTicketCredentialInvalid},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeBadRequest,
		Message:    "票码无效",
	},
	{
		Sentinels:  []error{ErrTicketAccessDenied},
		HTTPStatus: http.StatusForbidden,
		Code:       response.CodeForbidden,
		Message:    "无权核销该主办方的票",
	},

	// 评论
	{
		Sentinels:  []error{ErrCommentNotFound, ErrCommentEventNotPublished},
		HTTPStatus: http.StatusNotFound,
		Code:       response.CodeNotFound,
		Message:    "评论不存在或活动未发布",
	},
	{
		Sentinels:  []error{ErrCommentForbidden},
		HTTPStatus: http.StatusForbidden,
		Code:       response.CodeForbidden,
		Message:    "无权操作该评论",
	},
	{
		Sentinels:  []error{ErrCommentInvalid},
		HTTPStatus: http.StatusBadRequest,
		Code:       response.CodeBadRequest,
		Message:    "评论内容不合法",
	},
	{
		Sentinels:  []error{ErrCommentRateLimited},
		HTTPStatus: http.StatusTooManyRequests,
		Code:       response.CodeTooManyRequests,
		Message:    "评论过于频繁，请稍后再试",
	},
	{
		Sentinels:  []error{ErrCommentAlreadyLiked},
		HTTPStatus: http.StatusConflict,
		Code:       response.CodeConflict,
		Message:    "已经点过赞了",
	},
}

// ErrorSpec 把 service 层哨兵错误翻译为「HTTP 状态 + 业务码 + 用户文案」。
// ok 为 false 表示没有匹配的规格，调用方应回退到通用文案并记录告警。
//
// 这里返回的用户文案是唯一允许出现在响应体里的文本：
// err.Error() 携带的内部语义（SQL 细节、告警字段等）一律不得回传客户端。
func ErrorSpec(err error) (httpStatus int, code int, message string, ok bool) {
	if err == nil {
		return http.StatusInternalServerError, response.CodeInternalError, "", false
	}
	for _, spec := range errorSpecs {
		for _, sentinel := range spec.Sentinels {
			if sentinel == nil {
				continue
			}
			if errors.Is(err, sentinel) {
				return spec.HTTPStatus, spec.Code, spec.Message, true
			}
		}
	}
	return http.StatusInternalServerError, response.CodeInternalError, "", false
}

// NoteUnmappedError 记录未登记的错误，避免「新增哨兵忘记补翻译」悄无声息地退化成通用文案。
func NoteUnmappedError(context string, err error) {
	if err == nil {
		return
	}
	if metrics.UnmappedBusinessErrorTotal != nil {
		metrics.UnmappedBusinessErrorTotal.WithLabelValues(context).Inc()
	}
	log.Printf("[error-spec] 未登记的业务错误 %s: %v —— 请在 service/error_spec.go 补充映射", context, err)
}
