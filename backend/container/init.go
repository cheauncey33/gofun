package container

import (
	"context"
	"fmt"
	"gofun/common"
	"gofun/config"
	"gofun/migrations"
	"gofun/models"
	"gofun/repository"
	"gofun/search"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/snowflake"
	gocache "github.com/patrickmn/go-cache"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/extra/redisotel/v9"
	"github.com/redis/go-redis/v9"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
	otelgorm "gorm.io/plugin/opentelemetry/tracing"
)

func NewContainer(cfg *config.Config) (*Container, error) {
	db, workerDB, err := initDB(cfg.MySQL, cfg.Telemetry.Enabled)
	if err != nil {
		return nil, fmt.Errorf("init db: %w", err)
	}
	rdb, err := initRedis(cfg.Redis, cfg.Telemetry.Enabled)
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
	eventSearcher, preferES := initEventSearcher(cfg.Elasticsearch)

	c := &Container{
		DB:                db,
		WorkerDB:          workerDB,
		RDB:               rdb,
		MQConn:            conn,
		MQChannel:         ch,
		MQQueueName:       queueName,
		MQRetryQueueName:  retryQueueName,
		MQDLXName:         dlxName,
		MQDLQName:         dlqName,
		MQQueueType:       cfg.RabbitMQ.QueueType,
		SnowflakeNode:     node,
		JWTSecret:         []byte(cfg.JWT.Secret),
		TicketQRSecret:    []byte(cfg.TicketQR.Secret),
		TicketQRSecrets:   ticketQRSecrets(cfg),
		LocalCache:        lc,
		EventSearcher:     eventSearcher,
		SearchPreferES:    preferES,
		mqURL:             cfg.RabbitMQ.URL,
		TicketCatalogRepo: repository.NewTicketCatalogRepository(db),
	}

	// 闭包捕获 container 状态
	c.PublishPersistent = func(body []byte) error {
		return c.publishPersistent(context.Background(), body, nil)
	}
	c.PublishPersistentWithHeaders = c.publishPersistent
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

func initDB(cfg config.MySQLConfig, tracingEnabled bool) (httpDB, workerDB *gorm.DB, err error) {
	httpDB, err = openGormDB(cfg.DSN, tracingEnabled)
	if err != nil {
		return nil, nil, err
	}
	if err := configureSQLPool(httpDB, cfg.MaxIdleConns, cfg.MaxOpenConns, cfg.ConnMaxLifetime); err != nil {
		return nil, nil, err
	}

	if err := migrations.Run(httpDB); err != nil {
		return nil, nil, fmt.Errorf("执行数据库迁移: %w", err)
	}
	if httpDB.Migrator().HasIndex(&models.User{}, "uni_user_phone") {
		if err := httpDB.Migrator().DropIndex(&models.User{}, "uni_user_phone"); err != nil {
			return nil, nil, fmt.Errorf("删除遗留手机号唯一索引: %w", err)
		}
	}

	var adminCount int64
	httpDB.Model(&models.User{}).Where("role = ?", "admin").Count(&adminCount)
	if adminCount == 0 {
		hashed, _ := bcrypt.GenerateFromPassword([]byte("admin123"), 12)
		httpDB.Where(models.User{Username: "admin"}).Assign(models.User{
			Password:     string(hashed),
			Balance:      9999,
			BalanceCents: 999900,
			Role:         "admin",
		}).FirstOrCreate(&models.User{})
		log.Println("默认管理员已创建: admin / admin123")
	}

	workerDB = httpDB
	if cfg.WorkerMaxOpenConns > 0 {
		workerDB, err = openGormDB(cfg.DSN, tracingEnabled)
		if err != nil {
			return nil, nil, fmt.Errorf("init worker db: %w", err)
		}
		workerIdle := cfg.WorkerMaxIdleConns
		if workerIdle <= 0 {
			workerIdle = cfg.MaxIdleConns
		}
		if workerIdle > cfg.WorkerMaxOpenConns {
			workerIdle = cfg.WorkerMaxOpenConns
		}
		if err := configureSQLPool(workerDB, workerIdle, cfg.WorkerMaxOpenConns, cfg.ConnMaxLifetime); err != nil {
			return nil, nil, err
		}
		log.Printf(
			"MySQL 连接成功 (HTTP pool max_open=%d idle=%d; worker pool max_open=%d idle=%d)",
			cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.WorkerMaxOpenConns, workerIdle,
		)
	} else {
		log.Printf("MySQL 连接成功 (shared pool max_open=%d idle=%d)", cfg.MaxOpenConns, cfg.MaxIdleConns)
	}
	return httpDB, workerDB, nil
}

func openGormDB(dsn string, tracingEnabled bool) (*gorm.DB, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		NamingStrategy: schema.NamingStrategy{
			SingularTable: true,
		},
	})
	if err != nil {
		return nil, err
	}
	if tracingEnabled {
		if err := db.Use(otelgorm.NewPlugin()); err != nil {
			return nil, fmt.Errorf("启用 GORM tracing: %w", err)
		}
	}
	return db, nil
}

func configureSQLPool(db *gorm.DB, maxIdle, maxOpen, connMaxLifetimeSec int) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	if maxIdle > 0 {
		sqlDB.SetMaxIdleConns(maxIdle)
	}
	if maxOpen > 0 {
		sqlDB.SetMaxOpenConns(maxOpen)
	}
	if connMaxLifetimeSec > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(connMaxLifetimeSec) * time.Second)
	}
	return nil
}

// ticketingSchemaModels 用于测试票务模型边界；运行时结构由 migrations/*.sql 决定。
func ticketingSchemaModels() []interface{} {
	return []interface{}{
		&models.User{},
		&models.Organizer{},
		&models.OrganizerMember{},
		&models.Venue{},
		&models.Event{},
		&models.EventSession{},
		&models.TicketTier{},
		&models.TicketTierBucket{},
		&models.TicketOrder{},
		&models.TicketOrderItem{},
		&models.TicketOrderAttendee{},
		&models.TicketOrderOutbox{},
		&models.TicketOrderConsumerInbox{},
		&models.TicketStockRecoveryFence{},
		&models.PaymentTransaction{},
		&models.PaymentCallback{},
		&models.WaitlistEntry{},
		&models.WaitlistAttendee{},
		&models.RushSaleCampaign{},
		&models.RushCampaignBucket{},
		&models.AdmissionTicket{},
		&models.TicketVerificationRecord{},
		&models.EventComment{},
		&models.SeatLayout{},
		&models.Seat{},
		&models.SessionSeat{},
	}
}

func initRedis(cfg config.RedisConfig, tracingEnabled bool) (*redis.Client, error) {
	var rdb *redis.Client
	if strings.TrimSpace(cfg.MasterName) != "" {
		rdb = redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    cfg.MasterName,
			SentinelAddrs: cfg.SentinelAddrs,
			Password:      cfg.Password,
			DB:            cfg.DB,
			PoolSize:      cfg.PoolSize,
		})
	} else {
		rdb = redis.NewClient(&redis.Options{
			Addr:     cfg.Addr,
			Password: cfg.Password,
			DB:       cfg.DB,
			PoolSize: cfg.PoolSize,
		})
	}
	if tracingEnabled {
		if err := redisotel.InstrumentTracing(rdb); err != nil {
			return nil, fmt.Errorf("启用 Redis tracing: %w", err)
		}
	}
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
	if err := declareOrderQueues(
		ch,
		cfg.QueueName,
		cfg.RetryQueueName,
		cfg.DLXName,
		cfg.DLQName,
		cfg.QueueType,
	); err != nil {
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

func initEventSearcher(cfg config.ElasticsearchConfig) (search.EventSearcher, bool) {
	prefer := cfg.PreferElasticsearch()
	if !cfg.Enabled {
		log.Println("Elasticsearch 未启用，活动关键词检索走 MySQL LIKE")
		return search.NoopEventSearcher{}, false
	}
	addrs := cfg.Addresses
	if len(addrs) == 0 {
		addrs = []string{"http://127.0.0.1:9200"}
	}
	es, err := search.NewESEventSearcher(addrs, cfg.Username, cfg.Password, cfg.Index)
	if err != nil {
		log.Printf("Elasticsearch 客户端初始化失败，降级 MySQL LIKE: %v", err)
		return search.NoopEventSearcher{}, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := es.EnsureIndex(ctx); err != nil {
		log.Printf("Elasticsearch EnsureIndex 失败，降级 MySQL LIKE: %v", err)
		return search.NoopEventSearcher{}, false
	}
	log.Printf("Elasticsearch 已启用 index=%s prefer=%v", cfg.Index, prefer)
	return es, prefer
}

func (c *Container) publishPersistent(ctx context.Context, body []byte, headers amqp.Table) error {
	c.publishMu.Lock()
	defer c.publishMu.Unlock()

	if c.MQChannel == nil || c.MQChannel.IsClosed() {
		ch, err := c.newMQChannel()
		if err != nil {
			return err
		}
		if err := ch.Confirm(false); err != nil {
			_ = ch.Close()
			return fmt.Errorf("开启发布确认失败: %w", err)
		}
		c.MQChannel = ch
		common.MQChannel = ch
	}
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
			Headers:      headers,
		},
	)
	if err != nil {
		_ = c.MQChannel.Close()
		c.MQChannel = nil
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
	c.mqConnMu.Lock()
	defer c.mqConnMu.Unlock()

	for attempt := 0; attempt < 2; attempt++ {
		if c.MQConn == nil || c.MQConn.IsClosed() {
			conn, err := amqp.Dial(c.mqURL)
			if err != nil {
				return nil, fmt.Errorf("rabbitMQ reconnect: %w", err)
			}
			c.MQConn = conn
			common.MQConn = conn
		}
		ch, err := c.MQConn.Channel()
		if err != nil {
			_ = c.MQConn.Close()
			c.MQConn = nil
			continue
		}
		if err := declareOrderQueues(
			ch,
			c.MQQueueName,
			c.MQRetryQueueName,
			c.MQDLXName,
			c.MQDLQName,
			c.MQQueueType,
		); err != nil {
			_ = ch.Close()
			return nil, err
		}
		return ch, nil
	}
	return nil, fmt.Errorf("rabbit getting channel error after reconnect")
}

func declareOrderQueues(
	ch *amqp.Channel,
	queueName, retryQueueName, dlxName, dlqName, queueType string,
) error {
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
	args := rabbitQueueArgs(queueType, nil)
	if _, err := ch.QueueDeclare(dlqName, true, false, false, false, args); err != nil {
		return fmt.Errorf("声明死信队列失败: %w", err)
	}
	if err := ch.QueueBind(dlqName, dlqName, dlxName, false, nil); err != nil {
		return fmt.Errorf("绑定死信队列失败: %w", err)
	}
	if _, err := ch.QueueDeclare(queueName, true, false, false, false, args); err != nil {
		return fmt.Errorf("声明队列失败: %w", err)
	}
	if _, err := ch.QueueDeclare(retryQueueName, true, false, false, false, args); err != nil {
		return fmt.Errorf("声明重试队列失败: %w", err)
	}
	return nil
}

func rabbitQueueArgs(queueType string, base amqp.Table) amqp.Table {
	if queueType != "quorum" {
		return base
	}
	args := amqp.Table{"x-queue-type": "quorum"}
	for key, value := range base {
		args[key] = value
	}
	return args
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
