package common

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
)

var RDB *redis.Client
var Ctx = context.Background()

func InitRedis() {
	RDB = redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})
	pong, err := RDB.Ping(Ctx).Result()
	if err != nil {
		panic(fmt.Errorf("redis 链接失败:%v", err))
	}
	fmt.Println("redis 链接成功", pong)
}
