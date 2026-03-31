package main

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/controller"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	common.InitDB()
	// common.SeedData()
	r := gin.Default()

	//可以替换为
	//r.Use(cors.Default())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://127.0.0.1:5173", "http://localhost:5173"},
		AllowMethods:     []string{"POST", "GET", "DELETE", "PUT", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))
	v1 := r.Group("/api/v1")
	{
		v1.POST("/login", controller.LoginHandler)
		v1.POST("/register", controller.RegisterHandler)
		auth := v1.Group("/")
		auth.Use(common.AuthMiddleware())
		{
			auth.POST("/orders", controller.CreateOrderHandler)
			auth.GET("/user/info", controller.GetUserInfoHandler)
		}
	}
	r.Run("127.0.0.1:8080")
}
