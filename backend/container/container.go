package container

import (
	"context"
	"gofun/repository"
	"gofun/search"
	"sync"

	"github.com/bwmarrin/snowflake"
	gocache "github.com/patrickmn/go-cache"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Container struct {
	// DB 供 HTTP / 控制器路径使用。
	DB *gorm.DB
	// WorkerDB 供 Consumer / Outbox / Timeout / 库存补偿等后台路径使用。
	// 未启用 worker 池时与 DB 指向同一实例。
	WorkerDB         *gorm.DB
	RDB              *redis.Client
	MQConn           *amqp.Connection
	MQChannel        *amqp.Channel
	MQQueueName      string
	MQRetryQueueName string
	MQDLXName        string
	MQDLQName        string
	MQQueueType      string
	SnowflakeNode    *snowflake.Node
	JWTSecret        []byte
	TicketQRSecret   []byte
	TicketQRSecrets  [][]byte
	LocalCache       *gocache.Cache
	EventSearcher    search.EventSearcher
	SearchPreferES   bool

	TicketCatalogRepo repository.TicketCatalogRepository

	publishMu                    sync.Mutex
	mqConnMu                     sync.Mutex
	mqURL                        string
	PublishPersistent            func(body []byte) error
	PublishPersistentWithHeaders func(ctx context.Context, body []byte, headers amqp.Table) error
	NewMQChannel                 func() (*amqp.Channel, error)
}
