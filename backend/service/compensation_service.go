package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"context"
	"fmt"
	"log"
	"strconv"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

type CompensationService struct {
	db  *gorm.DB
	rdb *redis.Client
}

func NewCompensationService(c *container.Container) *CompensationService {
	return &CompensationService{db: c.DB, rdb: c.RDB}
}

func (s *CompensationService) RunStockCompensation() {
	log.Printf("数据定时更新触发")
	var products []models.Product
	if err := s.db.Find(&products).Error; err != nil {
		log.Printf("FIND 操作失败，数据库异常:%v", err)
		return
	}
	abnormal := 0
	for _, p := range products {
		redisKey := fmt.Sprintf("snack:stock:%d", p.ID)

		redisStockStr, err := s.rdb.Get(context.Background(), redisKey).Result()
		if err != nil {
			s.rdb.Set(context.Background(), redisKey, p.Stock, 0)
			log.Printf("发现库存缓存缺失:商品[%s], 已按DB库存%d重建", p.Name, p.Stock)
			abnormal++
			continue
		}
		redisStock, err := strconv.ParseInt(redisStockStr, 10, 64)
		if err != nil {
			continue
		}
		if redisStock > int64(p.Stock) {
			s.rdb.Set(context.Background(), redisKey, p.Stock, 0)
			log.Printf("发现库存超卖风险:商品[%s],DB实际:%d,Redis记录:%d", p.Name, p.Stock, redisStock)
			abnormal++
		}
	}
	log.Printf("本次数据定时更新结束 共修复%d个库存缓存异常", abnormal)
}
