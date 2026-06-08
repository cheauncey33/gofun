package controller

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/pkg/response"
	"WHU_Snack_GO/service"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type OrderController struct {
	orderSvc *service.OrderService
}

func NewOrderController(orderSvc *service.OrderService) *OrderController {
	return &OrderController{orderSvc: orderSvc}
}

func (ctrl *OrderController) CreateOrder(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return
	}
	var req service.CreateOrderInput
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := ctrl.orderSvc.CreateOrder(userID, req); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeOrderCreateFailed, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *OrderController) GetOrderList(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}

	var status *int
	if s := c.Query("status"); s != "" {
		if st, e := strconv.Atoi(s); e == nil {
			status = &st
		}
	}
	orders, total, err := ctrl.orderSvc.GetOrderList(userID, page, pageSize, status)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "订单列表获取失败")
		return
	}
	response.SuccessWithPage(c, orders, total, page, pageSize)
}

func (ctrl *OrderController) GetOrderDetail(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return
	}
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "订单ID格式错误")
		return
	}
	order, err := ctrl.orderSvc.GetOrderDetail(orderID, userID)
	if err != nil {
		response.Error(c, http.StatusNotFound, response.CodeOrderNotFound, "订单不存在")
		return
	}
	response.Success(c, order)
}

func (ctrl *OrderController) CancelOrder(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return
	}
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "订单ID格式错误")
		return
	}
	var req struct{ Reason string `json:"reason"` }
	_ = c.ShouldBindJSON(&req)

	if err := ctrl.orderSvc.CancelOrder(orderID, userID, req.Reason); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidOrderStatus, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *OrderController) RequestRefund(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return
	}
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "订单ID格式错误")
		return
	}
	var req struct{ Reason string `json:"reason"` }
	_ = c.ShouldBindJSON(&req)

	if err := ctrl.orderSvc.RequestRefund(orderID, userID, req.Reason); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidOrderStatus, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *OrderController) PayOrder(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return
	}
	orderID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "订单ID格式错误")
		return
	}
	if err := ctrl.orderSvc.PayOrder(orderID, userID); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidOrderStatus, err.Error())
		return
	}
	response.Success(c, nil)
}
