package repository

import (
	"WHU_Snack_GO/models"
	"context"

	"gorm.io/gorm"
)

type ProductListParams struct {
	Page       int
	PageSize   int
	CategoryID *int64
	Keyword    string
	Status     *models.ProductStatus
	SortBy     string // "price_asc", "price_desc", "sales", "newest"
}

type ProductRepository interface {
	FindByID(ctx context.Context, id int64) (*models.Product, error)
	List(ctx context.Context, params ProductListParams) ([]models.Product, int64, error)
	Create(ctx context.Context, product *models.Product) error
	Update(ctx context.Context, product *models.Product) error
	UpdateStock(ctx context.Context, id int64, delta int) error
	UpdateSalesCount(ctx context.Context, id int64, delta int64) error
	Delete(ctx context.Context, id int64) error
}

type productRepoImpl struct {
	db *gorm.DB
}

func NewProductRepository(db *gorm.DB) ProductRepository {
	return &productRepoImpl{db: db}
}

func (r *productRepoImpl) FindByID(ctx context.Context, id int64) (*models.Product, error) {
	var product models.Product
	err := r.db.WithContext(ctx).Preload("Category").First(&product, id).Error
	if err != nil {
		return nil, err
	}
	return &product, nil
}

func (r *productRepoImpl) List(ctx context.Context, params ProductListParams) ([]models.Product, int64, error) {
	var products []models.Product
	var total int64

	query := r.db.WithContext(ctx).Model(&models.Product{}).Preload("Category")

	// 默认只查在售商品
	if params.Status != nil {
		query = query.Where("status = ?", *params.Status)
	} else {
		query = query.Where("status = ?", models.ProductStatusOnSale)
	}

	// 分类筛选
	if params.CategoryID != nil {
		query = query.Where("category_id = ?", *params.CategoryID)
	}

	// 关键词搜索
	if params.Keyword != "" {
		query = query.Where("name LIKE ? OR description LIKE ?",
			"%"+params.Keyword+"%", "%"+params.Keyword+"%")
	}

	// 排序
	switch params.SortBy {
	case "price_asc":
		query = query.Order("price ASC")
	case "price_desc":
		query = query.Order("price DESC")
	case "sales":
		query = query.Order("sales_count DESC")
	case "newest":
		query = query.Order("create_time DESC")
	default:
		query = query.Order("id DESC")
	}

	// 统计总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 分页
	offset := (params.Page - 1) * params.PageSize
	err := query.Limit(params.PageSize).Offset(offset).Find(&products).Error

	return products, total, err
}

func (r *productRepoImpl) Create(ctx context.Context, product *models.Product) error {
	return r.db.WithContext(ctx).Create(product).Error
}

func (r *productRepoImpl) Update(ctx context.Context, product *models.Product) error {
	return r.db.WithContext(ctx).Save(product).Error
}

func (r *productRepoImpl) UpdateStock(ctx context.Context, id int64, delta int) error {
	result := r.db.WithContext(ctx).Model(&models.Product{}).
		Where("id = ? AND stock >= ?", id, -delta).
		Update("stock", gorm.Expr("stock + ?", delta))
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (r *productRepoImpl) UpdateSalesCount(ctx context.Context, id int64, delta int64) error {
	return r.db.WithContext(ctx).Model(&models.Product{}).
		Where("id = ?", id).
		Update("sales_count", gorm.Expr("sales_count + ?", delta)).Error
}

func (r *productRepoImpl) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&models.Product{}, id).Error
}
