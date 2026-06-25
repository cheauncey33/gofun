// 数据导入器：读取仓库根目录 grabData/*.json，写入 MySQL。
//
// 用法（在 backend/ 目录下执行）：
//
//	go run ./cmd/seed -config ./config/config.yaml -data ../grabData
//
// 幂等：按唯一键（分类名 / 商品名 / 用户名）判断是否已存在，已存在则跳过，
// 可重复执行。导入商品后请重启后端，main.go 启动时会把库存预热到 Redis。
package main

import (
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/models"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/bwmarrin/snowflake"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type categorySeed struct {
	Name string `json:"name"`
	Sort int    `json:"sort"`
}

type productSeed struct {
	Name        string  `json:"name"`
	Category    string  `json:"category"`
	Price       float64 `json:"price"`
	Stock       int     `json:"stock"`
	SalesCount  int64   `json:"sales_count"`
	Description string  `json:"description"`
	ImageURL    string  `json:"image_url"`
}

type addressSeed struct {
	ReceiverName string `json:"receiver_name"`
	Phone        string `json:"phone"`
	Province     string `json:"province"`
	City         string `json:"city"`
	District     string `json:"district"`
	Detail       string `json:"detail"`
	IsDefault    bool   `json:"is_default"`
}

type userSeed struct {
	Username     string      `json:"username"`
	Password     string      `json:"password"`
	RealName     string      `json:"real_name"`
	Phone        string      `json:"phone"`
	AvatarURL    string      `json:"avatar_url"`
	Balance      float64     `json:"balance"`
	DormBuilding string      `json:"dorm_building"`
	DormRoom     string      `json:"dorm_room"`
	Address      addressSeed `json:"address"`
}

func main() {
	configPath := flag.String("config", "./config/config.yaml", "配置文件路径")
	dataDir := flag.String("data", "../grabData", "grabData 数据目录")
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

	node, err := snowflake.NewNode(1)
	if err != nil {
		log.Fatalf("创建雪花节点失败: %v", err)
	}

	catMap := seedCategories(db, node, *dataDir)
	seedProducts(db, node, *dataDir, catMap)
	seedUsers(db, node, *dataDir)

	log.Println("✅ 数据导入完成")
}

func mustRead(dir, name string, out any) {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		log.Fatalf("读取 %s 失败: %v", name, err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		log.Fatalf("解析 %s 失败: %v", name, err)
	}
}

func seedCategories(db *gorm.DB, node *snowflake.Node, dir string) map[string]int64 {
	var seeds []categorySeed
	mustRead(dir, "categories.json", &seeds)

	result := make(map[string]int64, len(seeds))
	created := 0
	for _, s := range seeds {
		var existing models.Category
		if err := db.Where("name = ?", s.Name).First(&existing).Error; err == nil {
			result[s.Name] = existing.ID
			continue
		}
		c := models.Category{Base: models.Base{ID: node.Generate().Int64()}, Name: s.Name, Sort: s.Sort}
		if err := db.Create(&c).Error; err != nil {
			log.Fatalf("创建分类 %s 失败: %v", s.Name, err)
		}
		result[s.Name] = c.ID
		created++
	}
	log.Printf("分类：新增 %d，共 %d", created, len(seeds))
	return result
}

func seedProducts(db *gorm.DB, node *snowflake.Node, dir string, catMap map[string]int64) {
	var seeds []productSeed
	mustRead(dir, "products.json", &seeds)

	created := 0
	for _, s := range seeds {
		var count int64
		db.Model(&models.Product{}).Where("name = ?", s.Name).Count(&count)
		if count > 0 {
			continue
		}
		catID, ok := catMap[s.Category]
		if !ok {
			log.Fatalf("商品 %s 的分类 %s 不存在", s.Name, s.Category)
		}
		cid := catID
		p := models.Product{
			Base:        models.Base{ID: node.Generate().Int64()},
			Name:        s.Name,
			Description: s.Description,
			Price:       s.Price,
			Stock:       s.Stock,
			ImageURL:    s.ImageURL,
			CategoryID:  &cid,
			Status:      models.ProductStatusOnSale,
			SalesCount:  s.SalesCount,
		}
		if err := db.Create(&p).Error; err != nil {
			log.Fatalf("创建商品 %s 失败: %v", s.Name, err)
		}
		created++
	}
	log.Printf("商品：新增 %d，共 %d", created, len(seeds))
}

func seedUsers(db *gorm.DB, node *snowflake.Node, dir string) {
	var seeds []userSeed
	mustRead(dir, "users.json", &seeds)

	created := 0
	for _, s := range seeds {
		var count int64
		db.Model(&models.User{}).Where("username = ?", s.Username).Count(&count)
		if count > 0 {
			continue
		}

		// 宿舍
		dorm := models.Dormitory{Base: models.Base{ID: node.Generate().Int64()}, BuildingName: s.DormBuilding, RoomNumber: s.DormRoom}
		if err := db.Create(&dorm).Error; err != nil {
			log.Fatalf("创建宿舍失败: %v", err)
		}

		hash, err := bcrypt.GenerateFromPassword([]byte(s.Password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("密码加密失败: %v", err)
		}
		phone := s.Phone
		u := models.User{
			Base:      models.Base{ID: node.Generate().Int64()},
			Username:  s.Username,
			Password:  string(hash),
			Balance:   s.Balance,
			Phone:     &phone,
			AvatarURL: s.AvatarURL,
			DormID:    dorm.ID,
			Role:      "user",
		}
		if err := db.Create(&u).Error; err != nil {
			log.Fatalf("创建用户 %s 失败: %v", s.Username, err)
		}

		addr := models.Address{
			Base:         models.Base{ID: node.Generate().Int64()},
			UserID:       u.ID,
			ReceiverName: s.Address.ReceiverName,
			Phone:        s.Address.Phone,
			Province:     s.Address.Province,
			City:         s.Address.City,
			District:     s.Address.District,
			Detail:       s.Address.Detail,
			IsDefault:    s.Address.IsDefault,
		}
		if err := db.Create(&addr).Error; err != nil {
			log.Fatalf("创建地址失败: %v", err)
		}
		created++
	}
	log.Printf("用户：新增 %d，共 %d（默认密码 123456）", created, len(seeds))
}
