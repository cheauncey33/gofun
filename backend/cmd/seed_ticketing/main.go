// Gofun 票务演示目录种子：多城市 × 多分类活动。
//
// 用法（在 backend/ 目录）：
//
//	go run ./cmd/seed_ticketing -config ./config/config.yaml
//
// 幂等：按活动标题判断，已存在则跳过。
// 脱口秀按产品约定走 seated（必须选座）；若旧种子仍是 counter，会补厅图并改卖法。
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
		Description: "三组武汉本地独立乐队连开，中间不歇太久。场地能装大概两百人，前排很容易被贝斯糊掉，建议耳塞。开场前一小时门口有少量周边。",
		City:        "武汉", VenueName: "武汉光谷青年剧场", District: "洪山区", Address: "光谷步行街旁",
		PriceCents: 18800, DaysFromNow: 10,
	},
	{
		Title: "沿江喜剧夜·新梗实验场", Subtitle: "脱口秀开放麦精选", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1527224857830-43a7acc85260?w=800&q=80",
		Description: "开放麦精选夜，六到八位演员轮流上。段子以武汉生活和新梗实验为主，偶尔会点观众互动。不适合带小朋友。",
		City:        "武汉", VenueName: "汉阳造艺术区小剧场", District: "汉阳区", Address: "汉阳造创意园",
		PriceCents: 9900, DaysFromNow: 18,
	},
	{
		Title: "微光城市影像展", Subtitle: "当代摄影联展", Category: "展览",
		CoverURL:    "https://images.unsplash.com/photo-1561214115-f2f134cc4912?w=800&q=80",
		Description: "围绕夜色、江面和人群距离的当代摄影联展。票是当日入场一次，周末人会多一些，拍照请关闪光灯。",
		City:        "武汉", VenueName: "湖北美术馆展厅 B", District: "武昌区", Address: "东湖路",
		PriceCents: 6800, DaysFromNow: 26,
	},
	{
		Title: "北漂喜剧人·周五专场", Subtitle: "单口喜剧俱乐部", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?w=800&q=80",
		Description: "五位驻场单口轮流上场，节奏偏快。第一次看脱口秀也可以，前排更容易被点到。",
		City:        "北京", VenueName: "鼓楼西剧场", District: "东城区", Address: "鼓楼西大街",
		PriceCents: 12800, DaysFromNow: 7,
	},
	{
		Title: "峡谷回响音乐节", Subtitle: "户外双日营地", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1459749411175-04bf5292ceea?w=800&q=80",
		Description: "户外双日营地，电子厂牌和独立乐队同台。通票含营地通行证。请自备防晒、雨衣，现场安检不让带玻璃瓶。",
		City:        "北京", VenueName: "温榆河公园草地舞台", District: "朝阳区", Address: "温榆河公园",
		PriceCents: 39900, DaysFromNow: 45,
	},
	{
		Title: "江城之声演唱会", Subtitle: "流行歌手巡演北京站", Category: "演唱会",
		CoverURL:    "https://images.unsplash.com/photo-1501281668745-f7f57925c3b4?w=800&q=80",
		Description: "体育馆级别流行巡演北京站，实名制入场，请提前绑定观演人。开场后迟到从侧门进。",
		City:        "北京", VenueName: "国家体育场辅场", District: "朝阳区", Address: "国家体育场南路",
		PriceCents: 58000, DaysFromNow: 60,
	},
	{
		Title: "外滩夜话·即兴喜剧", Subtitle: "中英双语场", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1516450360452-9312f5e86fc7?w=800&q=80",
		Description: "中英双语即兴场，演员会根据观众提示现场编段。座位少，建议提前到。",
		City:        "上海", VenueName: "静安戏剧谷小剧场", District: "静安区", Address: "南京西路商圈",
		PriceCents: 15800, DaysFromNow: 14,
	},
	{
		Title: "海风电子音乐节", Subtitle: "海岸线日落场", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1533174072545-7a4b6ad7a6c3?w=800&q=80",
		Description: "滴水湖日落开场到午夜，主舞台加帐篷舞台。海风比较硬，晚上降温快。",
		City:        "上海", VenueName: "滴水湖草地广场", District: "浦东新区", Address: "临港滴水湖",
		PriceCents: 42000, DaysFromNow: 50,
	},
	{
		Title: "星河巡回演唱会·上海", Subtitle: "流行摇滚专场", Category: "演唱会",
		CoverURL:    "https://images.unsplash.com/photo-1493225457124-a3eb161ffa5f?w=800&q=80",
		Description: "流行摇滚专场，梅赛德斯奔驰文化中心全席次。看台视野稳定，内场前排会更吵。实名制一人一证。",
		City:        "上海", VenueName: "梅赛德斯奔驰文化中心", District: "浦东新区", Address: "世博大道",
		PriceCents: 68000, DaysFromNow: 70,
	},
	{
		Title: "宽窄巷子喜剧局", Subtitle: "成都开放麦精选", Category: "脱口秀",
		CoverURL:    "https://images.unsplash.com/photo-1585699324551-f6c309eedeca?w=800&q=80",
		Description: "成都开放麦精选，本地演员加巡演嘉宾，偏生活观察。小剧场没有栏杆，迟到可能要站后排。",
		City:        "成都", VenueName: "方所小剧场", District: "锦江区", Address: "太古里",
		PriceCents: 10800, DaysFromNow: 12,
	},
	{
		Title: "青城山音乐节", Subtitle: "民谣与城市流行", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1506157786151-b8491531f063?w=800&q=80",
		Description: "两日通票，民谣和城市流行为主。景区门口到营地要走一段，晚上有禁篝火规定。",
		City:        "成都", VenueName: "青城山麓露营地", District: "都江堰市", Address: "青城山景区入口旁",
		PriceCents: 36000, DaysFromNow: 55,
	},
	{
		Title: "川剧新编·折子戏夜", Subtitle: "传统与当代对照", Category: "戏剧",
		CoverURL:    "https://images.unsplash.com/photo-1503095396549-807759245b35?w=800&q=80",
		Description: "经典折子戏选段，开演前有十五分钟导赏。小剧场禁止饮食，中途不建议进出。",
		City:        "成都", VenueName: "锦城艺术宫小剧场", District: "高新区", Address: "天府大道",
		PriceCents: 16800, DaysFromNow: 22,
	},
	{
		Title: "珠江夜航·民谣专场", Subtitle: "船舱限定座位", Category: "音乐现场",
		CoverURL:    "https://images.unsplash.com/photo-1514525253161-7a46d19cd819?w=800&q=80",
		Description: "江上船舱演出，座位有限，售罄后转候补。船体轻微晃动，开船后不再检票上船。",
		City:        "广州", VenueName: "珠江夜航号剧场舱", District: "天河区", Address: "琶醍码头",
		PriceCents: 19800, DaysFromNow: 16,
	},
	{
		Title: "湾区电音节", Subtitle: "室内双舞台", Category: "音乐节",
		CoverURL:    "https://images.unsplash.com/photo-1470225620780-dba8ba36b745?w=800&q=80",
		Description: "室内双舞台通宵场，主舞台偏流行电音，地下舞台更硬。耳塞几乎是必需品。",
		City:        "广州", VenueName: "保利世贸博览馆", District: "海珠区", Address: "新港东路",
		PriceCents: 38000, DaysFromNow: 40,
	},
	{
		Title: "热浪巡回演唱会·广州", Subtitle: "体育馆级别制作", Category: "演唱会",
		CoverURL:    "https://images.unsplash.com/photo-1540039155733-5bb30b53aa14?w=800&q=80",
		Description: "体育馆级别制作，看台和内场分区，实名制。开售期间会有一波早鸟内场，数量少。",
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

	created, converted, skipped := 0, 0, 0
	for _, item := range demoEvents {
		var existing models.Event
		err := db.Where("title = ?", item.Title).First(&existing).Error
		if err == nil {
			if isSeatedCategory(item.Category) && !existing.SaleMode.IsSeated() {
				if err := convertPublishedToSeated(db, &existing); err != nil {
					log.Fatalf("补选座失败 (%s): %v", item.Title, err)
				}
				converted++
				fmt.Printf("converted to seated: [%s] %s @ %s\n", item.Category, item.Title, item.City)
				continue
			}
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
	fmt.Printf("done. created=%d converted=%d skipped=%d\n", created, converted, skipped)
}

func isSeatedCategory(category string) bool {
	return category == "脱口秀" || category == "电影"
}

func ensureOwnerUser(db *gorm.DB) (*models.User, error) {
	var user models.User
	err := db.Where("username = ?", "organizer").First(&user).Error
	if err == nil {
		return &user, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	err = db.Where("username = ?", "admin").First(&user).Error
	if err == nil {
		return &user, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	return nil, fmt.Errorf("未找到 organizer 或 admin 用户，请先启动后端完成演示账号初始化")
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
	saleMode := models.EventSaleModeCounter
	if isSeatedCategory(item.Category) {
		saleMode = models.EventSaleModeSeated
	}

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
			SaleMode:           saleMode,
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

		if saleMode.IsSeated() {
			vipOrig := item.PriceCents + 12000
			frontOrig := item.PriceCents + 7000
			regularOrig := item.PriceCents + 4000
			vip := models.TicketTier{
				SessionID:          session.ID,
				Name:               "VIP",
				Description:        "第一排，视野最好",
				PriceCents:         item.PriceCents + 8000,
				OriginalPriceCents: &vipOrig,
				PurchaseLimit:      2,
				AssignPlaceNo:      false,
				Status:             models.TicketTierStatusOnSale,
			}
			front := models.TicketTier{
				SessionID:          session.ID,
				Name:               "前排",
				Description:        "靠近舞台的第二排",
				PriceCents:         item.PriceCents + 3000,
				OriginalPriceCents: &frontOrig,
				PurchaseLimit:      4,
				AssignPlaceNo:      false,
				Status:             models.TicketTierStatusOnSale,
			}
			regular := models.TicketTier{
				SessionID:          session.ID,
				Name:               "普通座",
				Description:        "小剧场选座",
				PriceCents:         item.PriceCents,
				OriginalPriceCents: &regularOrig,
				PurchaseLimit:      4,
				AssignPlaceNo:      false,
				Status:             models.TicketTierStatusOnSale,
			}
			if err := tx.Create(&vip).Error; err != nil {
				return err
			}
			if err := tx.Create(&front).Error; err != nil {
				return err
			}
			if err := tx.Create(&regular).Error; err != nil {
				return err
			}
			return attachSmallTheaterLayout(tx, event.ID, session.ID, vip.ID, front.ID, regular.ID)
		}

		orig := item.PriceCents + 4000
		assignPlaceNo := item.Category != "展览"
		tier := models.TicketTier{
			SessionID:          session.ID,
			Name:               "普通票",
			Description:        "一人一票",
			PriceCents:         item.PriceCents,
			OriginalPriceCents: &orig,
			TotalQuota:         200,
			RemainingQuota:     200,
			PurchaseLimit:      4,
			AssignPlaceNo:      assignPlaceNo,
			Status:             models.TicketTierStatusOnSale,
		}
		return tx.Create(&tier).Error
	})
}

func convertPublishedToSeated(db *gorm.DB, event *models.Event) error {
	var layoutCount int64
	if err := db.Model(&models.SeatLayout{}).Where("event_id = ?", event.ID).Count(&layoutCount).Error; err != nil {
		return err
	}
	if layoutCount > 0 {
		return db.Model(event).Update("sale_mode", models.EventSaleModeSeated).Error
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(event).Update("sale_mode", models.EventSaleModeSeated).Error; err != nil {
			return err
		}
		var session models.EventSession
		if err := tx.Where("event_id = ?", event.ID).First(&session).Error; err != nil {
			return err
		}
		var tiers []models.TicketTier
		if err := tx.Where("session_id = ?", session.ID).Order("id ASC").Find(&tiers).Error; err != nil {
			return err
		}
		if len(tiers) == 0 {
			return fmt.Errorf("活动没有票档")
		}
		regular := tiers[0]
		if err := tx.Model(&regular).Updates(map[string]interface{}{
			"name":            "普通座",
			"description":     "小剧场选座",
			"assign_place_no": false,
		}).Error; err != nil {
			return err
		}
		frontOrig := regular.PriceCents + 7000
		vipOrig := regular.PriceCents + 12000
		front := models.TicketTier{
			SessionID:          session.ID,
			Name:               "前排",
			Description:        "靠近舞台的第二排",
			PriceCents:         regular.PriceCents + 3000,
			OriginalPriceCents: &frontOrig,
			PurchaseLimit:      4,
			AssignPlaceNo:      false,
			Status:             models.TicketTierStatusOnSale,
		}
		vip := models.TicketTier{
			SessionID:          session.ID,
			Name:               "VIP",
			Description:        "第一排，视野最好",
			PriceCents:         regular.PriceCents + 8000,
			OriginalPriceCents: &vipOrig,
			PurchaseLimit:      2,
			AssignPlaceNo:      false,
			Status:             models.TicketTierStatusOnSale,
		}
		if err := tx.Create(&front).Error; err != nil {
			return err
		}
		if err := tx.Create(&vip).Error; err != nil {
			return err
		}
		return attachSmallTheaterLayout(tx, event.ID, session.ID, vip.ID, front.ID, regular.ID)
	})
}

const (
	theaterRows = 6
	theaterCols = 10
	aisleCol    = 6
	vipRows     = 1
	frontRows   = 2
)

func layoutTierForRow(row int, vipTierID, frontTierID, regularTierID int64) int64 {
	if row <= vipRows {
		return vipTierID
	}
	if row <= frontRows {
		return frontTierID
	}
	return regularTierID
}

func attachSmallTheaterLayout(tx *gorm.DB, eventID, sessionID, vipTierID, frontTierID, regularTierID int64) error {
	layout := models.SeatLayout{
		EventID:  &eventID,
		Name:     "小剧场",
		RowCount: theaterRows,
		ColCount: theaterCols,
	}
	if err := tx.Create(&layout).Error; err != nil {
		return err
	}
	seats := make([]models.Seat, 0, theaterRows*(theaterCols-1))
	for row := 1; row <= theaterRows; row++ {
		for col := 1; col <= theaterCols; col++ {
			if col == aisleCol {
				continue
			}
			tierID := layoutTierForRow(row, vipTierID, frontTierID, regularTierID)
			seatTierID := tierID
			seats = append(seats, models.Seat{
				LayoutID:     layout.ID,
				TicketTierID: &seatTierID,
				RowNo:        row,
				ColNo:        col,
				Label:        fmt.Sprintf("%c%d", 'A'+row-1, col),
			})
		}
	}
	if err := tx.Create(&seats).Error; err != nil {
		return err
	}
	sessionSeats := make([]models.SessionSeat, 0, len(seats))
	quota := map[int64]int{}
	for _, seat := range seats {
		if seat.TicketTierID == nil {
			continue
		}
		quota[*seat.TicketTierID]++
		sessionSeats = append(sessionSeats, models.SessionSeat{
			SessionID:    sessionID,
			SeatID:       seat.ID,
			TicketTierID: *seat.TicketTierID,
			Status:       models.SessionSeatAvailable,
		})
	}
	if err := tx.Create(&sessionSeats).Error; err != nil {
		return err
	}
	for tierID, n := range quota {
		if err := tx.Model(&models.TicketTier{}).Where("id = ?", tierID).Updates(map[string]interface{}{
			"total_quota":     n,
			"remaining_quota": n,
			"sold_count":      0,
			"assign_place_no": false,
			"status":          models.TicketTierStatusOnSale,
		}).Error; err != nil {
			return err
		}
	}
	return nil
}
