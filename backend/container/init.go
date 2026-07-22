package container

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/migrations"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/repository"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	gocache "github.com/patrickmn/go-cache"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func NewContainer(cfg *config.Config) (*Container, error) {
	db, err := initDB(cfg.MySQL)
	if err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}

	rdb, err := initRedis(cfg.Redis)
	if err != nil {
		return nil, fmt.Errorf("init redis: %w", err)
	}

	conn, ch, queueName, retryQueueName, dlxName, dlqName, err := initRabbitMQ(cfg.RabbitMQ)
	if err != nil {
		return nil, fmt.Errorf("init rabbitmq: %w", err)
	}

	node, err := initSnowflake(cfg.Snowflake.NodeID)
	if err != nil {
		return nil, fmt.Errorf("init snowflake: %w", err)
	}

	lc := initLocalCache()

	c := &Container{
		DB:                db,
		RDB:               rdb,
		MQConn:            conn,
		MQChannel:         ch,
		MQQueueName:       queueName,
		MQRetryQueueName:  retryQueueName,
		MQDLXName:         dlxName,
		MQDLQName:         dlqName,
		SnowflakeNode:     node,
		JWTSecret:         []byte(cfg.JWT.Secret),
		TicketQRSecret:    []byte(cfg.TicketQR.Secret),
		TicketQRSecrets:   ticketQRSecrets(cfg),
		LocalCache:        lc,
		ProductRepo:       repository.NewProductRepository(db),
		OrderRepo:         repository.NewOrderRepository(db),
		CategoryRepo:      repository.NewCategoryRepository(db),
		TicketCatalogRepo: repository.NewTicketCatalogRepository(db),
	}

	// 闭包捕获 container 状态
	c.PublishPersistent = c.publishPersistent
	c.NewMQChannel = c.newMQChannel

	// 兼容旧代码: 设置 common 全局变量
	common.DB = db
	common.RDB = rdb
	common.Node = node
	common.LocalCache = lc
	common.MQConn = conn
	common.MQChannel = ch
	common.MQQueueName = queueName
	common.MQRetryQueueName = retryQueueName
	common.MQDLXName = dlxName
	common.MQDLQName = dlqName
	common.SetJWTSecret(cfg.JWT.Secret)

	return c, nil
}

func initDB(cfg config.MySQLConfig) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(cfg.DSN), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
	if err != nil {
		return nil, err
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetime) * time.Second)

	if err := migrations.Run(db); err != nil {
		return nil, fmt.Errorf("执行数据库迁移: %w", err)
	}
	if db.Migrator().HasIndex(&models.User{}, "uni_user_phone") {
		if err := db.Migrator().DropIndex(&models.User{}, "uni_user_phone"); err != nil {
			return nil, fmt.Errorf("删除遗留手机号唯一索引: %w", err)
		}
	}

	var adminCount int64
	db.Model(&models.User{}).Where("role = ?", "admin").Count(&adminCount)
	if adminCount == 0 {
		var dorm models.Dormitory
		if err := db.Where(models.Dormitory{BuildingName: "默认宿舍", RoomNumber: "000"}).
			FirstOrCreate(&dorm).Error; err != nil {
			return nil, fmt.Errorf("创建默认管理员宿舍失败: %w", err)
		}
		hashed, _ := bcrypt.GenerateFromPassword([]byte("admin123"), 12)
		db.Where(models.User{Username: "admin"}).Assign(models.User{
			Password:     string(hashed),
			Balance:      9999,
			BalanceCents: 999900,
			DormID:       dorm.ID,
			Role:         "admin",
		}).FirstOrCreate(&models.User{})
		log.Println("默认管理员已创建: admin / admin123")
	}

	log.Println("MySQL 连接成功")
	return db, nil
}

// ticketingSchemaModels 用于测试票务模型边界；运行时结构由 migrations/*.sql 决定。
// 遗留零食电商模型仍保留在源码中用于回滚，但不属于赴场迁移基线。
func ticketingSchemaModels() []interface{} {
	return []interface{}{
		&models.Dormitory{},
		&models.User{},
		&models.Organizer{},
		&models.OrganizerMember{},
		&models.Venue{},
		&models.Event{},
		&models.EventSession{},
		&models.TicketTier{},
		&models.TicketOrder{},
		&models.TicketOrderItem{},
		&models.TicketOrderAttendee{},
		&models.TicketOrderOutbox{},
		&models.RushSaleCampaign{},
		&models.AdmissionTicket{},
		&models.TicketVerificationRecord{},
		&models.EventComment{},
	}
}

func initRedis(cfg config.RedisConfig) (*redis.Client, error) {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})
	ctx := context.Background()
	pong, err := rdb.Ping(ctx).Result()
	if err != nil {
		return nil, fmt.Errorf("redis 链接失败: %w", err)
	}
	fmt.Println("redis 链接成功", pong)
	return rdb, nil
}

func initRabbitMQ(cfg config.RabbitMQConfig) (*amqp.Connection, *amqp.Channel, string, string, string, string, error) {
	conn, err := amqp.Dial(cfg.URL)
	if err != nil {
		return nil, nil, "", "", "", "", fmt.Errorf("rabbitMQ connection error: %w", err)
	}
	ch, err := conn.Channel()
	if err != nil {
		return nil, nil, "", "", "", "", fmt.Errorf("rabbit getting channel error: %w", err)
	}
	if err := declareOrderQueues(ch, cfg.QueueName, cfg.RetryQueueName, cfg.DLXName, cfg.DLQName); err != nil {
		return nil, nil, "", "", "", "", err
	}
	if err := ch.Confirm(false); err != nil {
		return nil, nil, "", "", "", "", fmt.Errorf("开启发布确认失败: %w", err)
	}
	fmt.Println("Success to init rabbitMQ")
	return conn, ch, cfg.QueueName, cfg.RetryQueueName, cfg.DLXName, cfg.DLQName, nil
}

func initSnowflake(nodeID int64) (*snowflake.Node, error) {
	node, err := snowflake.NewNode(nodeID)
	if err != nil {
		return nil, fmt.Errorf("snowflake init error: %v", err)
	}
	fmt.Println("snowflake node started")
	return node, nil
}

func initLocalCache() *gocache.Cache {
	return gocache.New(30*time.Second, 1*time.Minute)
}

func (c *Container) publishPersistent(body []byte) error {
	if c.MQChannel == nil {
		return fmt.Errorf("rabbitMQ publish channel is not initialized")
	}
	c.publishMu.Lock()
	defer c.publishMu.Unlock()

	ctx := context.Background()
	confirm, err := c.MQChannel.PublishWithDeferredConfirmWithContext(
		ctx,
		"",
		c.MQQueueName,
		true,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			DeliveryMode: amqp.Persistent,
		},
	)
	if err != nil {
		return err
	}
	if confirm == nil {
		return fmt.Errorf("rabbitMQ publish confirm is not enabled")
	}
	waitCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	acked, err := confirm.WaitContext(waitCtx)
	if err != nil {
		return err
	}
	if !acked {
		return fmt.Errorf("rabbitMQ publish not acknowledged")
	}
	return nil
}

func (c *Container) newMQChannel() (*amqp.Channel, error) {
	if c.MQConn == nil {
		return nil, fmt.Errorf("rabbitMQ connection is not initialized")
	}
	ch, err := c.MQConn.Channel()
	if err != nil {
		return nil, fmt.Errorf("rabbit getting channel error: %w", err)
	}
	if err := declareOrderQueues(ch, c.MQQueueName, c.MQRetryQueueName, c.MQDLXName, c.MQDLQName); err != nil {
		_ = ch.Close()
		return nil, err
	}
	return ch, nil
}

func declareOrderQueues(ch *amqp.Channel, queueName, retryQueueName, dlxName, dlqName string) error {
	if queueName == "" {
		return fmt.Errorf("rabbitMQ queue name is empty")
	}
	if retryQueueName == "" {
		retryQueueName = queueName + ".retry"
	}
	if dlxName == "" {
		dlxName = queueName + ".dlx"
	}
	if dlqName == "" {
		dlqName = queueName + ".dlq"
	}
	if err := ch.ExchangeDeclare(dlxName, "direct", true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明死信交换机失败: %w", err)
	}
	if _, err := ch.QueueDeclare(dlqName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明死信队列失败: %w", err)
	}
	if err := ch.QueueBind(dlqName, dlqName, dlxName, false, nil); err != nil {
		return fmt.Errorf("绑定死信队列失败: %w", err)
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明队列失败: %w", err)
	}
	if _, err := ch.QueueDeclare(retryQueueName, true, false, false, false, nil); err != nil {
		return fmt.Errorf("声明重试队列失败: %w", err)
	}
	return nil
}

func ticketQRSecrets(cfg *config.Config) [][]byte {
	secrets := [][]byte{[]byte(cfg.TicketQR.Secret)}
	for _, previous := range cfg.TicketQR.PreviousSecrets {
		previous = strings.TrimSpace(previous)
		if previous == "" || previous == cfg.TicketQR.Secret {
			continue
		}
		secrets = append(secrets, []byte(previous))
	}
	return secrets
}

var _ sync.Mutex
