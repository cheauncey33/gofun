package repository

import (
	"WHU_Snack_GO/models"
	"context"

	"gorm.io/gorm"
)

type CategoryRepository interface {
	FindAll(ctx context.Context) ([]models.Category, error)
	FindByID(ctx context.Context, id int64) (*models.Category, error)
	FindSubCategories(ctx context.Context, parentID int64) ([]models.Category, error)
	Create(ctx context.Context, category *models.Category) error
	Update(ctx context.Context, category *models.Category) error
	Delete(ctx context.Context, id int64) error
}

type categoryRepoImpl struct {
	db *gorm.DB
}

func NewCategoryRepository(db *gorm.DB) CategoryRepository {
	return &categoryRepoImpl{db: db}
}

func (r *categoryRepoImpl) FindAll(ctx context.Context) ([]models.Category, error) {
	var categories []models.Category
	err := r.db.WithContext(ctx).Order("sort ASC, id ASC").Find(&categories).Error
	return categories, err
}

func (r *categoryRepoImpl) FindByID(ctx context.Context, id int64) (*models.Category, error) {
	var category models.Category
	err := r.db.WithContext(ctx).First(&category, id).Error
	return &category, err
}

func (r *categoryRepoImpl) FindSubCategories(ctx context.Context, parentID int64) ([]models.Category, error) {
	var categories []models.Category
	err := r.db.WithContext(ctx).Where("parent_id = ?", parentID).Find(&categories).Error
	return categories, err
}

func (r *categoryRepoImpl) Create(ctx context.Context, category *models.Category) error {
	return r.db.WithContext(ctx).Create(category).Error
}

func (r *categoryRepoImpl) Update(ctx context.Context, category *models.Category) error {
	return r.db.WithContext(ctx).Save(category).Error
}

func (r *categoryRepoImpl) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Delete(&models.Category{}, id).Error
}
