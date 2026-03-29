package main

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/controller"

	"github.com/gin-gonic/gin"
)

func main() {
	common.InitDB()
	common.SeedData()
	r := gin.Default()
	v1 := r.Group("/api/v1")
	{
		v1.POST("/orders", controller.CreateOrderHandler)
		v1.GET("/products", controller.GetProductHandler)
	}
	r.Run("127.0.0.1:8080")
}
