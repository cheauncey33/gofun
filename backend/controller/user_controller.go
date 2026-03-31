package controller

import (
	"WHU_Snack_GO/service"

	"github.com/gin-gonic/gin"
)

type LoginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
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
