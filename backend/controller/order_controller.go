package controller

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/pkg/response"
	"WHU_Snack_GO/service"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func CreateOrderHandler(c *gin.Context) {
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
	if err := service.CreateOrder(userID, req); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeOrderCreateFailed, err.Error())
		return
	}
	response.Success(c, nil)
}

func GetOrderListHandler(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户未授权")
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 { page = 1 }
	if pageSize < 1 || pageSize > 50 { pageSize = 10 }

	var status *int
	if s := c.Query("status"); s != "" {
		if st, e := strconv.Atoi(s); e == nil { status = &st }
	}
	orders, total, err := service.GetOrderList(userID, page, pageSize, status)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "订单列表获取失败")
		return
	}
	response.SuccessWithPage(c, orders, total, page, pageSize)
}

func GetOrderDetailHandler(c *gin.Context) {
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
	order, err := service.GetOrderDetail(orderID, userID)
	if err != nil {
		response.Error(c, http.StatusNotFound, response.CodeOrderNotFound, "订单不存在")
		return
	}
	response.Success(c, order)
}

func CancelOrderHandler(c *gin.Context) {
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
	_ = c.ShouldBindJSON(&req) // reason is optional

	if err := service.CancelOrder(orderID, userID, req.Reason); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidOrderStatus, err.Error())
		return
	}
	response.Success(c, nil)
}

func RequestRefundHandler(c *gin.Context) {
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
	_ = c.ShouldBindJSON(&req) // reason is optional

	if err := service.RequestRefund(orderID, userID, req.Reason); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeInvalidOrderStatus, err.Error())
		return
	}
	response.Success(c, nil)
}
