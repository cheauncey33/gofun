// 写入主办方漏斗近 30 天日汇总。只写 funnel_daily / funnel_order_daily，不插订单明细。
//
// 用法（在 backend/ 目录）：
//
//	go run ./cmd/seed_funnel -config ./config/config.yaml
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"gofun/config"
	"gofun/migrations"
	"gofun/models"
	"gofun/service"

	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func main() {
	configPath := flag.String("config", "./config/config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	if err := migrations.Run(db); err != nil {
		log.Fatalf("执行迁移失败: %v", err)
	}

	ctx := context.Background()
	var rdb *redis.Client
	if cfg.Redis.Addr != "" {
		client := redis.NewClient(&redis.Options{
			Addr:     cfg.Redis.Addr,
			Password: cfg.Redis.Password,
			DB:       cfg.Redis.DB,
		})
		if pingErr := client.Ping(ctx).Err(); pingErr != nil {
			log.Printf("redis 不可用，漏斗缓存不会立刻失效: %v", pingErr)
			_ = client.Close()
		} else {
			rdb = client
			defer func() { _ = rdb.Close() }()
		}
	}

	slugs := []string{"jiangcheng-live", "gofun-demo"}
	seeded := 0
	for _, slug := range slugs {
		var organizer models.Organizer
		if err := db.Where("slug = ?", slug).First(&organizer).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				continue
			}
			log.Fatalf("查询主办方 %s: %v", slug, err)
		}
		var events []models.Event
		if err := db.Select("id, organizer_id, title").
			Where("organizer_id = ? AND status = ?", organizer.ID, models.EventStatusPublished).
			Find(&events).Error; err != nil {
			log.Fatalf("查询活动 %s: %v", slug, err)
		}
		if len(events) == 0 {
			fmt.Printf("skip %s: no published events\n", slug)
			continue
		}
		items := make([]service.FunnelHistoryEvent, 0, len(events))
		for _, event := range events {
			items = append(items, service.FunnelHistoryEvent{
				ID:          event.ID,
				OrganizerID: event.OrganizerID,
				Title:       event.Title,
			})
		}
		if err := service.ReplaceOrganizerFunnelHistory(ctx, db, organizer.ID, items, time.Now()); err != nil {
			log.Fatalf("写入漏斗日汇总 %s: %v", slug, err)
		}
		service.BumpFunnelCacheVersion(ctx, rdb, organizer.ID)
		seeded++
		fmt.Printf("seeded funnel history: %s events=%d\n", slug, len(events))
	}
	if seeded == 0 {
		log.Fatal("没有可写入的主办方，请先跑 tests/load/seed_demo_workspace.mjs 或 go run ./cmd/seed_ticketing")
	}
	fmt.Printf("done. organizers=%d\n", seeded)
}
