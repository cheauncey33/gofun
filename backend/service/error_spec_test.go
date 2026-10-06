package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"gofun/pkg/response"
)

// allSentinels 列出 service 层所有对外哨兵错误。
// 这里刻意不自动反射：显式列举能让「新增哨兵」这一改动触发一次人工翻译判断。
func allSentinels() []error {
	return []error{
		ErrTicketOrderNotFound, ErrTicketOrderUnavailable, ErrTicketQuotaInsufficient,
		ErrTicketOrderState, ErrTicketBalance, ErrTicketOrderNonRetryable, ErrTicketAlreadyUsed,
		ErrWaitlistNotFound, ErrWaitlistUnavailable, ErrWaitlistState, ErrWaitlistSeated,
		ErrTicketResourceNotFound, ErrOrganizerForbidden, ErrInvalidTicketCatalog,
		ErrOrganizerUnavailable,
		ErrPaymentSignature, ErrPaymentNotFound, ErrPaymentAmountMismatch,
		ErrPaymentNotRefundable, ErrPaymentInvalidNotification,
		ErrTicketCredentialInvalid, ErrTicketAccessDenied,
		ErrCommentNotFound, ErrCommentForbidden, ErrCommentInvalid,
		ErrCommentRateLimited, ErrCommentAlreadyLiked, ErrCommentEventNotPublished,
	}
}

func TestErrorSpecCoversEverySentinel(t *testing.T) {
	for _, sentinel := range allSentinels() {
		if sentinel == nil {
			t.Fatal("哨兵列表里存在 nil，说明变量名写错了")
		}
		wrapped := fmt.Errorf("service layer: %w", sentinel)
		status, code, message, ok := ErrorSpec(wrapped)
		if !ok {
			t.Errorf("哨兵 %v 缺少翻译表映射，请求会退化成通用文案", sentinel)
			continue
		}
		if status == http.StatusInternalServerError {
			t.Errorf("哨兵 %v 被映射为 500，应指定准确的 HTTP 状态", sentinel)
		}
		// 业务码允许落在 4xxxx（协议类）或 6xxxx（业务类）：
		// HTTP 状态已由规格表显式指定，不再依赖 code/100 推导。
		if code == response.CodeSuccess {
			t.Errorf("哨兵 %v 不应使用成功码", sentinel)
		}
		if strings.TrimSpace(message) == "" {
			t.Errorf("哨兵 %v 缺少面向用户的文案", sentinel)
		}
	}
}

// 这条用例是整个错误处理改造的安全底线：
// err.Error() 里可能含 SQL 报错、字段名、内部表名，永远不能进入响应体。
func TestErrorSpecNeverReturnsInternalErrorText(t *testing.T) {
	internal := fmt.Errorf("Error 1062 (23000): Duplicate entry '13800138000' for key 'uk_user_phone'")
	_, _, message, ok := ErrorSpec(internal)
	if ok {
		t.Fatal("未登记的错误不应返回任何用户文案")
	}
	if strings.Contains(message, "1062") || strings.Contains(message, "Duplicate") ||
		strings.Contains(message, internal.Error()) {
		t.Fatalf("内部错误文本泄漏到用户文案: %q", message)
	}
}

func TestErrorSpecDistinguishesHTTPStatusFromBusinessCode(t *testing.T) {
	// 订单状态类错误业务码是 6xxxx，但语义上是 400；
	// 若仅按 code/100 推导会得到 500，这里显式校验。
	status, code, _, ok := ErrorSpec(ErrTicketOrderState)
	if !ok {
		t.Fatal("ErrTicketOrderState 未登记")
	}
	if status != http.StatusBadRequest {
		t.Fatalf("ErrTicketOrderState 的 HTTP 状态=%d, want 400", status)
	}
	if code != response.CodeInvalidOrderStatus {
		t.Fatalf("ErrTicketOrderState 的业务码=%d, want %d", code, response.CodeInvalidOrderStatus)
	}
}

func TestErrorSpecHandlesNil(t *testing.T) {
	if _, _, _, ok := ErrorSpec(nil); ok {
		t.Fatal("nil 不应命中任何规格")
	}
}

func TestErrorSpecMatchesWrappedErrors(t *testing.T) {
	deep := fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrTicketQuotaInsufficient))
	status, code, _, ok := ErrorSpec(deep)
	if !ok {
		t.Fatal("多层包装后仍应能识别哨兵")
	}
	if status != http.StatusConflict || code != response.CodeInsufficientStock {
		t.Fatalf("wrapped err 映射不符: status=%d code=%d", status, code)
	}
}

func TestErrorSpecsHaveNoDuplicateSentinels(t *testing.T) {
	seen := make(map[error]struct{})
	for _, spec := range errorSpecs {
		for _, sentinel := range spec.Sentinels {
			if sentinel == nil {
				continue
			}
			if _, dup := seen[sentinel]; dup {
				t.Errorf("哨兵 %v 被重复登记，第一个匹配的规格会静默胜出", sentinel)
			}
			seen[sentinel] = struct{}{}
		}
	}
}

func TestErrorSpecMessagesAreHumanReadable(t *testing.T) {
	// payment_gateway 的哨兵原文是英文技术描述，翻译表必须给出中文文案。
	if _, _, message, ok := ErrorSpec(ErrPaymentSignature); !ok {
		t.Fatal("ErrPaymentSignature 未登记")
	} else if strings.ContainsAny(message, "abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("支付错误文案不应保留英文原文: %q", message)
	}
	if errors.Is(ErrPaymentSignature, errors.ErrUnsupported) {
		t.Fatal("哨兵不应与标准库错误混淆")
	}
}
