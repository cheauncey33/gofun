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

type AdminController struct {
	adminSvc *service.AdminService
}

func NewAdminController(adminSvc *service.AdminService) *AdminController {
	return &AdminController{adminSvc: adminSvc}
}

func requireAdmin(c *gin.Context) (int64, bool) {
	uid, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return 0, false
	}
	return uid, true
}

func (ctrl *AdminController) Dashboard(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	stats, err := ctrl.adminSvc.GetDashboard()
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "获取数据失败")
		return
	}
	response.Success(c, stats)
}

func (ctrl *AdminController) CreateProduct(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	var product models.Product
	if err := c.ShouldBindJSON(&product); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := ctrl.adminSvc.AdminCreateProduct(&product); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "创建商品失败")
		return
	}
	response.Success(c, product)
}

func (ctrl *AdminController) UpdateProduct(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if err := ctrl.adminSvc.AdminUpdateProduct(id, updates); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeProductNotFound, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *AdminController) DeleteProduct(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := ctrl.adminSvc.AdminDeleteProduct(id); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeProductNotFound, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *AdminController) UpdateProductStatus(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Status models.ProductStatus `json:"status"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if err := ctrl.adminSvc.AdminUpdateProductStatus(id, req.Status); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "更新失败")
		return
	}
	response.Success(c, nil)
}

func (ctrl *AdminController) GetAllOrders(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
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

	var status *models.OrderStatus
	if s := c.Query("status"); s != "" {
		if st, err := strconv.Atoi(s); err == nil {
			os := models.OrderStatus(st)
			status = &os
		}
	}
	orders, total, err := ctrl.adminSvc.AdminGetAllOrders(page, pageSize, status)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "获取订单列表失败")
		return
	}
	response.SuccessWithPage(c, orders, total, page, pageSize)
}

func (ctrl *AdminController) UpdateOrderStatus(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Status models.OrderStatus `json:"status" binding:"required"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if err := ctrl.adminSvc.AdminUpdateOrderStatus(id, req.Status); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *AdminController) GetUserList(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
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

	users, total, err := ctrl.adminSvc.AdminGetUserList(page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "获取用户列表失败")
		return
	}
	response.SuccessWithPage(c, users, total, page, pageSize)
}

func (ctrl *AdminController) UpdateUserRole(c *gin.Context) {
	if _, ok := requireAdmin(c); !ok {
		return
	}
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var req struct{ Role string `json:"role" binding:"required"` }
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if err := ctrl.adminSvc.AdminUpdateUserRole(id, req.Role); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}
