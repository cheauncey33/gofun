// 给已有演示目录补「活着」的数据：购票用户、讨论区、限时开售、近 7 天订单、一场候补。
//
//	go run ./cmd/seed_demo_life -config ./config/config.yaml
//
// 幂等：按用户名 / 开售名称 / 订单幂等键跳过已写入的行。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"gofun/config"
	"gofun/models"
	"gofun/service"

	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

type demoUser struct {
	Username string
}

type rushSpec struct {
	EventTitle string
	Name       string
	PriceCents int64
	Quota      int
	Sold       int
	PerUser    int
	StartOffset time.Duration
	EndOffset   time.Duration
	Status      models.RushSaleStatus
}

type commentSpec struct {
	EventTitle string
	Author     string
	HoursAgo   int
	Likes      int64
	Content    string
}

var idSeq int64

func nextID() int64 {
	return time.Now().UnixNano() + atomic.AddInt64(&idSeq, 1)
}

var fanUsers = []demoUser{
	{Username: "林晓雨"},
	{Username: "老王看演出"},
	{Username: "江城夜猫"},
	{Username: "北漂阿凯"},
	{Username: "外滩散步"},
	{Username: "成都夜宵"},
	{Username: "广州热浪粉"},
	{Username: "票根收藏家"},
	{Username: "第一次看live"},
	{Username: "周末出门"},
	{Username: "音乐节钉子户"},
	{Username: "喜剧俱乐部"},
}

var eventDescriptions = map[string]string{
	"夏夜回声 Livehouse 专场": "三组武汉本地独立乐队连开，中间不歇太久。场地能装大概两百人，前排很容易被贝斯糊掉，建议耳塞。开场前一小时门口有少量周边，酒水可以带密封瓶。",
	"沿江喜剧夜·新梗实验场":     "开放麦精选夜，六到八位演员轮流上。段子以武汉生活和新梗实验为主，偶尔会点观众互动。不适合带小朋友，中途可以出去透气再进来。",
	"微光城市影像展":         "围绕夜色、江面和人群距离的当代摄影联展，展期约三周。票是当日入场一次，周末人会多一些。展厅有中英说明，拍照请关闪光灯。",
	"北漂喜剧人·周五专场":      "五位驻场单口轮流上场，节奏偏快。第一次看脱口秀也可以，前排更容易被点到。演出后半段会留十分钟开放提问。",
	"峡谷回响音乐节":         "户外双日营地，电子厂牌和独立乐队同台。通票含营地通行证，帐篷区晚上会控音量。请自备防晒、雨衣和充电宝，现场安检不让带玻璃瓶。",
	"江城之声演唱会":         "体育馆级别流行巡演北京站，实名制入场，请提前绑定观演人。内场和看台分区售卖，开场后迟到从侧门进。禁止携带自拍杆和专业相机。",
	"外滩夜话·即兴喜剧":       "中英双语即兴场，演员会根据观众提示现场编段。座位少，建议提前到。英文段子有中文字幕卡，不一定每句都译。",
	"海风电子音乐节":         "滴水湖日落开场到午夜，主舞台加帐篷舞台。海风比较硬，晚上降温快。现场有饮水补给，回市区末班地铁请自己卡时间。",
	"星河巡回演唱会·上海":      "流行摇滚专场，梅赛德斯奔驰文化中心全席次。看台视野稳定，内场前排会更吵。实名制，一人一证，电子票当天核销入场。",
	"宽窄巷子喜剧局":         "成都开放麦精选，本地演员加巡演嘉宾，偏生活观察。小剧场没有栏杆，迟到可能要站后排。可以自带无酒精饮料。",
	"青城山音乐节":          "两日通票，民谣和城市流行为主，含露营须知。景区门口到营地要走一段，箱子不方便。晚上有禁篝火规定，请看主办方当日公告。",
	"川剧新编·折子戏夜":       "经典折子戏选段，开演前有十五分钟导赏。小剧场禁止饮食，演出中途不建议进出。适合想第一次接触川剧的观众。",
	"珠江夜航·民谣专场":       "江上船舱演出，座位有限，售罄后转候补。船体轻微晃动，晕船请提前准备。开船后不再检票上船，请预留安检时间。",
	"湾区电音节":           "室内双舞台通宵场，主舞台偏流行电音，地下舞台更硬。耳塞几乎是必需品。现场有储物柜，数量有限先到先得。",
	"热浪巡回演唱会·广州":      "体育馆级别制作，看台和内场分区，实名制。开售期间会有一波早鸟内场，数量少。请提前下载电子票，门口信号不稳定。",
}

var comments = []commentSpec{
	{EventTitle: "热浪巡回演唱会·广州", Author: "广州热浪粉", HoursAgo: 2, Likes: 18, Content: "内场早鸟开了吗？刚才首页闪过限时开售，手慢了两张都没抢到。"},
	{EventTitle: "热浪巡回演唱会·广州", Author: "票根收藏家", HoursAgo: 5, Likes: 9, Content: "看台视线其实够用，不一定非要内场。去年同馆看台C区录音也干净。"},
	{EventTitle: "热浪巡回演唱会·广州", Author: "林晓雨", HoursAgo: 14, Likes: 6, Content: "实名制要提前绑定证件，现场改观演人过不了。第一次去的记得先在个人中心加观演人。"},
	{EventTitle: "热浪巡回演唱会·广州", Author: "周末出门", HoursAgo: 30, Likes: 3, Content: "有人拼车从深圳过去吗？演出结束地铁会挤到爆炸。"},
	{EventTitle: "夏夜回声 Livehouse 专场", Author: "江城夜猫", HoursAgo: 3, Likes: 12, Content: "光谷这场位置很小，想靠前建议早点到。二组贝斯手会把人往前推。"},
	{EventTitle: "夏夜回声 Livehouse 专场", Author: "第一次看live", HoursAgo: 8, Likes: 7, Content: "第一次看 livehouse，要带耳塞吗？门口酒水能带进去不？"},
	{EventTitle: "夏夜回声 Livehouse 专场", Author: "老王看演出", HoursAgo: 20, Likes: 15, Content: "能带密封瓶。耳塞建议带，第三组鼓会很近。周边只有开场前一小会儿。"},
	{EventTitle: "夏夜回声 Livehouse 专场", Author: "林晓雨", HoursAgo: 40, Likes: 4, Content: "有人出一张普通票吗？同事突然来不了。"},
	{EventTitle: "湾区电音节", Author: "音乐节钉子户", HoursAgo: 4, Likes: 11, Content: "地下舞台更值得待着，主舞台人太多。储物柜下午四点就没了。"},
	{EventTitle: "湾区电音节", Author: "广州热浪粉", HoursAgo: 16, Likes: 5, Content: "通宵场记得带一件薄外套，空调会突然开很大。"},
	{EventTitle: "湾区电音节", Author: "周末出门", HoursAgo: 28, Likes: 2, Content: "限时票比原价便宜一百多，就是每人限一张。"},
	{EventTitle: "星河巡回演唱会·上海", Author: "外滩散步", HoursAgo: 6, Likes: 8, Content: "三小时后那波内场特惠蹲一下，官网倒计时比代购靠谱。"},
	{EventTitle: "星河巡回演唱会·上海", Author: "票根收藏家", HoursAgo: 22, Likes: 4, Content: "梅奔看台后排也听得见，别被内场价吓到。"},
	{EventTitle: "北漂喜剧人·周五专场", Author: "北漂阿凯", HoursAgo: 1, Likes: 21, Content: "前排真的会被点到，社恐建议普通座中间。周五这场基本座无虚席。"},
	{EventTitle: "北漂喜剧人·周五专场", Author: "喜剧俱乐部", HoursAgo: 9, Likes: 10, Content: "驻场五个人水平稳，新来的开场那位上周在鼓楼西也演过。"},
	{EventTitle: "北漂喜剧人·周五专场", Author: "林晓雨", HoursAgo: 26, Likes: 3, Content: "选座页面前两排贵三十，值不值看你怕不怕被点名。"},
	{EventTitle: "宽窄巷子喜剧局", Author: "成都夜宵", HoursAgo: 7, Likes: 8, Content: "太古里下场容易迟到，建议演出前四十分钟到。后排站着也能听清。"},
	{EventTitle: "宽窄巷子喜剧局", Author: "喜剧俱乐部", HoursAgo: 18, Likes: 5, Content: "本地演员那段搬家的特别准，外地朋友也能笑。"},
	{EventTitle: "珠江夜航·民谣专场", Author: "广州热浪粉", HoursAgo: 2, Likes: 16, Content: "票没了，已经去候补了。有出票的优先转我，别加价。"},
	{EventTitle: "珠江夜航·民谣专场", Author: "江城夜猫", HoursAgo: 11, Likes: 6, Content: "船舱座位真的少，上次也是候补放出来两张。晕船药备着。"},
	{EventTitle: "外滩夜话·即兴喜剧", Author: "外滩散步", HoursAgo: 12, Likes: 7, Content: "双语场比想象中好懂，提示词用中文也行。座位少，别踩点。"},
	{EventTitle: "青城山音乐节", Author: "音乐节钉子户", HoursAgo: 15, Likes: 9, Content: "营地 rec 一下：箱子别拖，从景区口走进去要二十分钟。晚上不能生火。"},
	{EventTitle: "江城之声演唱会", Author: "北漂阿凯", HoursAgo: 19, Likes: 4, Content: "辅场安检慢，建议开场前一小时到。实名制改不了观演人。"},
	{EventTitle: "川剧新编·折子戏夜", Author: "成都夜宵", HoursAgo: 33, Likes: 6, Content: "导赏值得听，完全没接触过川剧也能跟上。中途出去可能回不来原位。"},
	{EventTitle: "微光城市影像展", Author: "林晓雨", HoursAgo: 27, Likes: 2, Content: "周末下午人有点多，工作日去更舒服。B 展厅空调足。"},
	{EventTitle: "海风电子音乐节", Author: "音乐节钉子户", HoursAgo: 36, Likes: 5, Content: "海边风大，支架音响听着爽，晚上记得加衣服。回市区别卡末班。"},
	{EventTitle: "峡谷回响音乐节", Author: "老王看演出", HoursAgo: 44, Likes: 3, Content: "双日票如果只去一天有点亏，阵容第二天更密。玻璃瓶安检会收。"},
	{EventTitle: "沿江喜剧夜·新梗实验场", Author: "江城夜猫", HoursAgo: 10, Likes: 8, Content: "新梗实验顾名思义，有的段会当场改。选座前排互动多。"},
}

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

	ctx := context.Background()
	rdb := openRedis(ctx, cfg)
	if rdb != nil {
		defer rdb.Close()
	}
	settings := service.NewInventoryBucketSettings(cfg.Inventory)

	var organizer models.Organizer
	if err := db.Where("slug = ?", "gofun-demo").First(&organizer).Error; err != nil {
		log.Fatalf("未找到 gofun-demo，请先跑 seed_ticketing: %v", err)
	}

	users, err := ensureFans(db)
	if err != nil {
		log.Fatalf("准备购票用户失败: %v", err)
	}
	if err := enrichDescriptions(db, organizer.ID); err != nil {
		log.Fatalf("补活动介绍失败: %v", err)
	}
	commentN, err := seedComments(db, rdb, organizer.ID, users)
	if err != nil {
		log.Fatalf("写评论失败: %v", err)
	}
	rushN, err := seedRushSales(ctx, db, rdb, settings, organizer.ID)
	if err != nil {
		log.Fatalf("写限时开售失败: %v", err)
	}
	orderN, ticketN, err := seedRecentOrders(db, users, organizer.ID)
	if err != nil {
		log.Fatalf("写订单失败: %v", err)
	}
	if err := occupyPopularTiers(ctx, db, rdb, settings, organizer.ID); err != nil {
		log.Fatalf("占用库存失败: %v", err)
	}
	waitN, err := seedWaitlist(ctx, db, rdb, settings, users, organizer.ID)
	if err != nil {
		log.Fatalf("写候补失败: %v", err)
	}
	bumpCatalogCache(ctx, rdb, organizer.ID, db)

	fmt.Printf("done. users=%d comments=%d rush=%d orders=%d tickets=%d waitlist=%d\n",
		len(users), commentN, rushN, orderN, ticketN, waitN)
}

func openRedis(ctx context.Context, cfg *config.Config) *redis.Client {
	if cfg.Redis.Addr == "" {
		return nil
	}
	password := cfg.Redis.Password
	if env := strings.TrimSpace(os.Getenv("REDIS_PASSWORD")); env != "" {
		password = env
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.Redis.Addr,
		Password: password,
		DB:       cfg.Redis.DB,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		log.Printf("redis 不可用，库存展示可能要等后端预热: %v", err)
		_ = client.Close()
		return nil
	}
	return client
}

func ensureFans(db *gorm.DB) (map[string]models.User, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte("user123"), 12)
	if err != nil {
		return nil, err
	}
	out := make(map[string]models.User, len(fanUsers))
	for _, item := range fanUsers {
		var user models.User
		err := db.Where("username = ?", item.Username).Limit(1).Find(&user).Error
		if err != nil {
			return nil, err
		}
		if user.ID == 0 {
			user = models.User{
				Username:     item.Username,
				Password:     string(hashed),
				Balance:      2000,
				BalanceCents: 200000,
				Role:         "user",
			}
			if err := db.Create(&user).Error; err != nil {
				return nil, err
			}
			log.Printf("购票用户: %s / user123", item.Username)
		}
		out[item.Username] = user
	}
	return out, nil
}

func enrichDescriptions(db *gorm.DB, organizerID int64) error {
	for title, text := range eventDescriptions {
		if err := db.Model(&models.Event{}).
			Where("organizer_id = ? AND title = ?", organizerID, title).
			Update("description", text).Error; err != nil {
			return err
		}
	}
	return nil
}

func seedComments(db *gorm.DB, rdb *redis.Client, organizerID int64, users map[string]models.User) (int, error) {
	created := 0
	now := time.Now()
	for _, item := range comments {
		event, err := findEvent(db, organizerID, item.EventTitle)
		if err != nil {
			continue
		}
		author, ok := users[item.Author]
		if !ok {
			continue
		}
		var existing models.EventComment
		err = db.Where("event_id = ? AND user_id = ? AND content = ?", event.ID, author.ID, item.Content).
			Limit(1).Find(&existing).Error
		if err != nil {
			return created, err
		}
		if existing.ID != 0 {
			continue
		}
		at := now.Add(-time.Duration(item.HoursAgo) * time.Hour)
		comment := models.EventComment{
			EventID:   event.ID,
			UserID:    author.ID,
			Content:   item.Content,
			LikeCount: item.Likes,
		}
		if err := db.Create(&comment).Error; err != nil {
			return created, err
		}
		if err := db.Model(&comment).Updates(map[string]any{
			"create_time": at,
			"update_time": at,
		}).Error; err != nil {
			return created, err
		}
		created++
		if rdb != nil {
			_ = rdb.Del(context.Background(), fmt.Sprintf("fuchang:comment:list:%d", event.ID)).Err()
		}
	}
	return created, nil
}

func seedRushSales(ctx context.Context, db *gorm.DB, rdb *redis.Client, settings service.InventoryBucketSettings, organizerID int64) (int, error) {
	now := time.Now()
	specs := []rushSpec{
		{
			EventTitle: "热浪巡回演唱会·广州", Name: "早鸟内场",
			PriceCents: 38800, Quota: 80, Sold: 57, PerUser: 2,
			StartOffset: -90 * time.Minute, EndOffset: 8 * time.Hour,
			Status: models.RushSaleStatusActive,
		},
		{
			EventTitle: "湾区电音节", Name: "通宵早鸟",
			PriceCents: 26800, Quota: 50, Sold: 32, PerUser: 1,
			StartOffset: -40 * time.Minute, EndOffset: 26 * time.Hour,
			Status: models.RushSaleStatusActive,
		},
		{
			EventTitle: "夏夜回声 Livehouse 专场", Name: "站票加场",
			PriceCents: 12800, Quota: 40, Sold: 33, PerUser: 2,
			StartOffset: -2 * time.Hour, EndOffset: 5 * time.Hour,
			Status: models.RushSaleStatusActive,
		},
		{
			EventTitle: "星河巡回演唱会·上海", Name: "内场特惠",
			PriceCents: 49900, Quota: 60, Sold: 0, PerUser: 2,
			StartOffset: 3 * time.Hour, EndOffset: 27 * time.Hour,
			Status: models.RushSaleStatusScheduled,
		},
	}
	created := 0
	for _, spec := range specs {
		tier, err := findCounterTier(db, organizerID, spec.EventTitle)
		if err != nil {
			log.Printf("跳过开售 %s: %v", spec.Name, err)
			continue
		}
		var existing models.RushSaleCampaign
		err = db.Where("organizer_id = ? AND name = ?", organizerID, spec.Name).Limit(1).Find(&existing).Error
		if err != nil {
			return created, err
		}
		if existing.ID != 0 {
			continue
		}
		if spec.PriceCents >= tier.PriceCents || spec.Quota > tier.RemainingQuota {
			log.Printf("跳过开售 %s: 价格或库存不满足", spec.Name)
			continue
		}
		remaining := spec.Quota - spec.Sold
		if remaining < 0 {
			remaining = 0
		}
		campaign := models.RushSaleCampaign{
			OrganizerID:    organizerID,
			TicketTierID:   tier.ID,
			Name:           spec.Name,
			RushPriceCents: spec.PriceCents,
			TotalQuota:     spec.Quota,
			RemainingQuota: remaining,
			PerUserLimit:   spec.PerUser,
			StartsAt:       now.Add(spec.StartOffset),
			EndsAt:         now.Add(spec.EndOffset),
			Status:         spec.Status,
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&campaign).Error; err != nil {
				return err
			}
			return service.EnsureRushBuckets(tx, &campaign, settings)
		}); err != nil {
			return created, err
		}
		if err := rewriteRushBuckets(ctx, db, rdb, settings, &campaign, remaining); err != nil {
			return created, err
		}
		created++
		log.Printf("限时开售: %s / %s 余 %d/%d", spec.EventTitle, spec.Name, remaining, spec.Quota)
	}
	if rdb != nil {
		_ = rdb.Del(ctx, "fuchang:catalog:rush_sales:list").Err()
	}
	return created, nil
}

func seedRecentOrders(db *gorm.DB, users map[string]models.User, organizerID int64) (int, int, error) {
	type plan struct {
		EventTitle string
		Author     string
		Qty        int
		DaysAgo    int
		HoursAgo   int
		Status     models.TicketOrderStatus
		Source     models.TicketOrderSource
		Used       bool
	}
	plans := []plan{
		{EventTitle: "热浪巡回演唱会·广州", Author: "广州热浪粉", Qty: 2, DaysAgo: 1, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceRushSale},
		{EventTitle: "热浪巡回演唱会·广州", Author: "票根收藏家", Qty: 1, DaysAgo: 2, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "热浪巡回演唱会·广州", Author: "林晓雨", Qty: 2, DaysAgo: 3, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "热浪巡回演唱会·广州", Author: "周末出门", Qty: 1, HoursAgo: 3, Status: models.TicketOrderStatusCancelled, Source: models.TicketOrderSourceNormal},
		{EventTitle: "夏夜回声 Livehouse 专场", Author: "江城夜猫", Qty: 2, DaysAgo: 1, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal, Used: true},
		{EventTitle: "夏夜回声 Livehouse 专场", Author: "第一次看live", Qty: 1, DaysAgo: 2, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "夏夜回声 Livehouse 专场", Author: "老王看演出", Qty: 2, DaysAgo: 4, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceRushSale},
		{EventTitle: "湾区电音节", Author: "音乐节钉子户", Qty: 1, DaysAgo: 1, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceRushSale},
		{EventTitle: "湾区电音节", Author: "广州热浪粉", Qty: 1, DaysAgo: 5, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "星河巡回演唱会·上海", Author: "外滩散步", Qty: 2, DaysAgo: 2, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "江城之声演唱会", Author: "北漂阿凯", Qty: 1, DaysAgo: 3, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "青城山音乐节", Author: "音乐节钉子户", Qty: 2, DaysAgo: 6, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "川剧新编·折子戏夜", Author: "成都夜宵", Qty: 2, DaysAgo: 2, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "微光城市影像展", Author: "林晓雨", Qty: 1, DaysAgo: 1, Status: models.TicketOrderStatusPaid, Source: models.TicketOrderSourceNormal},
		{EventTitle: "海风电子音乐节", Author: "周末出门", Qty: 2, HoursAgo: 5, Status: models.TicketOrderStatusPendingPayment, Source: models.TicketOrderSourceNormal},
	}

	orders, tickets := 0, 0
	now := time.Now()
	for i, item := range plans {
		user, ok := users[item.Author]
		if !ok {
			continue
		}
		event, session, venue, tier, err := loadSale(db, organizerID, item.EventTitle)
		if err != nil {
			continue
		}
		key := fmt.Sprintf("demo-life-%s-%d-%d", item.Author, event.ID, i)
		var existing models.TicketOrder
		if err := db.Where("user_id = ? AND idempotency_key = ?", user.ID, key).Limit(1).Find(&existing).Error; err != nil {
			return orders, tickets, err
		}
		if existing.ID != 0 {
			n, err := ensureOrderTickets(db, &existing, item.Qty, item.Used, whenOrNow(existing.PaidAt, now))
			if err != nil {
				return orders, tickets, err
			}
			tickets += n
			continue
		}
		when := now.Add(-time.Duration(item.DaysAgo) * 24 * time.Hour).Add(-time.Duration(item.HoursAgo) * time.Hour)
		unit := tier.PriceCents
		if item.Source == models.TicketOrderSourceRushSale {
			var campaign models.RushSaleCampaign
			if err := db.Where("ticket_tier_id = ? AND organizer_id = ?", tier.ID, organizerID).
				Order("id desc").Limit(1).Find(&campaign).Error; err == nil && campaign.ID != 0 {
				unit = campaign.RushPriceCents
			}
		}
		amount := unit * int64(item.Qty)
		display := displayName(item.Author)
		order := models.TicketOrder{
			OrderNo:               fmt.Sprintf("FCDEMO%d", time.Now().UnixNano()+int64(i)),
			UserID:                user.ID,
			OrganizerID:           organizerID,
			EventID:               event.ID,
			SessionID:             session.ID,
			OrderSource:           item.Source,
			Status:                item.Status,
			PaymentStatus:         models.PaymentStatusUnpaid,
			TotalAmountCents:      amount,
			ContactName:           display,
			ContactPhone:          fmt.Sprintf("1380000%04d", 1000+i),
			RealNameRequired:      event.RealNameRequired,
			PurchaseNoticeVersion: "demo",
			IdempotencyKey:        key,
			ExpiresAt:             when.Add(15 * time.Minute),
		}
		if item.Status == models.TicketOrderStatusPaid {
			order.PaymentStatus = models.PaymentStatusPaid
			order.PaidAt = &when
		}
		if item.Status == models.TicketOrderStatusCancelled {
			order.PaymentStatus = models.PaymentStatusUnpaid
			reason := "支付超时自动取消"
			order.CancelReason = reason
			order.CancelledAt = &when
		}
		if err := db.Create(&order).Error; err != nil {
			return orders, tickets, err
		}
		line := models.TicketOrderItem{
			OrderID:                 order.ID,
			TicketTierID:            tier.ID,
			Quantity:                item.Qty,
			UnitPriceCents:          unit,
			EventTitleSnapshot:      event.Title,
			SessionStartsAtSnapshot: session.StartsAt,
			VenueNameSnapshot:       venue.Name,
			VenueAddressSnapshot:    venue.Address,
			TierNameSnapshot:        tier.Name,
		}
		if err := db.Create(&line).Error; err != nil {
			return orders, tickets, err
		}
		payStatus := models.PaymentTransactionPending
		if item.Status == models.TicketOrderStatusPaid {
			payStatus = models.PaymentTransactionSuccess
		}
		if item.Status == models.TicketOrderStatusCancelled {
			payStatus = models.PaymentTransactionClosed
		}
		pay := models.PaymentTransaction{
			PaymentNo:         fmt.Sprintf("PAYDEMO%d", order.ID),
			OrderID:           order.ID,
			UserID:            user.ID,
			Provider:          "sandbox",
			ProviderPaymentID: fmt.Sprintf("sandbox-demo-%d", order.ID),
			AmountCents:       amount,
			Status:            payStatus,
			Scenario:          "success",
			ExpiresAt:         when.Add(15 * time.Minute),
		}
		if item.Status == models.TicketOrderStatusPaid {
			pay.PaidAt = &when
		}
		if err := db.Create(&pay).Error; err != nil {
			return orders, tickets, err
		}
		if item.Status == models.TicketOrderStatusPaid {
			n, err := issueTickets(db, &order, &line, item.Qty, item.Used, when)
			if err != nil {
				return orders, tickets, err
			}
			tickets += n
		}
		orders++
	}
	return orders, tickets, nil
}

func whenOrNow(paid *time.Time, now time.Time) time.Time {
	if paid != nil {
		return *paid
	}
	return now
}

func ensureOrderTickets(db *gorm.DB, order *models.TicketOrder, qty int, used bool, when time.Time) (int, error) {
	if order.Status != models.TicketOrderStatusPaid {
		return 0, nil
	}
	var count int64
	if err := db.Model(&models.AdmissionTicket{}).Where("order_id = ?", order.ID).Count(&count).Error; err != nil {
		return 0, err
	}
	if count > 0 {
		return 0, nil
	}
	var line models.TicketOrderItem
	if err := db.Where("order_id = ?", order.ID).First(&line).Error; err != nil {
		return 0, err
	}
	return issueTickets(db, order, &line, qty, used, when)
}

func issueTickets(db *gorm.DB, order *models.TicketOrder, line *models.TicketOrderItem, qty int, used bool, when time.Time) (int, error) {
	created := 0
	for seq := 1; seq <= qty; seq++ {
		ticket := models.AdmissionTicket{
			Base:         models.Base{ID: nextID()},
			TicketNo:     fmt.Sprintf("TKDEMO%d%02d", order.ID, seq),
			OrderID:      order.ID,
			OrderItemID:  line.ID,
			SequenceNo:   seq,
			UserID:       order.UserID,
			OrganizerID:  order.OrganizerID,
			EventID:      order.EventID,
			SessionID:    order.SessionID,
			TicketTierID: line.TicketTierID,
			Status:       models.AdmissionTicketStatusValid,
			IssuedAt:     when,
		}
		if used && seq == 1 {
			ticket.Status = models.AdmissionTicketStatusUsed
			ticket.UsedAt = &when
		}
		if err := db.Create(&ticket).Error; err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func occupyPopularTiers(ctx context.Context, db *gorm.DB, rdb *redis.Client, settings service.InventoryBucketSettings, organizerID int64) error {
	targets := map[string]int{
		"热浪巡回演唱会·广州":  86,
		"夏夜回声 Livehouse 专场": 74,
		"湾区电音节":             61,
		"星河巡回演唱会·上海":        38,
		"江城之声演唱会":           29,
		"青城山音乐节":            22,
	}
	for title, sold := range targets {
		tier, err := findCounterTier(db, organizerID, title)
		if err != nil {
			continue
		}
		if tier.SoldCount >= int64(sold) {
			continue
		}
		remaining := tier.TotalQuota - sold
		if remaining < 0 {
			remaining = 0
		}
		if err := applyTierStock(ctx, db, rdb, settings, tier, remaining, int64(sold), models.TicketTierStatusOnSale, 0); err != nil {
			return err
		}
	}
	return nil
}

func seedWaitlist(ctx context.Context, db *gorm.DB, rdb *redis.Client, settings service.InventoryBucketSettings, users map[string]models.User, organizerID int64) (int, error) {
	event, session, venue, tier, err := loadSale(db, organizerID, "珠江夜航·民谣专场")
	if err != nil {
		return 0, nil
	}
	if err := applyTierStock(ctx, db, rdb, settings, tier, 0, int64(tier.TotalQuota), models.TicketTierStatusWaitlist, 3); err != nil {
		return 0, err
	}
	authors := []string{"广州热浪粉", "江城夜猫", "林晓雨"}
	created := 0
	now := time.Now()
	for i, name := range authors {
		user, ok := users[name]
		if !ok {
			continue
		}
		key := fmt.Sprintf("demo-life-wait-%s-%d", name, event.ID)
		var existing models.WaitlistEntry
		if err := db.Where("user_id = ? AND idempotency_key = ?", user.ID, key).Limit(1).Find(&existing).Error; err != nil {
			return created, err
		}
		if existing.ID != 0 {
			continue
		}
		paid := now.Add(-time.Duration(i+1) * time.Hour)
		entry := models.WaitlistEntry{
			Base:                    models.Base{ID: nextID()},
			WaitlistNo:              fmt.Sprintf("WLDEMO%d", time.Now().UnixNano()+int64(i)),
			UserID:                  user.ID,
			OrganizerID:             organizerID,
			EventID:                 event.ID,
			SessionID:               session.ID,
			TicketTierID:            tier.ID,
			Quantity:                1,
			AmountCents:             tier.PriceCents,
			Status:                  models.WaitlistStatusQueued,
			ContactName:             displayName(name),
			ContactPhone:            fmt.Sprintf("1390000%04d", 2000+i),
			PurchaseNoticeVersion:   "demo",
			EventTitleSnapshot:      event.Title,
			SessionStartsAtSnapshot: session.StartsAt,
			VenueNameSnapshot:       venue.Name,
			VenueAddressSnapshot:    venue.Address,
			TierNameSnapshot:        tier.Name,
			IdempotencyKey:          key,
			ExpiresAt:               paid.Add(24 * time.Hour),
			PaidAt:                  &paid,
		}
		if err := db.Create(&entry).Error; err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func applyTierStock(
	ctx context.Context,
	db *gorm.DB,
	rdb *redis.Client,
	settings service.InventoryBucketSettings,
	tier *models.TicketTier,
	remaining int,
	sold int64,
	status models.TicketTierStatus,
	waitlistPending int,
) error {
	if err := db.Model(&models.TicketTier{}).Where("id = ?", tier.ID).Updates(map[string]any{
		"remaining_quota":  remaining,
		"sold_count":       sold,
		"status":           status,
		"waitlist_pending": waitlistPending,
	}).Error; err != nil {
		return err
	}
	n := settings.EffectiveBucketCount(tier.TotalQuota)
	parts := service.SplitQuotaEvenly(remaining, n)
	soldParts := service.SplitQuotaEvenly(int(sold), n)
	for i := 0; i < n; i++ {
		row := models.TicketTierBucket{
			TierID:         tier.ID,
			BucketNo:       i,
			RemainingQuota: parts[i],
			SoldCount:      int64(soldParts[i]),
		}
		if err := db.Where("tier_id = ? AND bucket_no = ?", tier.ID, i).
			Assign(row).
			FirstOrCreate(&row).Error; err != nil {
			return err
		}
		if rdb != nil {
			if err := rdb.Set(ctx, service.TicketStockBucketKey(tier.ID, i), parts[i], 0).Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func rewriteRushBuckets(ctx context.Context, db *gorm.DB, rdb *redis.Client, settings service.InventoryBucketSettings, campaign *models.RushSaleCampaign, remaining int) error {
	n := settings.EffectiveBucketCount(campaign.TotalQuota)
	parts := service.SplitQuotaEvenly(remaining, n)
	ttl := time.Until(campaign.EndsAt) + time.Hour
	for i := 0; i < n; i++ {
		if err := db.Model(&models.RushCampaignBucket{}).
			Where("campaign_id = ? AND bucket_no = ?", campaign.ID, i).
			Update("remaining_quota", parts[i]).Error; err != nil {
			return err
		}
		if rdb != nil {
			if err := rdb.Set(ctx, service.RushStockBucketKey(campaign.ID, i), parts[i], ttl).Err(); err != nil {
				return err
			}
		}
	}
	return nil
}

func bumpCatalogCache(ctx context.Context, rdb *redis.Client, organizerID int64, db *gorm.DB) {
	if rdb == nil {
		return
	}
	_ = rdb.Del(ctx, "fuchang:catalog:rush_sales:list", "fuchang:catalog:meta").Err()
	_ = rdb.Incr(ctx, "fuchang:catalog:events:list:ver").Err()
	var events []models.Event
	_ = db.Select("id").Where("organizer_id = ?", organizerID).Find(&events).Error
	for _, event := range events {
		_ = rdb.Del(ctx, fmt.Sprintf("fuchang:catalog:event:%d", event.ID), fmt.Sprintf("fuchang:comment:list:%d", event.ID)).Err()
	}
}

func findEvent(db *gorm.DB, organizerID int64, title string) (*models.Event, error) {
	var event models.Event
	err := db.Where("organizer_id = ? AND title = ?", organizerID, title).First(&event).Error
	return &event, err
}

func findCounterTier(db *gorm.DB, organizerID int64, title string) (*models.TicketTier, error) {
	_, _, _, tier, err := loadSale(db, organizerID, title)
	if err != nil {
		return nil, err
	}
	return tier, nil
}

func loadSale(db *gorm.DB, organizerID int64, title string) (*models.Event, *models.EventSession, *models.Venue, *models.TicketTier, error) {
	event, err := findEvent(db, organizerID, title)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if event.SaleMode.IsSeated() {
		return nil, nil, nil, nil, fmt.Errorf("%s 是选座场", title)
	}
	var session models.EventSession
	if err := db.Where("event_id = ?", event.ID).Order("starts_at asc").First(&session).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	var venue models.Venue
	if err := db.First(&venue, session.VenueID).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	var tier models.TicketTier
	if err := db.Where("session_id = ?", session.ID).Order("id asc").First(&tier).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	return event, &session, &venue, &tier, nil
}

func displayName(username string) string {
	return username
}
