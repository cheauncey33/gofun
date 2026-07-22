package container

import (
	"WHU_Snack_GO/repository"
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
	SnowflakeNode    *snowflake.Node
	JWTSecret         []byte
	TicketQRSecret    []byte
	TicketQRSecrets   [][]byte
	LocalCache        *gocache.Cache

	ProductRepo       repository.ProductRepository
	OrderRepo         repository.OrderRepository
	CategoryRepo      repository.CategoryRepository
	TicketCatalogRepo repository.TicketCatalogRepository

	publishMu         sync.Mutex
	PublishPersistent func(body []byte) error
	NewMQChannel      func() (*amqp.Channel, error)
}
