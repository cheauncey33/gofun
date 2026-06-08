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

type AddressController struct {
	addressSvc *service.AddressService
}

func NewAddressController(addressSvc *service.AddressService) *AddressController {
	return &AddressController{addressSvc: addressSvc}
}

func (ctrl *AddressController) CreateAddress(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	var addr models.Address
	if err := c.ShouldBindJSON(&addr); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := ctrl.addressSvc.CreateAddress(userID, &addr); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "地址创建失败")
		return
	}
	response.Success(c, addr)
}

func (ctrl *AddressController) UpdateAddress(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	addressID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "地址ID格式错误")
		return
	}
	var addr models.Address
	if err := c.ShouldBindJSON(&addr); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := ctrl.addressSvc.UpdateAddress(userID, addressID, &addr); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *AddressController) DeleteAddress(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	addressID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "地址ID格式错误")
		return
	}
	if err := ctrl.addressSvc.DeleteAddress(userID, addressID); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *AddressController) ListAddresses(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	addresses, err := ctrl.addressSvc.ListAddresses(userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "地址列表获取失败")
		return
	}
	response.Success(c, addresses)
}

func (ctrl *AddressController) SetDefaultAddress(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	addressID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "地址ID格式错误")
		return
	}
	if err := ctrl.addressSvc.SetDefaultAddress(userID, addressID); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error())
		return
	}
	response.Success(c, nil)
}
