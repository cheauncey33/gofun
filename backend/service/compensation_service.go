package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"fmt"
	"log"
	"strconv"
)

func RunStockCompensation() {
	log.Printf("数据定时更新触发")
	var products []models.Product
	if err := common.DB.Find(&products).Error; err != nil {
		log.Printf("FIND 操作失败，数据库异常:%v", err)
		return
	}
	//记录数据不符的个数
	abnormal := 0
	for _, p := range products {
		redisKey := fmt.Sprintf("snack:stock:%d", p.ID)

		redisStockStr, err := common.RDB.Get(common.Ctx, redisKey).Result()
		//按照redisKey没有读到结果
		if err != nil {
			continue // 可能在这个期间刚好过期了或者网络波动，先跳过
		}
		redisStock, err := strconv.ParseInt(redisStockStr, 10, 64)
		if err != nil {
			continue //字符串转换失败
		}
		if redisStock < int64(p.Stock) {
			common.RDB.Set(common.Ctx, redisKey, p.Stock, 0)
			log.Printf("发现一条库存异常:商品[%s],DB实际:%d,Redis记录:%d", p.Name, p.Stock, redisStock)
			abnormal++
		}
	}
	log.Printf("本次数据定时更新结束 共修复%d个商品少卖异常", abnormal)
}
