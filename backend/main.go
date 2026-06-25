package main

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/controller"
	"WHU_Snack_GO/metrics"
	"WHU_Snack_GO/pkg/logger"
	"WHU_Snack_GO/pkg/middleware"
	"WHU_Snack_GO/pkg/validator"
	"WHU_Snack_GO/pkg/ws"
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

	cfg, err := config.Load(*configPath)
	if err != nil {
		panic(fmt.Errorf("加载配置失败: %v", err))
	}

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
	gin.SetMode(cfg.Server.Mode)

	// 创建 DI 容器（内部同时设置 common 全局变量兼容 middleware）
	cont, err := container.NewContainer(cfg)
	if err != nil {
		panic(fmt.Errorf("初始化容器失败: %v", err))
	}

	validator.Init()

	// 构建 service 层
	lockTimeoutSec := cfg.DelayedOrder.LockTimeoutSec
	if lockTimeoutSec <= 0 {
		lockTimeoutSec = 10
	}
	timeoutMinutes := cfg.DelayedOrder.TimeoutMinutes
	if timeoutMinutes <= 0 {
		timeoutMinutes = 15
	}

	orderSvc := service.NewOrderService(cont, lockTimeoutSec)
	seckillSvc := service.NewSeckillService(cont, lockTimeoutSec)
	productSvc := service.NewProductService(cont)
	userSvc := service.NewUserService(cont, cfg.JWT.ExpireSecs, cfg.JWT.RefreshExpireSecs)
	adminSvc := service.NewAdminService(cont)
	addressSvc := service.NewAddressService(cont)
	compSvc := service.NewCompensationService(cont)
	consumerSvc := service.NewOrderConsumerService(cont.NewMQChannel, cont.MQQueueName, orderSvc)
	consumerSvc.SetDeadLetterConfig(cont.MQRetryQueueName, cont.MQDLXName, cont.MQDLQName)

	// Phase 3: 延时队列初始化
	orderTimeoutSvc := service.NewOrderTimeoutService(
		cont.DB, cont.RDB, cont.MQConn,
		cont.OrderRepo, cont.ProductRepo,
		timeoutMinutes,
	)
	if err := orderTimeoutSvc.SetupTimeoutInfrastructure(); err != nil {
		logger.Log.Fatal("订单超时队列初始化失败", zap.Error(err))
	}
	orderSvc.SetPublishTimeout(orderTimeoutSvc.PublishDelayedOrderTimeout)

	// WebSocket Hub：订单状态变更主动推送给前端，替代轮询。
	wsHub := ws.NewHub()
	go wsHub.Run()
	orderSvc.SetNotifier(func(userID int64, ev service.OrderStatusEvent) {
		wsHub.PushJSON(userID, ev)
	})
	orderTimeoutSvc.SetNotifier(func(userID int64, ev service.OrderStatusEvent) {
		wsHub.PushJSON(userID, ev)
	})

	// 构建 controller 层
	orderCtrl := controller.NewOrderController(orderSvc)
	seckillCtrl := controller.NewSeckillController(seckillSvc)
	productCtrl := controller.NewProductController(productSvc)
	userCtrl := controller.NewUserController(userSvc)
	adminCtrl := controller.NewAdminController(adminSvc)
	addressCtrl := controller.NewAddressController(addressSvc)
	wsCtrl := controller.NewWSController(wsHub)

	// 预热商品库存
	if err := productSvc.InitProductStockToRedis(); err != nil {
		logger.Log.Fatal("商品预热redis失败", zap.Error(err))
	}

	// 创建路由
	r := gin.Default()
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
		v1.POST("/login", userCtrl.Login)
		v1.POST("/register", userCtrl.Register)
		v1.POST("/auth/refresh", userCtrl.Refresh)
		v1.POST("/logout", userCtrl.Logout)

		// WebSocket 订单实时推送：浏览器无法自定义请求头，token 走查询参数 ?token=，在 Handle 内自行鉴权。
		v1.GET("/ws", wsCtrl.Handle)

		auth := v1.Group("/")
		auth.Use(common.AuthMiddleware())
		{
			auth.POST("/orders", orderCtrl.CreateOrder)
			auth.GET("/orders", orderCtrl.GetOrderList)
			auth.GET("/orders/:id", orderCtrl.GetOrderDetail)
			auth.POST("/orders/:id/cancel", orderCtrl.CancelOrder)
			auth.POST("/orders/:id/refund", orderCtrl.RequestRefund)
			auth.POST("/orders/:id/pay", orderCtrl.PayOrder)
			auth.POST("/orders/:id/confirm", orderCtrl.ConfirmOrder)

			auth.GET("/user/info", userCtrl.GetUserInfo)
			auth.PUT("/user/info", userCtrl.UpdateUserInfo)
			auth.PUT("/user/password", userCtrl.ChangePassword)

			auth.POST("/addresses", addressCtrl.CreateAddress)
			auth.GET("/addresses", addressCtrl.ListAddresses)
			auth.PUT("/addresses/:id", addressCtrl.UpdateAddress)
			auth.DELETE("/addresses/:id", addressCtrl.DeleteAddress)
			auth.PUT("/addresses/:id/default", addressCtrl.SetDefaultAddress)

			auth.GET("/products", productCtrl.GetProducts)
			auth.GET("/products/:id", productCtrl.GetProductDetail)
			auth.GET("/categories", productCtrl.GetCategoryList)
			auth.GET("/categories/:id/products", productCtrl.GetCategoryProducts)

			auth.GET("/seckill/activities", seckillCtrl.GetSeckillList)
			auth.GET("/seckill/activities/:id", seckillCtrl.GetSeckillDetail)
			auth.POST("/seckill/activities/:id/token", seckillCtrl.GetSeckillToken)
			auth.POST("/seckill/activities/:id/execute", seckillCtrl.ExecuteSeckill)
		}

		admin := v1.Group("/admin")
		admin.Use(common.AuthMiddleware(), common.AdminAuthMiddleware())
		{
			admin.GET("/dashboard", adminCtrl.Dashboard)
			admin.POST("/products", adminCtrl.CreateProduct)
			admin.PUT("/products/:id", adminCtrl.UpdateProduct)
			admin.DELETE("/products/:id", adminCtrl.DeleteProduct)
			admin.PUT("/products/:id/status", adminCtrl.UpdateProductStatus)
			admin.GET("/orders", adminCtrl.GetAllOrders)
			admin.PUT("/orders/:id/status", adminCtrl.UpdateOrderStatus)
			admin.GET("/users", adminCtrl.GetUserList)
			admin.PUT("/users/:id/role", adminCtrl.UpdateUserRole)
			admin.POST("/seckill", seckillCtrl.AdminCreateSeckill)
			admin.PUT("/seckill/:id", seckillCtrl.AdminUpdateSeckill)
			admin.DELETE("/seckill/:id", seckillCtrl.AdminDeleteSeckill)
			admin.POST("/seckill/:id/warmup", seckillCtrl.AdminWarmUpSeckill)
		}
	}

	metrics.RegisterHandler(r)

	// 后台 goroutine
	mainCtx, cancel := context.WithCancel(context.Background())
	go consumerSvc.Start(mainCtx, cfg.OrderConsumer)
	go orderTimeoutSvc.StartTimeoutConsumer(mainCtx, 2)

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

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-mainCtx.Done():
				logger.Log.Info("库存补偿定时任务退出")
				return
			case <-ticker.C:
				compSvc.RunStockCompensation()
			}
		}
	}()

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
