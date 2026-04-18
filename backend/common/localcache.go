package common

import (
	"time"

	gocache "github.com/patrickmn/go-cache"
)

// LocalCache 是进程内的本地缓存实例（全局共享）
var LocalCache *gocache.Cache

func InitLocalCache() {
	// 第一个参数：默认过期时间 30 秒
	// 第二个参数：每 1 分钟自动清理一次过期的键，防止内存泄露
	LocalCache = gocache.New(30*time.Second, 1*time.Minute)
}
