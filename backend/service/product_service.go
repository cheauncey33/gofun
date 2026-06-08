package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/repository"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
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
	SortBy     string `form:"sort_by"`
}

func (q *ProductListQuery) Normalize() {
	if q.Page < 1 {
		q.Page = 1
	}
	if q.PageSize < 1 || q.PageSize > 100 {
		q.PageSize = 10
	}
}

type ProductService struct {
	db          *gorm.DB
	rdb         *redis.Client
	localCache  *gocache.Cache
	productRepo repository.ProductRepository
}

func NewProductService(c *container.Container) *ProductService {
	return &ProductService{
		db:          c.DB,
		rdb:         c.RDB,
		localCache:  c.LocalCache,
		productRepo: c.ProductRepo,
	}
}

func (s *ProductService) GetProductList(query ProductListQuery) ([]models.Product, int64, error) {
	query.Normalize()
	ctx := context.Background()
	cacheKey := fmt.Sprintf("product:list:page=%d:size=%d:cat=%v:kw=%s:sort=%s",
		query.Page, query.PageSize, query.CategoryID, query.Keyword, query.SortBy)

	if val, found := s.localCache.Get(cacheKey); found {
		cached := val.(ProductListCache)
		products := cloneProducts(cached.Products)
		s.hydrateProductStocks(ctx, products)
		return products, cached.Total, nil
	}

	redisVal, err := s.rdb.Get(ctx, cacheKey).Result()
	if err == nil {
		var cached ProductListCache
		if json.Unmarshal([]byte(redisVal), &cached) == nil {
			s.localCache.Set(cacheKey, cached, 0)
			products := cloneProducts(cached.Products)
			s.hydrateProductStocks(ctx, products)
			return products, cached.Total, nil
		}
	}

	var products []models.Product
	var total int64

	db := s.db.Model(&models.Product{}).Preload("Category").Where("status = ?", models.ProductStatusOnSale)

	if query.CategoryID != nil {
		db = db.Where("category_id = ?", *query.CategoryID)
	}
	if query.Keyword != "" {
		db = db.Where("name LIKE ? OR description LIKE ?", "%"+query.Keyword+"%", "%"+query.Keyword+"%")
	}

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

	cacheProducts := cloneProducts(products)
	stripProductStocks(cacheProducts)
	cached := ProductListCache{Products: cacheProducts, Total: total}
	s.localCache.Set(cacheKey, cached, 0)
	jsonBytes, _ := json.Marshal(cached)
	s.rdb.Set(ctx, cacheKey, string(jsonBytes), 5*time.Minute)

	s.hydrateProductStocks(ctx, products)
	return products, total, nil
}

func (s *ProductService) GetProductDetail(productID int64) (*models.Product, error) {
	var product models.Product
	err := s.db.Preload("Category").First(&product, productID).Error
	if err != nil {
		return nil, err
	}
	s.hydrateProductStocks(context.Background(), []models.Product{product})
	return &product, nil
}

func (s *ProductService) GetCategoryList() ([]models.Category, error) {
	var categories []models.Category
	err := s.db.Order("sort ASC, id ASC").Find(&categories).Error
	return categories, err
}

func cloneProducts(products []models.Product) []models.Product {
	cloned := make([]models.Product, len(products))
	copy(cloned, products)
	return cloned
}

func stripProductStocks(products []models.Product) {
	for i := range products {
		products[i].Stock = 0
	}
}

func (s *ProductService) hydrateProductStocks(ctx context.Context, products []models.Product) {
	if len(products) == 0 {
		return
	}

	keys := make([]string, 0, len(products))
	for _, product := range products {
		keys = append(keys, fmt.Sprintf("snack:stock:%d", product.ID))
	}

	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		return
	}
	for i, value := range values {
		if value == nil {
			continue
		}
		stock, ok := redisValueToInt(value)
		if ok {
			products[i].Stock = stock
		}
	}
}

func redisValueToInt(value any) (int, bool) {
	switch v := value.(type) {
	case string:
		n, err := strconv.Atoi(v)
		return n, err == nil
	case []byte:
		n, err := strconv.Atoi(string(v))
		return n, err == nil
	case int:
		return v, true
	case int64:
		return int(v), true
	default:
		return 0, false
	}
}

func (s *ProductService) InitProductStockToRedis() error {
	var products []models.Product
	if err := s.db.Find(&products).Error; err != nil {
		return fmt.Errorf("读取商品数据失败: %v", err)
	}
	for _, p := range products {
		redisKey := fmt.Sprintf("snack:stock:%d", p.ID)
		err := s.rdb.Set(context.Background(), redisKey, p.Stock, 0).Err()
		if err != nil {
			return fmt.Errorf("预热商品%v失败-%v", p.ID, err)
		}
	}
	fmt.Println("成功预热", len(products), "个商品到redis中！")
	return nil
}
