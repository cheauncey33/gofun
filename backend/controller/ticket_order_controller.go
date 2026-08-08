package controller

import (
	"errors"
	"gofun/pkg/response"
	"gofun/service"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type TicketOrderController struct {
	service *service.TicketOrderService
}

func NewTicketOrderController(service *service.TicketOrderService) *TicketOrderController {
	return &TicketOrderController{service: service}
}

func (ctrl *TicketOrderController) CreateOrder(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	idempotencyKey := c.GetHeader("X-Idempotency-Key")
	if idempotencyKey == "" {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "缺少 X-Idempotency-Key")
		return
	}
	var input service.CreateTicketOrderInput
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	requestID, _ := c.Get("request_id")
	receipt, err := ctrl.service.CreateOrder(
		c.Request.Context(), userID, idempotencyKey, stringifyRequestID(requestID), input,
	)
	if err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, receipt)
}

func (ctrl *TicketOrderController) GetOrder(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	order, err := ctrl.service.GetOrder(c.Request.Context(), userID, orderID)
	if err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, order)
}

func (ctrl *TicketOrderController) ListOrders(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	page, pageSize := parseTicketPage(c)
	orders, total, err := ctrl.service.ListOrders(c.Request.Context(), userID, page, pageSize)
	if err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.SuccessWithPage(c, orders, total, page, pageSize)
}

func (ctrl *TicketOrderController) PayOrder(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Scenario string `json:"scenario"`
	}
	_ = c.ShouldBindJSON(&input)
	intent, err := ctrl.service.PayOrder(c.Request.Context(), userID, orderID, input.Scenario)
	if err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, gin.H{
		"order_id": strconv.FormatInt(orderID, 10),
		"status":   intent.Status,
		"payment":  intent,
	})
}

// PaymentCallback receives the provider webhook. It intentionally has no
// user-auth middleware: provider credentials/signature authenticate it.
func (ctrl *TicketOrderController) PaymentCallback(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	var notification service.PaymentNotification
	if err := c.ShouldBindJSON(&notification); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidOrderStatus, "invalid payment callback")
		return
	}
	if err := ctrl.service.HandlePaymentCallback(c.Request.Context(), notification); err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, gin.H{"status": "accepted"})
}

func (ctrl *TicketOrderController) CancelOrder(c *gin.Context) {
	userID, ok := ticketUserID(c)
	if !ok {
		return
	}
	orderID, ok := parseTicketID(c, "id")
	if !ok {
		return
	}
	var input struct {
		Reason string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&input)
	if err := ctrl.service.CancelOrder(
		c.Request.Context(), userID, orderID, input.Reason,
	); err != nil {
		writeTicketOrderError(c, err)
		return
	}
	response.Success(c, gin.H{"order_id": strconv.FormatInt(orderID, 10), "status": "cancelled"})
}

func stringifyRequestID(value interface{}) string {
	if requestID, ok := value.(string); ok {
		return requestID
	}
	return ""
}

func writeTicketOrderError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrTicketOrderNotFound),
		errors.Is(err, service.ErrTicketResourceNotFound):
		response.Error(c, http.StatusNotFound, response.CodeOrderNotFound, err.Error())
	case errors.Is(err, service.ErrOrganizerForbidden):
		response.Error(c, http.StatusForbidden, response.CodeForbidden, err.Error())
	case errors.Is(err, service.ErrTicketQuotaInsufficient):
		response.Error(c, http.StatusConflict, response.CodeInsufficientStock, err.Error())
	case errors.Is(err, service.ErrTicketBalance):
		response.Error(c, http.StatusConflict, response.CodeInsufficientBalance, err.Error())
	case errors.Is(err, service.ErrTicketOrderState),
		errors.Is(err, service.ErrTicketAlreadyUsed),
		errors.Is(err, service.ErrTicketOrderUnavailable),
		errors.Is(err, service.ErrInvalidTicketCatalog),
		errors.Is(err, service.ErrPaymentSignature),
		errors.Is(err, service.ErrPaymentAmountMismatch),
		errors.Is(err, service.ErrPaymentInvalidNotification):
		response.Error(c, http.StatusBadRequest, response.CodeInvalidOrderStatus, err.Error())
	case errors.Is(err, service.ErrPaymentNotFound),
		errors.Is(err, service.ErrPaymentNotRefundable):
		response.Error(c, http.StatusConflict, response.CodeInvalidOrderStatus, err.Error())
	default:
		log.Printf("ticket order request failed: %v", err)
		response.Error(c, http.StatusInternalServerError, response.CodeOrderCreateFailed, "票务订单处理失败")
	}
}
