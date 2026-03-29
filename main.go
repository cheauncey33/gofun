package main

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/controller"

	"github.com/gin-gonic/gin"
)

func main() {
	common.InitDB()
	r := gin.Default()
	v1 := r.Group("/api/v1")
	{
		v1.POST("/orders", controller.CreateOrderHandler)
	}
	r.Run("127.0.0.1:8080")
}
