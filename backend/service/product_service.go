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
	Page       int    `form:"page,default=1"`
	PageSize   int    `form:"page_size,default=10"`
	CategoryID *int64 `form:"category_id"`
	Keyword    string `form:"keyword"`
	SortBy     string `form:"sort_by"` // price_asc, price_desc, sales, newest
}

func (q *ProductListQuery) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 10
	}
}

func GetProductList(query ProductListQuery) ([]models.Product, int64, error) {
	query.Normalize()
	cacheKey := fmt.Sprintf("product:list:page=%d:size=%d:cat=%v:kw=%s:sort=%s",
		query.Page, query.PageSize, query.CategoryID, query.Keyword, query.SortBy)

	// L1: 本地缓存
	if val, found := common.LocalCache.Get(cacheKey); found {
		cached := val.(ProductListCache)
		return cached.Products, cached.Total, nil
	}

	// L2: Redis
	redisVal, err := common.RDB.Get(common.Ctx, cacheKey).Result()
	if err == nil {
		var cached ProductListCache
		if json.Unmarshal([]byte(redisVal), &cached) == nil {
			common.LocalCache.Set(cacheKey, cached, 0)
			return cached.Products, cached.Total, nil
		}
	}

	// L3: MySQL — 构建查询
	var products []models.Product
	var total int64

	db := common.DB.Model(&models.Product{}).Preload("Category").Where("status = ?", models.ProductStatusOnSale)

	if query.CategoryID != nil {
		db = db.Where("category_id = ?", *query.CategoryID)
	}
	if query.Keyword != "" {
		db = db.Where("name LIKE ? OR description LIKE ?", "%"+query.Keyword+"%", "%"+query.Keyword+"%")
	}

	// 排序
	switch query.SortBy {
	case "price_asc":
		db = db.Order("price ASC")
	case "price_desc":
		db = db.Order("price DESC")
	case "sales":
		db = db.Order("sales_count DESC")
	case "newest":
		db = db.Order("create_time DESC")
	default:
		db = db.Order("id DESC")
	}

	db.Count(&total)

	offset := (query.Page - 1) * query.PageSize
	err = db.Limit(query.PageSize).Offset(offset).Find(&products).Error
	if err != nil {
		return nil, 0, err
	}

	// 回填缓存
	cached := ProductListCache{Products: products, Total: total}
	common.LocalCache.Set(cacheKey, cached, 0)
	jsonBytes, _ := json.Marshal(cached)
	common.RDB.Set(common.Ctx, cacheKey, string(jsonBytes), 5*time.Minute)

	return products, total, nil
}

func GetProductDetail(productID int64) (*models.Product, error) {
	var product models.Product
	err := common.DB.Preload("Category").First(&product, productID).Error
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func GetCategoryList() ([]models.Category, error) {
	var categories []models.Category
	err := common.DB.Order("sort ASC, id ASC").Find(&categories).Error
	return categories, err
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
