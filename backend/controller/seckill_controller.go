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

// ===== 用户端 =====
func GetSeckillListHandler(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "10"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 10
	}

	activities, total, err := service.GetSeckillActivityList(page, pageSize)
	if err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "获取秒杀列表失败")
		return
	}
	response.SuccessWithPage(c, activities, total, page, pageSize)
}

func GetSeckillDetailHandler(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	activity, err := service.GetSeckillActivityDetail(id)
	if err != nil {
		response.Error(c, http.StatusNotFound, response.CodeNotFound, "活动不存在")
		return
	}
	response.Success(c, activity)
}

func GetSeckillTokenHandler(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	activityID, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	token, err := service.GenerateSeckillToken(activityID, userID)
	if err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, gin.H{"token": token, "expire_secs": 60})
}

func ExecuteSeckillHandler(c *gin.Context) {
	userID, ok := common.GetUserID(c)
	if !ok {
		response.Error(c, http.StatusUnauthorized, response.CodeUnauthorized, "未登录")
		return
	}
	activityID, _ := strconv.ParseInt(c.Param("id"), 10, 64)

	var req struct {
		Token    string `json:"token" binding:"required"`
		Quantity int    `json:"quantity" binding:"omitempty,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if req.Quantity <= 0 {
		req.Quantity = 1
	}

	if err := service.ExecuteSeckill(activityID, userID, req.Token, req.Quantity); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}

// ===== 管理端 =====
func AdminCreateSeckillHandler(c *gin.Context) {
	var activity models.SeckillActivity
	if err := c.ShouldBindJSON(&activity); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误: "+err.Error())
		return
	}
	if err := service.CreateSeckillActivity(&activity); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "创建活动失败")
		return
	}
	response.Success(c, activity)
}

func AdminUpdateSeckillHandler(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	var activity models.SeckillActivity
	if err := c.ShouldBindJSON(&activity); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, "参数错误")
		return
	}
	if err := service.UpdateSeckillActivity(id, &activity); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "更新活动失败")
		return
	}
	response.Success(c, nil)
}

func AdminDeleteSeckillHandler(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := service.DeleteSeckillActivity(id); err != nil {
		response.Error(c, http.StatusInternalServerError, response.CodeInternalError, "删除活动失败")
		return
	}
	response.Success(c, nil)
}

func AdminWarmUpSeckillHandler(c *gin.Context) {
	id, _ := strconv.ParseInt(c.Param("id"), 10, 64)
	if err := service.WarmUpSeckillActivity(id); err != nil {
		response.Error(c, http.StatusBadRequest, response.CodeBadRequest, err.Error())
		return
	}
	response.Success(c, nil)
}
