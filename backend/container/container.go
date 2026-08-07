package container

import (
	"WHU_Snack_GO/repository"
	"WHU_Snack_GO/search"
	"context"
	"sync"

	"github.com/bwmarrin/snowflake"
	gocache "github.com/patrickmn/go-cache"
	amqp "github.com/rabbitmq/amqp091-go"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type Container struct {
	DB               *gorm.DB
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
