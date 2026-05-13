package common

import (
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/models"
	"log"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var DB *gorm.DB

func InitDB(cfg config.MySQLConfig) {
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
	if err != nil {
		panic(err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		panic(err)
	}
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	err = db.AutoMigrate(
		&models.Dormitory{},
		&models.Category{},
		&models.Order{},
		&models.OrderItem{},
		&models.User{},
		&models.Product{},
		&models.Address{},
		&models.SeckillActivity{},
		&models.SeckillOrder{},
	)
	if err != nil {
		panic(err)
	}
	// 兼容旧唯一索引：phone改为可空后需删除旧约束（忽略错误，索引可能不存在）
	_ = db.Exec("DROP INDEX uni_user_phone ON user")

	// Ensure an admin user exists
	var adminCount int64
	db.Model(&models.User{}).Where("role = ?", "admin").Count(&adminCount)
	if adminCount == 0 {
		hashed, _ := bcrypt.GenerateFromPassword([]byte("admin123"), 12)
		db.Where(models.User{Username: "admin"}).Assign(models.User{
			Password: string(hashed),
			Balance:  9999,
			Role:     "admin",
		}).FirstOrCreate(&models.User{})
		log.Println("默认管理员已创建: admin / admin123")
	}

	DB = db
	log.Println("MySQL 连接成功")
}
