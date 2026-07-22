package main

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/controller"
	"WHU_Snack_GO/metrics"
	"WHU_Snack_GO/pkg/logger"
	"WHU_Snack_GO/pkg/middleware"
	"WHU_Snack_GO/pkg/response"
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

	logger.Log.Info("赴场票务服务启动中...")
	gin.SetMode(cfg.Server.Mode)

	// 创建 DI 容器（内部同时设置 common 全局变量兼容 middleware）
	cont, err := container.NewContainer(cfg)
	if err != nil {
		panic(fmt.Errorf("初始化容器失败: %v", err))
	}

	validator.Init()

	// 构建 service 层
	timeoutMinutes := cfg.DelayedOrder.TimeoutMinutes
	if timeoutMinutes <= 0 {
		timeoutMinutes = 15
	}

	userSvc := service.NewUserService(cont, cfg.JWT.ExpireSecs, cfg.JWT.RefreshExpireSecs)
	ticketCatalogSvc := service.NewTicketCatalogService(cont)
	ticketOrderSvc := service.NewTicketOrderService(cont, timeoutMinutes)
	ticketVerificationSvc := service.NewTicketVerificationService(cont)
	rushSaleSvc := service.NewRushSaleService(cont, ticketCatalogSvc, ticketOrderSvc)
	ticketOrderConsumer := service.NewTicketOrderConsumer(
		cont.NewMQChannel,
		cont.MQQueueName,
		cont.MQRetryQueueName,
		cont.MQDLXName,
		cont.MQDLQName,
		ticketOrderSvc,
	)

	// 构建 controller 层
	userCtrl := controller.NewUserController(userSvc)
	ticketCatalogCtrl := controller.NewTicketCatalogController(ticketCatalogSvc, ticketOrderSvc)
	ticketOrderCtrl := controller.NewTicketOrderController(ticketOrderSvc)
	ticketVerificationCtrl := controller.NewTicketVerificationController(ticketVerificationSvc)
	rushSaleCtrl := controller.NewRushSaleController(rushSaleSvc)
	ticketCompensationSvc := service.NewTicketCompensationService(cont)
	writeLimiter := common.NewIPRateLimiter(rate.Limit(5), 10)

	// MySQL 是最终票额来源，启动时将票档剩余量写入赴场独立 Redis 命名空间。
	if err := ticketOrderSvc.WarmTicketQuota(context.Background()); err != nil {
		logger.Log.Fatal("票档预热 Redis 失败", zap.Error(err))
	}
	if err := ticketOrderSvc.RecoverQueuedOrders(context.Background()); err != nil {
		logger.Log.Fatal("queued 票务订单恢复失败", zap.Error(err))
	}
	if err := ticketOrderSvc.BackfillPaidAdmissionTickets(context.Background()); err != nil {
		logger.Log.Fatal("历史已支付订单补签电子票失败", zap.Error(err))
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
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization", "X-Request-ID", "X-Idempotency-Key"},
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

		// 活动浏览无需登录；购票和主办方管理仍由各自的鉴权路由保护。
		v1.GET("/events", ticketCatalogCtrl.ListPublishedEvents)
		v1.GET("/events/:id", ticketCatalogCtrl.GetPublishedEvent)
		v1.GET("/rush-sales", rushSaleCtrl.ListCampaigns)

		auth := v1.Group("/")
		auth.Use(common.AuthMiddleware())
		{
			auth.POST("/orders", writeLimitMiddleware(writeLimiter), ticketOrderCtrl.CreateOrder)
			auth.GET("/orders", ticketOrderCtrl.ListOrders)
			auth.GET("/orders/:id", ticketOrderCtrl.GetOrder)
			auth.POST("/orders/:id/cancel", ticketOrderCtrl.CancelOrder)
			auth.POST("/orders/:id/pay", ticketOrderCtrl.PayOrder)
			auth.POST("/rush-sales/:id/token", writeLimitMiddleware(writeLimiter), rushSaleCtrl.IssueToken)
			auth.POST("/rush-sales/:id/execute", writeLimitMiddleware(writeLimiter), rushSaleCtrl.Execute)

			auth.GET("/user/info", userCtrl.GetUserInfo)
			auth.PUT("/user/info", userCtrl.UpdateUserInfo)
			auth.PUT("/user/password", userCtrl.ChangePassword)
			auth.GET("/organizers/mine", ticketCatalogCtrl.ListMyOrganizers)
		}

		organizer := v1.Group("/organizers/:organizer_id")
		organizer.Use(common.AuthMiddleware())
		{
			organizer.POST("/venues", ticketCatalogCtrl.CreateVenue)
			organizer.GET("/venues", ticketCatalogCtrl.ListVenues)
			organizer.POST("/events", ticketCatalogCtrl.CreateEvent)
			organizer.GET("/events", ticketCatalogCtrl.ListOrganizerEvents)
			organizer.GET("/overview", ticketCatalogCtrl.GetOrganizerOverview)
			organizer.GET("/orders", ticketCatalogCtrl.ListOrganizerOrders)
			organizer.POST("/verifications", ticketVerificationCtrl.Verify)
			organizer.GET("/verifications", ticketVerificationCtrl.ListRecords)
			organizer.POST("/events/:event_id/publish", ticketCatalogCtrl.PublishEvent)
			organizer.POST("/events/:event_id/unpublish", ticketCatalogCtrl.UnpublishEvent)
			organizer.POST("/events/:event_id/cancel", ticketCatalogCtrl.CancelEvent)
			organizer.POST("/events/:event_id/refunds", ticketCatalogCtrl.BatchRefundEvent)
			organizer.POST("/events/:event_id/sessions", ticketCatalogCtrl.CreateSession)
			organizer.POST("/sessions/:session_id/ticket-tiers", ticketCatalogCtrl.CreateTicketTier)
			organizer.POST("/ticket-tiers/:tier_id/disable", ticketCatalogCtrl.DisableTicketTier)
			organizer.POST("/rush-sales", rushSaleCtrl.CreateCampaign)
		}

		admin := v1.Group("/admin")
		admin.Use(common.AuthMiddleware(), common.AdminAuthMiddleware())
		{
			admin.POST("/organizers", ticketCatalogCtrl.AdminCreateOrganizer)
			admin.GET("/organizers", ticketCatalogCtrl.AdminListOrganizers)
		}
	}

	metrics.RegisterHandler(r)

	// 后台 goroutine
	mainCtx, cancel := context.WithCancel(context.Background())
	go ticketOrderConsumer.Start(mainCtx, cfg.OrderConsumer)
	go ticketOrderSvc.StartTimeoutScanner(mainCtx)
	go ticketOrderSvc.StartOutboxPublisher(mainCtx)
	go ticketCompensationSvc.Start(mainCtx)

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

func writeLimitMiddleware(limiter *common.IPRateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !limiter.GetLimiter(c.ClientIP()).Allow() {
			response.Error(c, http.StatusTooManyRequests, response.CodeTooManyRequests, "操作过于频繁，请稍后再试")
			c.Abort()
			return
		}
		c.Next()
	}
}
