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

func getUserID(c *gin.Context) (int64, bool) { return common.GetUserID(c) }

func CreateAddressHandler(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok { response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录"); return }
	var addr models.Address
	if err := c.ShouldBindJSON(&addr); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error()); return
	}
	if err := service.CreateAddress(userID, &addr); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "地址创建失败"); return
	}
	response.Success(c, addr)
}

func UpdateAddressHandler(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok { response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录"); return }
	addressID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil { response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "地址ID格式错误"); return }
	var addr models.Address
	if err := c.ShouldBindJSON(&addr); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error()); return
	}
	if err := service.UpdateAddress(userID, addressID, &addr); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error()); return
	}
	response.Success(c, nil)
}

func DeleteAddressHandler(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok { response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录"); return }
	addressID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil { response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "地址ID格式错误"); return }
	if err := service.DeleteAddress(userID, addressID); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error()); return
	}
	response.Success(c, nil)
}

func ListAddressHandler(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok { response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录"); return }
	addresses, err := service.ListAddresses(userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "地址列表获取失败"); return
	}
	response.Success(c, addresses)
}

func SetDefaultAddressHandler(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok { response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录"); return }
	addressID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil { response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "地址ID格式错误"); return }
	if err := service.SetDefaultAddress(userID, addressID); err != nil {
		response.Error(c, http.StatusNotFound, response.CodeNotFound, err.Error()); return
	}
	response.Success(c, nil)
}
