package main

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/controller"
	"WHU_Snack_GO/service"
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()
	common.InitDB()
	common.InitRedis()
	common.InitRabbitMQ()
	common.InitLocalCache()
	// common.SeedData()
	err := service.InitProductStockToRedis()
	if err != nil {
		panic("商品预热redis失败")
	}

	// 添加全局限流：系统总计每秒处理1000个请求，允许突发1200
	r.Use(common.GlobalRateLimitMiddleware(1000, 1200))
	// 添加IP限流：单个IP每秒10个请求，允许突发20
	r.Use(common.IPRateLimitMiddleware(10, 20))

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
			auth.GET("/products", controller.GetProductHandler)

		}
	}

	//定义了ctx cancel是一个函数可以任意命名
	mainCtx, cancel := context.WithCancel(context.Background())
	//启动协程 通过select一直不间断处理不为空的msgs，除非msgs被关闭或者mainCtx发来一个Done信号
	go service.StartOrderConsumer(mainCtx)
	srv := &http.Server{
		Addr:    "127.0.0.1:8080",
		Handler: r,
	}
	//开启一个协程将web gin的r实例（通过use、group等等关联了中间件、后端的查看商品、下单等等服务）运行在srv指向的套接字下
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen:%s\n", err)
		}
	}()

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-mainCtx.Done():
				log.Printf("定时检测redis少卖程序退出")
				return
			case <-ticker.C:
				service.RunStockCompensation()
			}
		}
	}()

	//由于协程的平级地位，所以这两个go func执行完开启后就继续下面的代码：

	//这里持续监听了quit信号，一直阻塞在这里
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Println("收到关机信号，正在收尾")

	//监听到了quit信号，开始处理收尾工作
	//cancel是mainCtx, cancel := context.WithCancel(context.Background())定义的，所以cancel的隐式参数就是绑定mainCtx的，cancel（）
	//函数会mainCtx发出Done信号，此时在后台跑着的 StartOrderConsumer 函数里的 case <-ctx.Done() 会立刻感应到。
	cancel()
	time.Sleep(time.Second * 2)

	//此时后端的mysql、redis服务停止了。下面是http请求的收尾工作
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatal("Sever Shutdown Error:", err)
	}
	log.Println("Program exit success")
}
