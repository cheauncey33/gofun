package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"encoding/json"
	"fmt"
	"time"
)

type ProductListCache struct {
	Products []models.Product `json:"products"`
	Total    int64            `json:"total"`
}

type ProductListQuery struct {
	Page     int `form:"page,default=1"`
	PageSize int `form:"page_size,default=10"`
}

// service 完成什么？对数据库的crud
// GetPRoductList完成什么？按照传来的ProductListQuery查询目标页的商品记录，存储到返回值中
// 所以要read product表
func GetProductList(query ProductListQuery) ([]models.Product, int64, error) {
	cacheKey := fmt.Sprintf("product:list:page=%d:size=%d", query.Page, query.PageSize)

	//========  一级缓存——查本地内存缓存（听说是纳秒级）  ===========
	if val, found := common.LocalCache.Get(cacheKey); found {
		cached := val.(ProductListCache)
		return cached.Products, cached.Total, nil
	}

	//========  二级缓存——查redis缓存（微秒级）  ==========
	//Redis 和本地缓存不同，Redis 里只能存字符串（或二进制），不能直接存 Go 的结构体。
	//写入时：把 ProductListCache 序列化成 JSON 字符串再存
	//读取时：从 Redis 取出 JSON 字符串再反序列化回来----unmarshal
	redisVal, err := common.RDB.Get(common.Ctx, cacheKey).Result()
	if err == nil {
		//说明查redis查到了
		var cached ProductListCache
		if json.Unmarshal([]byte(redisVal), &cached) == nil {
			common.LocalCache.Set(cacheKey, cached, 0)
			return cached.Products, cached.Total, nil
		}
	}
	//========  三级缓存——查mysql硬盘缓存（毫秒级）  ==========
	var products []models.Product
	var total int64

	//先统计一共有多少商品 便于展示一共多少页
	common.DB.Model(&models.Product{}).Count(&total)

	offset := (query.Page - 1) * query.PageSize
	err = common.DB.Limit(query.PageSize).Offset(offset).Find(&products).Error
	if err != nil {
		return nil, 0, err
	}
	//查完mysql写入一二级缓存
	//写入本地一级缓存
	common.LocalCache.Set(cacheKey, ProductListCache{
		Products: products,
		Total:    total,
	}, 0)
	//写入redis二级缓存 有效期设为5分钟
	jsonBytes, _ := json.Marshal(ProductListCache{
		Products: products,
		Total:    total,
	})
	common.RDB.Set(common.Ctx, cacheKey, string(jsonBytes), 5*time.Minute)
	return products, total, err

	// }
}

func InitProductStockToRedis() error {
	var products []models.Product
	if err := common.DB.Find(&products).Error; err != nil {
		return fmt.Errorf("读取商品数据失败: %v", err)
	}
	for _, p := range products {
		redisKey := fmt.Sprintf("snack:stock:%d", p.ID)
		err := common.RDB.Set(common.Ctx, redisKey, p.Stock, 0).Err()
		if err != nil {
			return fmt.Errorf("预热商品%v失败-%v", p.ID, err)
		}
	}
	fmt.Println("成功预热", len(products), "个商品到redis中！")
	return nil
}
