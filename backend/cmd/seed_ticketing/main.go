// Gofun 票务演示目录种子：多城市 × 多分类活动。
//
// 用法（在 backend/ 目录）：
//
//	go run ./cmd/seed_ticketing -config ./config/config.yaml
//
// 幂等：按活动标题判断，已存在则跳过。
package main

import (
	"flag"
	"fmt"
	"gofun/config"
	"gofun/models"
	"log"
	"time"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type seedEvent struct {
	Title       string
	Subtitle    string
	Category    string
	CoverURL    string
	Description string
	City        string
	VenueName   string
	District    string
	Address     string
	PriceCents  int64
	DaysFromNow int
}

var demoEvents = []seedEvent{
	{
		Title: "夏夜回声 Livehouse 专场", Subtitle: "独立乐队联合巡演武汉站", Category: "音乐现场",
		CoverURL:    "https://images.unsplash.com/photo-1470229722913-7c0e2dbbafd3?w=800&q=80",
		Description: "三组独立乐队连开，适合想近距离看现场的观众。",
		City:        "武汉", VenueName: "武汉光谷青年剧场", District: "洪山区", Address: "光谷步行街旁",
		PriceCents: 18800, DaysFromNow: 10,
	},
	{
		Title: "沿江喜剧夜·新梗实验场", Subtitle: "脱口秀开放麦精选", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1527224857830-43a7acc85260?w=800&q=80",
		Description: "本地与巡演演员轮麦，60 分钟连续新段子。",
		City:        "武汉", VenueName: "汉阳造艺术区小剧场", District: "汉阳区", Address: "汉阳造创意园",
		PriceCents: 9900, DaysFromNow: 18,
	},
	{
		Title: "微光城市影像展", Subtitle: "当代摄影联展", Category: "展览",
		CoverURL:    "https://images.unsplash.com/photo-1561214115-f2f134cc4912?w=800&q=80",
		Description: "围绕城市夜色与人群关系的影像专题展。",
		City:        "武汉", VenueName: "湖北美术馆展厅 B", District: "武昌区", Address: "东湖路",
		PriceCents: 6800, DaysFromNow: 26,
	},
	{
		Title: "北漂喜剧人·周五专场", Subtitle: "单口喜剧俱乐部", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?w=800&q=80",
		Description: "五位驻场演员轮流上场，适合第一次看脱口秀。",
		City:        "北京", VenueName: "鼓楼西剧场", District: "东城区", Address: "鼓楼西大街",
		PriceCents: 12800, DaysFromNow: 7,
	},
	{
		Title: "峡谷回响音乐节", Subtitle: "户外双日营地", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1459749411175-04bf5292ceea?w=800&q=80",
		Description: "电子与独立厂牌同台，含营地通行证说明。",
		City:        "北京", VenueName: "温榆河公园草地舞台", District: "朝阳区", Address: "温榆河公园",
		PriceCents: 39900, DaysFromNow: 45,
	},
	{
		Title: "江城之声演唱会", Subtitle: "流行歌手巡演北京站", Category: "演唱会",
		CoverURL:    "https://images.unsplash.com/photo-1501281668745-f7f57925c3b4?w=800&q=80",
		Description: "体育馆级别舞台与灯光，实名制入场。",
		City:        "北京", VenueName: "国家体育场辅场", District: "朝阳区", Address: "国家体育场南路",
		PriceCents: 58000, DaysFromNow: 60,
	},
	{
		Title: "外滩夜话·即兴喜剧", Subtitle: "中英双语场", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1516450360452-9312f5e86fc7?w=800&q=80",
		Description: "即兴剧团根据观众提示现场编段。",
		City:        "上海", VenueName: "静安戏剧谷小剧场", District: "静安区", Address: "南京西路商圈",
		PriceCents: 15800, DaysFromNow: 14,
	},
	{
		Title: "海风电子音乐节", Subtitle: "海岸线日落场", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1533174072545-7a4b6ad7a6c3?w=800&q=80",
		Description: "日落开场至午夜，主舞台 + 帐篷舞台。",
		City:        "上海", VenueName: "滴水湖草地广场", District: "浦东新区", Address: "临港滴水湖",
		PriceCents: 42000, DaysFromNow: 50,
	},
	{
		Title: "星河巡回演唱会·上海", Subtitle: "流行摇滚专场", Category: "演唱会",
		CoverURL:    "https://images.unsplash.com/photo-1493225457124-a3eb161ffa5f?w=800&q=80",
		Description: "全席次可选，含内场与看台说明。",
		City:        "上海", VenueName: "梅赛德斯奔驰文化中心", District: "浦东新区", Address: "世博大道",
		PriceCents: 68000, DaysFromNow: 70,
	},
	{
		Title: "宽窄巷子喜剧局", Subtitle: "成都开放麦精选", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1585699324551-f6c309eedeca?w=800&q=80",
		Description: "本地演员 + 巡演嘉宾，偏生活观察类段子。",
		City:        "成都", VenueName: "方所小剧场", District: "锦江区", Address: "太古里",
		PriceCents: 10800, DaysFromNow: 12,
	},
	{
		Title: "青城山音乐节", Subtitle: "民谣与城市流行", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1506157786151-b8491531f063?w=800&q=80",
		Description: "两日通票，含露营须知。",
		City:        "成都", VenueName: "青城山麓露营地", District: "都江堰市", Address: "青城山景区入口旁",
		PriceCents: 36000, DaysFromNow: 55,
	},
	{
		Title: "川剧新编·折子戏夜", Subtitle: "传统与当代对照", Category: "戏剧",
		CoverURL:    "https://images.unsplash.com/photo-1503095396549-807759245b35?w=800&q=80",
		Description: "经典折子戏选段，附演前导赏。",
		City:        "成都", VenueName: "锦城艺术宫小剧场", District: "高新区", Address: "天府大道",
		PriceCents: 16800, DaysFromNow: 22,
	},
	{
		Title: "珠江夜航·民谣专场", Subtitle: "船舱限定座位", Category: "音乐现场",
		CoverURL:    "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?w=800&q=80",
		Description: "江上船舱演出，座位有限。",
		City:        "广州", VenueName: "珠江夜航号剧场舱", District: "天河区", Address: "琶醍码头",
		PriceCents: 19800, DaysFromNow: 16,
	},
	{
		Title: "湾区电音节", Subtitle: "室内双舞台", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1470225620780-dba8ba36b745?w=800&q=80",
		Description: "主舞台 + 地下舞台，适合通宵场爱好者。",
		City:        "广州", VenueName: "保利世贸博览馆", District: "海珠区", Address: "新港东路",
		PriceCents: 38000, DaysFromNow: 40,
	},
	{
		Title: "热浪巡回演唱会·广州", Subtitle: "体育馆级别制作", Category: "演唱会",
		CoverURL:    "https://images.unsplash.com/photo-1540039155733-5bb30b53aa14?w=800&q=80",
		Description: "看台与内场分区售卖，实名制。",
		City:        "广州", VenueName: "广州体育馆 1 号馆", District: "天河区", Address: "体育馆路",
		PriceCents: 52000, DaysFromNow: 65,
	},
}

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

	owner, err := ensureOwnerUser(db)
	if err != nil {
		log.Fatalf("准备种子用户失败: %v", err)
	}
	organizer, err := ensureOrganizer(db, owner.ID)
	if err != nil {
		log.Fatalf("准备主办方失败: %v", err)
	}

	created, skipped := 0, 0
	for _, item := range demoEvents {
		var existing models.Event
		err := db.Where("title = ?", item.Title).First(&existing).Error
		if err == nil {
			skipped++
			continue
		}
		if err != nil && err != gorm.ErrRecordNotFound {
			log.Fatalf("查询活动失败 (%s): %v", item.Title, err)
		}
		if err := createPublishedEvent(db, organizer.ID, item); err != nil {
			log.Fatalf("创建活动失败 (%s): %v", item.Title, err)
		}
		created++
		fmt.Printf("created: [%s] %s @ %s\n", item.Category, item.Title, item.City)
	}
	fmt.Printf("done. created=%d skipped=%d\n", created, skipped)
}

func ensureOwnerUser(db *gorm.DB) (*models.User, error) {
	var user models.User
	err := db.Where("username = ?", "admin").First(&user).Error
	if err == nil {
		return &user, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return nil, fmt.Errorf("未找到 admin 用户，请先启动后端完成自动建管理员")
}

func ensureOrganizer(db *gorm.DB, ownerUserID int64) (*models.Organizer, error) {
	const slug = "gofun-demo"
	var organizer models.Organizer
	err := db.Where("slug = ?", slug).First(&organizer).Error
	if err == nil {
		return &organizer, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	organizer = models.Organizer{
		Name:         "Gofun 演示主办方",
		Slug:         slug,
		Description:  "多城市演示目录种子数据",
		ContactName:  "演示运营",
		ContactPhone: "13800000000",
		Status:       models.OrganizerStatusActive,
		AuditStatus:  models.AuditStatusApproved,
	}
	return &organizer, db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&organizer).Error; err != nil {
			return err
		}
		member := models.OrganizerMember{
			OrganizerID: organizer.ID,
			UserID:      ownerUserID,
			Role:        models.OrganizerRoleOwner,
			Status:      models.OrganizerStatusActive,
		}
		return tx.Create(&member).Error
	})
}

func createPublishedEvent(db *gorm.DB, organizerID int64, item seedEvent) error {
	now := time.Now()
	starts := now.Add(time.Duration(item.DaysFromNow) * 24 * time.Hour).Truncate(time.Hour)
	ends := starts.Add(3 * time.Hour)
	saleStart := now.Add(-24 * time.Hour)
	saleEnd := starts.Add(-2 * time.Hour)
	published := now

	return db.Transaction(func(tx *gorm.DB) error {
		venue := models.Venue{
			OrganizerID: organizerID,
			Name:        item.VenueName,
			City:        item.City,
			District:    item.District,
			Address:     item.Address,
			Timezone:    "Asia/Shanghai",
			Status:      models.OrganizerStatusActive,
		}
		if err := tx.Create(&venue).Error; err != nil {
			return err
		}

		event := models.Event{
			OrganizerID:        organizerID,
			Title:              item.Title,
			Subtitle:           item.Subtitle,
			Category:           item.Category,
			CoverURL:           item.CoverURL,
			Description:        item.Description,
			Status:             models.EventStatusPublished,
			RealNameRequired:   item.Category == "演唱会" || item.Category == "音乐节",
			MaxTicketsPerOrder: 6,
			PublishedAt:        &published,
		}
		if err := tx.Create(&event).Error; err != nil {
			return err
		}

		session := models.EventSession{
			EventID:      event.ID,
			VenueID:      venue.ID,
			StartsAt:     starts,
			EndsAt:       ends,
			SaleStartsAt: saleStart,
			SaleEndsAt:   saleEnd,
			Status:       models.SessionStatusOnSale,
		}
		if err := tx.Create(&session).Error; err != nil {
			return err
		}

		orig := item.PriceCents + 4000
		tier := models.TicketTier{
			SessionID:          session.ID,
			Name:               "普通票",
			Description:        "一人一票",
			PriceCents:         item.PriceCents,
			OriginalPriceCents: &orig,
			TotalQuota:         200,
			RemainingQuota:     200,
			PurchaseLimit:      4,
			Status:             models.TicketTierStatusOnSale,
		}
		return tx.Create(&tier).Error
	})
}
