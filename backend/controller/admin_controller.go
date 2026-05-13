package controller

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/pkg/response"
	"WHU_Snack_GO/service"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func requireAdmin(c *gin.Context) (int64, bool) {
	uid, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return 0, false
	}
	return uid, true
}

// ===== Dashboard =====
func AdminDashboardHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	stats, err := service.GetDashboard()
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "获取数据失败")
		return
	}
	response.Success(c, stats)
}

// ===== Product Management =====
func AdminCreateProductHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	var product models.Product
	if err := c.ShouldBindJSON(&product); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error()); return
	}
	if err := service.AdminCreateProduct(&product); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "创建商品失败"); return
	}
	response.Success(c, product)
}

func AdminUpdateProductHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误"); return
	}
	if err := service.AdminUpdateProduct(id, updates); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeProductNotFound, err.Error()); return
	}
	response.Success(c, nil)
}

func AdminDeleteProductHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := service.AdminDeleteProduct(id); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeProductNotFound, err.Error()); return
	}
	response.Success(c, nil)
}

func AdminUpdateProductStatusHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Status models.ProductStatus `json:"status"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误"); return
	}
	if err := service.AdminUpdateProductStatus(id, req.Status); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "更新失败"); return
	}
	response.Success(c, nil)
}

// ===== Order Management =====
func AdminGetAllOrdersHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 { page = 1 }
	if pageSize < 1 || pageSize > 50 { pageSize = 10 }

	var status *models.OrderStatus
	if s := c.Query("status"); s != "" {
		if st, err := strconv.Atoi(s); err == nil {
			os := models.OrderStatus(st); status = &os
		}
	}
	orders, total, err := service.AdminGetAllOrders(page, pageSize, status)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "获取订单列表失败"); return
	}
	response.SuccessWithPage(c, orders, total, page, pageSize)
}

func AdminUpdateOrderStatusHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Status models.OrderStatus `json:"status" binding:"required"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误"); return
	}
	if err := service.AdminUpdateOrderStatus(id, req.Status); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error()); return
	}
	response.Success(c, nil)
}

// ===== User Management =====
func AdminGetUserListHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 { page = 1 }
	if pageSize < 1 || pageSize > 50 { pageSize = 10 }

	users, total, err := service.AdminGetUserList(page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "获取用户列表失败"); return
	}
	response.SuccessWithPage(c, users, total, page, pageSize)
}

func AdminUpdateUserRoleHandler(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok { return }
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Role string `json:"role" binding:"required"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误"); return
	}
	if err := service.AdminUpdateUserRole(id, req.Role); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error()); return
	}
	response.Success(c, nil)
}
