// 写入本地演示账号：admin / organizer / user。已存在的账号不会改密码。
//
//	go run ./cmd/seed_demo_accounts -config ./config/config.yaml
package main

import (
	"flag"
	"log"

	"gofun/config"
	"gofun/container"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func main() {
	configPath := flag.String("config", "./config/config.yaml", "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("读取配置失败: %v", err)
	}
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{SingularTable: true},
	})
	if err != nil {
		log.Fatalf("连接数据库失败: %v", err)
	}
	container.EnsureDemoAccounts(db)
	log.Println("演示账号已就绪：admin / organizer / user")
}
