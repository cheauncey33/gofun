package main

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/controller"
	"WHU_Snack_GO/metrics"
	"WHU_Snack_GO/pkg/logger"
	"WHU_Snack_GO/pkg/middleware"
	"WHU_Snack_GO/pkg/validator"
	"WHU_Snack_GO/service"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"golang.org/x/time/rate"
)

func main() {
	configPath := flag.String("config", "", "配置文件路径")
	flag.Parse()

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(fmt.Errorf("加载配置失败: %v", err))
	}

	// 初始化日志
	if err := logger.Init(logger.Config{
		Level:      cfg.Log.Level,
		FilePath:   cfg.Log.FilePath,
		MaxSize:    cfg.Log.MaxSize,
		MaxBackups: cfg.Log.MaxBackups,
		MaxAge:     cfg.Log.MaxAge,
	}); err != nil {
		panic(fmt.Errorf("初始化日志失败: %v", err))
	}
	defer logger.Sync()

	logger.Log.Info("WHU_Snack_GO 启动中...")

	// 设置 Gin 模式
	gin.SetMode(cfg.Server.Mode)

	// 初始化基础设施
	common.SetJWTSecret(cfg.JWT.Secret)
	common.InitDB(cfg.MySQL)
	common.InitRedis(cfg.Redis)
	common.InitRabbitMQ(cfg.RabbitMQ)
	common.InitSnowFlake(cfg.Snowflake.NodeID)
	common.InitLocalCache()

	// 初始化校验器
	validator.Init()

	// 预热商品库存到 Redis
	if err := service.InitProductStockToRedis(); err != nil {
		logger.Log.Fatal("商品预热redis失败", zap.Error(err))
	}

	// 创建 Gin 路由
	r := gin.Default()

	// 中间件：request_id → metrics → 限流 → CORS
	r.Use(middleware.RequestIDMiddleware())
	r.Use(metrics.PrometheusMiddleware())
	r.Use(common.GlobalRateLimitMiddleware(
		rate.Limit(cfg.RateLimit.GlobalRate),
		cfg.RateLimit.GlobalBurst,
	))
	r.Use(common.IPRateLimitMiddleware(
		rate.Limit(cfg.RateLimit.IPRate),
		cfg.RateLimit.IPBurst,
	))
	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.Cors.AllowOrigins,
		AllowMethods:     []string{"POST", "GET", "DELETE", "PUT", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID"},
		ExposeHeaders:    []string{"X-Request-ID"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 路由注册
	v1 := r.Group("/api/v1")
	{
		v1.POST("/login", controller.LoginHandler)
		v1.POST("/register", controller.RegisterHandler)

		auth := v1.Group("/")
		auth.Use(common.AuthMiddleware())
		{
			// 订单
			auth.POST("/orders", controller.CreateOrderHandler)
			auth.GET("/orders", controller.GetOrderListHandler)
			auth.GET("/orders/:id", controller.GetOrderDetailHandler)
			auth.POST("/orders/:id/cancel", controller.CancelOrderHandler)
			auth.POST("/orders/:id/refund", controller.RequestRefundHandler)

			// 用户
			auth.GET("/user/info", controller.GetUserInfoHandler)
			auth.PUT("/user/info", controller.UpdateUserInfoHandler)
			auth.PUT("/user/password", controller.ChangePasswordHandler)

			// 收货地址
			auth.POST("/addresses", controller.CreateAddressHandler)
			auth.GET("/addresses", controller.ListAddressHandler)
			auth.PUT("/addresses/:id", controller.UpdateAddressHandler)
			auth.DELETE("/addresses/:id", controller.DeleteAddressHandler)
			auth.PUT("/addresses/:id/default", controller.SetDefaultAddressHandler)

			// 商品
			auth.GET("/products", controller.GetProductHandler)
			auth.GET("/products/:id", controller.GetProductDetailHandler)
			auth.GET("/categories", controller.GetCategoryListHandler)
			auth.GET("/categories/:id/products", controller.GetCategoryProductsHandler)

			// 秒杀(用户端)
			auth.GET("/seckill/activities", controller.GetSeckillListHandler)
			auth.GET("/seckill/activities/:id", controller.GetSeckillDetailHandler)
			auth.POST("/seckill/activities/:id/token", controller.GetSeckillTokenHandler)
			auth.POST("/seckill/activities/:id/execute", controller.ExecuteSeckillHandler)
		}

		// 管理员路由
		admin := v1.Group("/admin")
		admin.Use(common.AuthMiddleware(), common.AdminAuthMiddleware())
		{
			admin.GET("/dashboard", controller.AdminDashboardHandler)
			admin.POST("/products", controller.AdminCreateProductHandler)
			admin.PUT("/products/:id", controller.AdminUpdateProductHandler)
			admin.DELETE("/products/:id", controller.AdminDeleteProductHandler)
			admin.PUT("/products/:id/status", controller.AdminUpdateProductStatusHandler)
			admin.GET("/orders", controller.AdminGetAllOrdersHandler)
			admin.PUT("/orders/:id/status", controller.AdminUpdateOrderStatusHandler)
			admin.GET("/users", controller.AdminGetUserListHandler)
			admin.PUT("/users/:id/role", controller.AdminUpdateUserRoleHandler)
			admin.POST("/seckill", controller.AdminCreateSeckillHandler)
			admin.PUT("/seckill/:id", controller.AdminUpdateSeckillHandler)
			admin.DELETE("/seckill/:id", controller.AdminDeleteSeckillHandler)
			admin.POST("/seckill/:id/warmup", controller.AdminWarmUpSeckillHandler)
		}
	}

	// Prometheus /metrics endpoint
	metrics.RegisterHandler(r)

	// 后台 goroutine
	mainCtx, cancel := context.WithCancel(context.Background())
	go service.StartOrderConsumer(mainCtx, cfg.OrderConsumer)

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: r,
	}

	go func() {
		logger.Log.Info("HTTP 服务启动", zap.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Log.Error("HTTP 服务异常退出", zap.Error(err))
		}
	}()

	// IP限流器定期清理
	go func() {
		ticker := time.NewTicker(30 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-mainCtx.Done():
				return
			case <-ticker.C:
				common.CleanupIPLimiters()
			}
		}
	}()

	// 库存补偿定时任务
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-mainCtx.Done():
				logger.Log.Info("库存补偿定时任务退出")
				return
			case <-ticker.C:
				service.RunStockCompensation()
			}
		}
	}()

	// 优雅关闭
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Log.Info("收到关机信号，开始收尾...")

	cancel()
	time.Sleep(time.Second * 2)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Log.Error("Server Shutdown Error", zap.Error(err))
	}
	logger.Log.Info("程序退出成功")
}
