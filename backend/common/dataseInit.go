package common

import (
	"WHU_Snack_GO/models"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

var DB *gorm.DB

func InitDB() {
	dsn := "root:root@tcp(127.0.0.1:3306)/whu_snack_go?charset=utf8mb4&parseTime=True&loc=Local"
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
	if err != nil {
		panic(err)
	}
	err = db.AutoMigrate(
		&models.Dormitory{},
		&models.Order{},
		&models.OrderItem{},
		&models.User{},
		&models.Product{},
	)
	if err != nil {
		panic(err)
	}
	DB = db
}
