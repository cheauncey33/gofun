package controller

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/pkg/response"
	"WHU_Snack_GO/service"
	"net/http"

	"github.com/gin-gonic/gin"
)

type LoginReq struct {
	Username string `json:"username" binding:"required,username"`
	Password string `json:"password" binding:"required,password"`
}

type RegisterReq struct {
	Username string `json:"username" binding:"required,username"`
	Password string `json:"password" binding:"required,password"`
	DormID   int64  `json:"dorm_id" binding:"required,gt=0"`
}

func RegisterHandler(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := service.Register(req.Username, req.Password, req.DormID); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeUserExists, err.Error())
		return
	}
	response.Success(c, nil)
}

func LoginHandler(c *gin.Context) {
	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	token, err := service.Login(req.Username, req.Password)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, err.Error())
		return
	}
	response.Success(c, gin.H{"token": token})
}

func GetUserInfoHandler(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	user, err := service.GetUserInfo(userID)
	if err != nil {
		response.Error(c, http.StatusNotFound, response.CodeUserNotFound, "用户不存在")
		return
	}
	response.Success(c, user)
}

func UpdateUserInfoHandler(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	var req service.UpdateUserInfoReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := service.UpdateUserInfo(userID, req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}

func ChangePasswordHandler(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	var req service.ChangePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := service.ChangePassword(userID, req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}
