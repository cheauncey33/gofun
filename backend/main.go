package main

import (
	"context"
	"flag"
	"fmt"
	"gofun/common"
	"gofun/config"
	"gofun/container"
	"gofun/controller"
	"gofun/metrics"
	"gofun/pkg/logger"
	"gofun/pkg/middleware"
	"gofun/pkg/response"
	apptelemetry "gofun/pkg/telemetry"
	"gofun/pkg/validator"
	"gofun/pkg/ws"
	"gofun/service"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gin-gonic/gin/otelgin"
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

	logger.Log.Info("Gofun 票务服务启动中...")
	gin.SetMode(cfg.Server.Mode)

	shutdownTelemetry, err := apptelemetry.Setup(context.Background(), cfg.Telemetry)
	if err != nil {
		panic(fmt.Errorf("初始化 OpenTelemetry 失败: %v", err))
	}

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
	ticketCatalogSvc.ConfigureInventory(cfg.Inventory)
	ticketOrderSvc := service.NewTicketOrderService(cont, timeoutMinutes, cfg.Payment)
	orderHub := ws.NewHub()
	ticketOrderSvc.ConfigureOrderEvents(orderHub)
	ticketOrderSvc.ConfigureInventory(cfg.Inventory)
	ticketVerificationSvc := service.NewTicketVerificationService(cont)
	rushSaleSvc := service.NewRushSaleService(
		cont,
		ticketCatalogSvc,
		ticketOrderSvc,
		time.Duration(cfg.RushSale.CampaignCacheTTLMS)*time.Millisecond,
	)
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
	eventCommentSvc := service.NewEventCommentService(cont)
	eventCommentCtrl := controller.NewEventCommentController(eventCommentSvc)
	orderSocketHandler := ws.NewHandler(orderHub, cfg.Cors.AllowOrigins...)
	ticketCompensationSvc := service.NewTicketCompensationService(cont)
	ticketCompensationSvc.ConfigureInventory(cfg.Inventory)
	eventSearchCompensationSvc := service.NewEventSearchCompensationService(
		ticketCatalogSvc,
		cont.RDB,
		time.Duration(cfg.Elasticsearch.SyncIntervalMinutes)*time.Minute,
		time.Duration(cfg.Elasticsearch.SyncLockTimeoutSec)*time.Second,
	)
	var writeLimit gin.HandlerFunc
	if cfg.RateLimit.DistributedWriteEnabled {
		writeLimit = common.DistributedWriteRateLimitMiddleware(
			common.NewRedisSlidingWindowLimiter(
				cont.RDB,
				time.Duration(cfg.RateLimit.WriteWindowMS)*time.Millisecond,
				cfg.RateLimit.WriteMaxPerWindow,
				cfg.RateLimit.WriteFailOpen,
			),
		)
	} else {
		writeLimiter := common.NewIPRateLimiter(
			rate.Limit(cfg.RateLimit.WriteRate),
			cfg.RateLimit.WriteBurst,
		)
		writeLimit = writeLimitMiddleware(writeLimiter)
	}

	// MySQL 是最终票额来源，启动时将票档剩余量写入 Gofun 独立 Redis 命名空间。
	if err := ticketOrderSvc.WarmTicketQuota(context.Background()); err != nil {
		logger.Log.Fatal("票档预热 Redis 失败", zap.Error(err))
	}
	if err := ticketOrderSvc.BackfillPaidAdmissionTickets(context.Background()); err != nil {
		logger.Log.Fatal("历史已支付订单补签电子票失败", zap.Error(err))
	}
	if err := ticketOrderSvc.RecoverPaymentState(context.Background()); err != nil {
		logger.Log.Fatal("支付状态恢复失败", zap.Error(err))
	}
	if err := ticketOrderSvc.SetupPaymentTimeoutInfrastructure(); err != nil {
		logger.Log.Fatal("票务支付超时延时队列初始化失败", zap.Error(err))
	}
	if err := ticketCatalogSvc.ReindexPublishedEvents(context.Background()); err != nil {
		logger.Log.Warn("活动 ES 索引重建失败（已忽略，检索可降级 MySQL）", zap.Error(err))
	}

	// 创建路由
	r := gin.Default()
	r.Use(middleware.RequestIDMiddleware())
	if cfg.Telemetry.Enabled {
		r.Use(otelgin.Middleware(cfg.Telemetry.ServiceName))
	}
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
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// 路由注册
	v1 := r.Group("/api/v1")
	{
		v1.POST("/login", userCtrl.Login)
		v1.POST("/register", userCtrl.Register)
		v1.POST("/auth/refresh", userCtrl.Refresh)
		v1.POST("/logout", userCtrl.Logout)
		v1.GET("/ws", orderSocketHandler.Handle)
		v1.POST("/payments/sandbox/callback", ticketOrderCtrl.PaymentCallback)

		// 活动浏览无需登录；购票和主办方管理仍由各自的鉴权路由保护。
		v1.GET("/events", ticketCatalogCtrl.ListPublishedEvents)
		v1.GET("/events/:id", ticketCatalogCtrl.GetPublishedEvent)
		v1.GET("/events/:id/comments", common.OptionalAuthMiddleware(), eventCommentCtrl.List)
		v1.GET("/catalog/meta", ticketCatalogCtrl.GetCatalogMeta)
		v1.GET("/rush-sales", rushSaleCtrl.ListCampaigns)

		auth := v1.Group("/")
		auth.Use(common.AuthMiddleware())
		{
			auth.POST("/orders", writeLimit, ticketOrderCtrl.CreateOrder)
			auth.GET("/orders", ticketOrderCtrl.ListOrders)
			auth.GET("/orders/:id", ticketOrderCtrl.GetOrder)
			auth.POST("/orders/:id/cancel", ticketOrderCtrl.CancelOrder)
			auth.POST("/orders/:id/pay", ticketOrderCtrl.PayOrder)
			auth.POST("/rush-sales/:id/execute", writeLimit, rushSaleCtrl.Execute)
			auth.POST("/events/:id/comments", writeLimit, eventCommentCtrl.Create)
			auth.DELETE("/comments/:id", eventCommentCtrl.Delete)
			auth.POST("/comments/:id/like", writeLimit, eventCommentCtrl.Like)

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
	go orderHub.Run(mainCtx)
	go ticketOrderConsumer.Start(mainCtx, cfg.OrderConsumer)
	go ticketOrderSvc.StartPaymentTimeoutConsumer(mainCtx, cfg.DelayedOrder.WorkerCount)
	go ticketOrderSvc.StartTimeoutScanner(mainCtx)
	go ticketOrderSvc.StartOutboxPublisher(mainCtx, cfg.OrderOutbox)
	go ticketCompensationSvc.Start(mainCtx)
	go eventSearchCompensationSvc.Start(mainCtx)
	go eventCommentSvc.StartLikeCountFlusher(mainCtx)
	go metrics.StartRuntimeCollector(
		mainCtx,
		cont.DB,
		cont.NewMQChannel,
		cont.MQQueueName,
		cont.MQRetryQueueName,
		"fuchang.order.delay",
		"fuchang.order.timeout",
		cont.MQDLQName,
	)

	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port),
		Handler: r,
	}
	var pprofSrv *http.Server
	if cfg.Pprof.Enabled {
		pprofSrv = &http.Server{
			Addr:              fmt.Sprintf("%s:%d", cfg.Pprof.Host, cfg.Pprof.Port),
			Handler:           http.DefaultServeMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			logger.Log.Info("pprof 服务启动", zap.String("addr", pprofSrv.Addr))
			if err := pprofSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Log.Error("pprof 服务异常退出", zap.Error(err))
			}
		}()
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
	if pprofSrv != nil {
		if err := pprofSrv.Shutdown(shutdownCtx); err != nil {
			logger.Log.Error("pprof Shutdown Error", zap.Error(err))
		}
	}
	traceShutdownCtx, traceShutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer traceShutdownCancel()
	if err := shutdownTelemetry(traceShutdownCtx); err != nil {
		logger.Log.Error("OpenTelemetry Shutdown Error", zap.Error(err))
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
