package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gofun/pkg/response"
	"gofun/service"
)

func newErrorTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/v1/orders", nil)
	return c, w
}

type errorBody struct {
	Code    int    `json:"code"`
	Message string `json:"msg"`
}

func decodeErrorBody(t *testing.T, w *httptest.ResponseRecorder) errorBody {
	t.Helper()
	var body errorBody
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("解析响应体失败: %v; body=%s", err, w.Body.String())
	}
	return body
}

// assertSentinel 验证哨兵经统一出口渲染后：HTTP 状态、业务码、文案与规格表一致，
// 且错误原文（含包装上下文）没有出现在响应体里。
func assertSentinel(t *testing.T, err error) {
	t.Helper()
	c, w := newErrorTestContext()
	writeTicketOrderError(c, err)

	wantStatus, wantCode, wantMessage, ok := service.ErrorSpec(err)
	if !ok {
		t.Fatalf("哨兵 %v 未在规格表登记", err)
	}
	if w.Code != wantStatus {
		t.Errorf("哨兵 %v: HTTP 状态 = %d, 期望 %d", err, w.Code, wantStatus)
	}
	body := decodeErrorBody(t, w)
	if body.Code != wantCode {
		t.Errorf("哨兵 %v: 业务码 = %d, 期望 %d", err, body.Code, wantCode)
	}
	if body.Message != wantMessage {
		t.Errorf("哨兵 %v: 文案 = %q, 期望规格表文案 %q", err, body.Message, wantMessage)
	}
	// 哨兵文本与用户文案不同时，错误原文不得出现在响应体里；
	// 文本恰好一致（哨兵本身就定义为用户文案）则属预期，跳过该断言。
	if err.Error() != wantMessage && strings.Contains(w.Body.String(), err.Error()) {
		t.Errorf("哨兵 %v: 错误原文泄漏到响应体: %s", err, w.Body.String())
	}
}

func TestWriteErrorMapsRegisteredSentinels(t *testing.T) {
	sentinels := []error{
		service.ErrTicketOrderNotFound,
		service.ErrTicketOrderUnavailable,
		service.ErrTicketQuotaInsufficient,
		service.ErrTicketOrderState,
		service.ErrTicketOrderNonRetryable,
		service.ErrTicketBalance,
		service.ErrTicketAlreadyUsed,
		service.ErrWaitlistNotFound,
		service.ErrWaitlistUnavailable,
		service.ErrOrganizerForbidden,
		service.ErrInvalidTicketCatalog,
		service.ErrPaymentSignature,
		service.ErrPaymentNotFound,
		service.ErrTicketAccessDenied,
		service.ErrCommentRateLimited,
	}
	for _, sentinel := range sentinels {
		t.Run(fmt.Sprintf("%T", sentinel), func(t *testing.T) {
			assertSentinel(t, sentinel)
		})
	}
}

func TestWriteErrorMapsWrappedSentinel(t *testing.T) {
	// 生产里大量错误是 fmt.Errorf("%w", sentinel) 包装出来的，必须同样命中规格表。
	assertSentinel(t, fmt.Errorf("%w: 本场每账号限购 2 张", service.ErrTicketOrderUnavailable))
	assertSentinel(t, fmt.Errorf("预扣票额: %w", service.ErrTicketQuotaInsufficient))
}

func TestWriteErrorFallbackHidesInternalDetail(t *testing.T) {
	c, w := newErrorTestContext()
	internal := errors.New("mysql: unknown column ticket_order.secret_col in 'where clause'")
	writeTicketOrderError(c, internal)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("未登记错误 HTTP 状态 = %d, 期望 500", w.Code)
	}
	body := decodeErrorBody(t, w)
	if body.Code != response.CodeInternalError {
		t.Errorf("未登记错误业务码 = %d, 期望 %d", body.Code, response.CodeInternalError)
	}
	if body.Message != "票务订单处理失败，请稍后再试" {
		t.Errorf("未登记错误应返回兜底文案, 实际 %q", body.Message)
	}
	if strings.Contains(w.Body.String(), "secret_col") {
		t.Errorf("内部细节泄漏到响应体: %s", w.Body.String())
	}
}

func TestBadRequestUsesFixedMessage(t *testing.T) {
	c, w := newErrorTestContext()
	badRequest(c, errors.New("binding: field Username required but missing"), "参数错误")

	if w.Code != http.StatusBadRequest {
		t.Errorf("HTTP 状态 = %d, 期望 400", w.Code)
	}
	body := decodeErrorBody(t, w)
	if body.Code != response.CodeBadRequest {
		t.Errorf("业务码 = %d, 期望 %d", body.Code, response.CodeBadRequest)
	}
	if body.Message != "参数错误" {
		t.Errorf("应返回固定文案, 实际 %q", body.Message)
	}
	if strings.Contains(w.Body.String(), "Username") {
		t.Errorf("binder 字段名泄漏到响应体: %s", w.Body.String())
	}
}

func TestWriteErrorNilIsNoop(t *testing.T) {
	c, w := newErrorTestContext()
	writeTicketOrderError(c, nil)
	if w.Body.Len() != 0 {
		t.Errorf("nil 错误不应写出响应体: %s", w.Body.String())
	}
}
