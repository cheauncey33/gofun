package controller

import (
	"gofun/common"
	"gofun/pkg/response"
	"gofun/service"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

type LoginReq struct {
	Username string `json:"username" binding:"required,username"`
	Password string `json:"password" binding:"required,password"`
}

type RegisterReq struct {
	Username string `json:"username" binding:"required,username"`
	Password string `json:"password" binding:"required,password"`
}

type RefreshReq struct {
	RefreshToken string `json:"refresh_token" binding:"required"`
}

type LogoutReq struct {
	RefreshToken string `json:"refresh_token"`
}

type UserController struct {
	userSvc *service.UserService
}

func NewUserController(userSvc *service.UserService) *UserController {
	return &UserController{userSvc: userSvc}
}

func (ctrl *UserController) Register(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := ctrl.userSvc.Register(req.Username, req.Password); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeUserExists, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *UserController) Login(c *gin.Context) {
	var req LoginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	tokens, err := ctrl.userSvc.Login(req.Username, req.Password)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, err.Error())
		return
	}
	response.Success(c, tokens)
}

func (ctrl *UserController) Refresh(c *gin.Context) {
	var req RefreshReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	tokens, err := ctrl.userSvc.Refresh(req.RefreshToken)
	if err != nil {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, err.Error())
		return
	}
	response.Success(c, tokens)
}

func (ctrl *UserController) Logout(c *gin.Context) {
	var req LogoutReq
	_ = c.ShouldBindJSON(&req)
	if err := ctrl.userSvc.Logout(req.RefreshToken); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "退出登录失败")
		return
	}
	response.Success(c, nil)
}

func (ctrl *UserController) GetUserInfo(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	user, err := ctrl.userSvc.GetUserInfo(userID)
	if err != nil {
		response.Error(c, http.StatusNotFound, response.CodeUserNotFound, "用户不存在")
		return
	}
	response.Success(c, user)
}

func (ctrl *UserController) UpdateUserInfo(c *gin.Context) {
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
	if err := ctrl.userSvc.UpdateUserInfo(userID, req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *UserController) ChangePassword(c *gin.Context) {
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
	if err := ctrl.userSvc.ChangePassword(userID, req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *UserController) ListAttendees(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	rows, err := ctrl.userSvc.ListAttendees(userID)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "加载观演人失败")
		return
	}
	response.Success(c, rows)
}

func (ctrl *UserController) CreateAttendee(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	var req service.SaveAttendeeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	row, err := ctrl.userSvc.CreateAttendee(userID, req)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, row)
}

func (ctrl *UserController) DeleteAttendee(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	attendeeID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || attendeeID <= 0 {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if err := ctrl.userSvc.DeleteAttendee(userID, attendeeID); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}

func (ctrl *UserController) ListFavorites(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	rows, err := ctrl.userSvc.ListFavorites(userID, c.Query("type"))
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "加载收藏失败")
		return
	}
	response.Success(c, rows)
}

func (ctrl *UserController) AddFavorite(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	var req service.AddFavoriteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	row, err := ctrl.userSvc.AddFavorite(userID, req)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, row)
}

func (ctrl *UserController) DeleteFavorite(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "用户信息获取失败")
		return
	}
	favoriteID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || favoriteID <= 0 {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if err := ctrl.userSvc.RemoveFavorite(userID, favoriteID); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}
