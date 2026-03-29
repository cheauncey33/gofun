package controller

import (
	"WHU_Snack_GO/service"

	"github.com/gin-gonic/gin"
)

func CreateOrderHandler(c *gin.Context) {
	//存放网页发来的订单请求
	var req service.CreateOrderInput

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{
			"code": 400,
			"msg":  "参数错误" + err.Error(),
		})
		return
	}
	err := service.CreateOrder(service.CreateOrderInput(req))
	if err != nil {
		c.JSON(500, gin.H{
			"code": 500,
			"msg":  err.Error(),
		})
		return
	}
	c.JSON(200, gin.H{
		"code": 200,
		"msg":  "下单成功",
	})

}
