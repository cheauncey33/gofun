package controller

import (
	"WHU_Snack_GO/service"

	"github.com/gin-gonic/gin"
)

type LoginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

type RegisterReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
	DormID   int64  `json:"dorm_id" binding:"required"`
}

func RegisterHandler(c *gin.Context) {
	var req RegisterReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}
	if err := service.Register(req.Username, req.Password, req.DormID); err != nil {
		c.JSON(400, gin.H{
			"code": 400,
			"msg":  err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"code": 200,
		"msg":  "注册成功",
	})

}
func LoginHandler(c *gin.Context) {

	var req LoginReq
	if err := c.ShouldBindBodyWithJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"code": 400,
			"msg":  "参数错误",
		})
		return
	}
	token, err := service.Login(req.Username, req.Password)
	if err != nil {
		c.JSON(401, gin.H{
			"code": 401,
			"msg":  err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"token": token,
	})
}
func GetUserInfoHandler(c *gin.Context) {
	uid, ok := c.Get("user_id")
	if !ok {
		c.JSON(401, gin.H{
			"code": 401,
			"msg":  "用户信息获取失败",
		})
		return
	}
	user, err := service.GetUserInfo(uid.(int64))
	if err != nil {
		c.JSON(404, gin.H{
			"code": 404,
			"msg":  "用户不存在",
		})
		return
	}
	c.JSON(200, gin.H{
		"code": 200,
		"msg":  "获取成功",
		"data": user,
	})

}
