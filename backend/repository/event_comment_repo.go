package repository

import (
	"gofun/models"
	"context"

	"gorm.io/gorm"
)

type EventCommentRepository interface {
	Create(ctx context.Context, comment *models.EventComment) error
	FindByID(ctx context.Context, id int64) (*models.EventComment, error)
	SoftDelete(ctx context.Context, id, userID int64) (int64, error)
	ListByEvent(ctx context.Context, eventID int64, page, pageSize int) ([]models.EventComment, int64, error)
	ListRecentByEvent(ctx context.Context, eventID int64, limit int) ([]models.EventComment, error)
	IncrementLikeCount(ctx context.Context, id int64, delta int64) error
	FlushLikeCounts(ctx context.Context, counts map[int64]int64) error
}

type eventCommentRepo struct {
	db *gorm.DB
}

func NewEventCommentRepository(db *gorm.DB) EventCommentRepository {
	return &eventCommentRepo{db: db}
}

func (r *eventCommentRepo) Create(ctx context.Context, comment *models.EventComment) error {
	return r.db.WithContext(ctx).Create(comment).Error
}

func (r *eventCommentRepo) FindByID(ctx context.Context, id int64) (*models.EventComment, error) {
	var comment models.EventComment
	err := r.db.WithContext(ctx).First(&comment, id).Error
	return &comment, err
}

func (r *eventCommentRepo) SoftDelete(ctx context.Context, id, userID int64) (int64, error) {
	res := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&models.EventComment{})
	return res.RowsAffected, res.Error
}

func (r *eventCommentRepo) ListByEvent(
	ctx context.Context,
	eventID int64,
	page, pageSize int,
) ([]models.EventComment, int64, error) {
	var (
		list  []models.EventComment
		total int64
	)
	q := r.db.WithContext(ctx).Model(&models.EventComment{}).Where("event_id = ?", eventID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	err := q.Preload("User").
		Order("create_time DESC").
		Limit(pageSize).
		Offset((page - 1) * pageSize).
		Find(&list).Error
	return list, total, err
}

func (r *eventCommentRepo) ListRecentByEvent(
	ctx context.Context,
	eventID int64,
	limit int,
) ([]models.EventComment, error) {
	var list []models.EventComment
	err := r.db.WithContext(ctx).
		Where("event_id = ?", eventID).
		Preload("User").
		Order("create_time DESC").
		Limit(limit).
		Find(&list).Error
	return list, err
}

func (r *eventCommentRepo) IncrementLikeCount(ctx context.Context, id int64, delta int64) error {
	return r.db.WithContext(ctx).
		Model(&models.EventComment{}).
		Where("id = ?", id).
		UpdateColumn("like_count", gorm.Expr("like_count + ?", delta)).Error
}

func (r *eventCommentRepo) FlushLikeCounts(ctx context.Context, counts map[int64]int64) error {
	if len(counts) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for id, count := range counts {
			if err := tx.Model(&models.EventComment{}).
				Where("id = ?", id).
				UpdateColumn("like_count", count).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
