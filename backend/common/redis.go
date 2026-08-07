package common

import (
	"gofun/config"
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client
var Ctx = context.Background()

func InitRedis(cfg config.RedisConfig) {
	RDB = redis.NewClient(&redis.Options{
		Addr:     cfg.Addr,
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})
	pong, err := RDB.Ping(Ctx).Result()
	if err != nil {
		panic(fmt.Errorf("redis 链接失败:%v", err))
	}
	fmt.Println("redis 链接成功", pong)
}
