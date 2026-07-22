package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/repository"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/bwmarrin/snowflake"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var (
	ErrCommentNotFound   = errors.New("评论不存在")
	ErrCommentForbidden  = errors.New("无权操作该评论")
	ErrCommentInvalid    = errors.New("评论内容不合法")
	ErrCommentRateLimited = errors.New("评论过于频繁，请稍后再试")
	ErrCommentAlreadyLiked = errors.New("已经点过赞了")
	ErrCommentEventNotPublished = errors.New("活动不存在或未发布")
)

const (
	commentListCacheLimit = 50
	commentListCacheTTL   = 3 * time.Minute
	commentEmptyCacheTTL  = 30 * time.Second
	commentRateLimitMax   = 10
	commentRateLimitWindow = time.Minute
	commentLikeFlushEvery = 20 * time.Second
)

type EventCommentView struct {
	ID        string    `json:"id"`
	EventID   string    `json:"event_id"`
	UserID    string    `json:"user_id"`
	Username  string    `json:"username"`
	Content   string    `json:"content"`
	LikeCount int64     `json:"like_count"`
	CreatedAt time.Time `json:"created_at"`
	IsOwner   bool      `json:"is_owner,omitempty"`
}

type EventCommentService struct {
	repo     repository.EventCommentRepository
	catalog  repository.TicketCatalogRepository
	db       *gorm.DB
	rdb      *redis.Client
	node     *snowflake.Node
}

func NewEventCommentService(c *container.Container) *EventCommentService {
	return &EventCommentService{
		repo:    repository.NewEventCommentRepository(c.DB),
		catalog: c.TicketCatalogRepo,
		db:      c.DB,
		rdb:     c.RDB,
		node:    c.SnowflakeNode,
	}
}

func commentListKey(eventID int64) string {
	return fmt.Sprintf("fuchang:comment:list:%d", eventID)
}

func commentRateKey(userID int64) string {
	return fmt.Sprintf("fuchang:comment:rl:%d", userID)
}

func commentLikeZKey(eventID int64) string {
	return fmt.Sprintf("fuchang:comment:like:%d", eventID)
}

func commentLikedKey(userID, commentID int64) string {
	return fmt.Sprintf("fuchang:comment:liked:%d:%d", userID, commentID)
}

func (s *EventCommentService) List(
	ctx context.Context,
	eventID int64,
	page, pageSize int,
	viewerID int64,
) ([]EventCommentView, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 50 {
		pageSize = 20
	}
	if err := s.ensurePublishedEvent(ctx, eventID); err != nil {
		return nil, 0, err
	}

	// 第一页走按活动拆分的列表缓存，避免全局大 key。
	if page == 1 && pageSize <= commentListCacheLimit {
		if cached, ok, err := s.loadListCache(ctx, eventID); err == nil && ok {
			views := cached
			if viewerID > 0 {
				for i := range views {
					uid, _ := strconv.ParseInt(views[i].UserID, 10, 64)
					views[i].IsOwner = uid == viewerID
				}
			}
			total := int64(len(views))
			// 缓存只存近期投影；total 用库计数更准
			if _, t, err := s.repo.ListByEvent(ctx, eventID, 1, 1); err == nil {
				total = t
			}
			if pageSize < len(views) {
				views = views[:pageSize]
			}
			return views, total, nil
		}
	}

	list, total, err := s.repo.ListByEvent(ctx, eventID, page, pageSize)
	if err != nil {
		return nil, 0, err
	}
	views := make([]EventCommentView, 0, len(list))
	for _, item := range list {
		views = append(views, toCommentView(item, viewerID))
	}
	if page == 1 {
		_ = s.fillListCache(ctx, eventID)
	}
	return views, total, nil
}

func (s *EventCommentService) Create(
	ctx context.Context,
	eventID, userID int64,
	content string,
) (*EventCommentView, error) {
	normalized, err := normalizeCommentContent(content)
	if err != nil {
		return nil, err
	}
	content = normalized
	if err := s.ensurePublishedEvent(ctx, eventID); err != nil {
		return nil, err
	}
	if err := s.checkRateLimit(ctx, userID); err != nil {
		return nil, err
	}

	comment := &models.EventComment{
		Base:    models.Base{ID: s.node.Generate().Int64()},
		EventID: eventID,
		UserID:  userID,
		Content: content,
	}
	if err := s.repo.Create(ctx, comment); err != nil {
		return nil, err
	}
	_ = s.rdb.Del(ctx, commentListKey(eventID)).Err()

	full, err := s.repo.FindByID(ctx, comment.ID)
	if err != nil {
		view := toCommentView(*comment, userID)
		return &view, nil
	}
	_ = s.db.WithContext(ctx).Preload("User").First(full, full.ID).Error
	view := toCommentView(*full, userID)
	return &view, nil
}

func (s *EventCommentService) Delete(ctx context.Context, commentID, userID int64) error {
	comment, err := s.repo.FindByID(ctx, commentID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCommentNotFound
		}
		return err
	}
	if comment.UserID != userID {
		return ErrCommentForbidden
	}
	affected, err := s.repo.SoftDelete(ctx, commentID, userID)
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrCommentNotFound
	}
	_ = s.rdb.Del(ctx, commentListKey(comment.EventID)).Err()
	_ = s.rdb.ZRem(ctx, commentLikeZKey(comment.EventID), strconv.FormatInt(commentID, 10)).Err()
	return nil
}

func (s *EventCommentService) Like(ctx context.Context, commentID, userID int64) (int64, error) {
	comment, err := s.repo.FindByID(ctx, commentID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, ErrCommentNotFound
		}
		return 0, err
	}
	likedKey := commentLikedKey(userID, commentID)
	ok, err := s.rdb.SetNX(ctx, likedKey, "1", 30*24*time.Hour).Result()
	if err != nil {
		return 0, err
	}
	if !ok {
		score, _ := s.rdb.ZScore(ctx, commentLikeZKey(comment.EventID), strconv.FormatInt(commentID, 10)).Result()
		if score > 0 {
			return int64(score), ErrCommentAlreadyLiked
		}
		return comment.LikeCount, ErrCommentAlreadyLiked
	}

	member := strconv.FormatInt(commentID, 10)
	score, err := s.rdb.ZIncrBy(ctx, commentLikeZKey(comment.EventID), 1, member).Result()
	if err != nil {
		_, _ = s.rdb.Del(ctx, likedKey).Result()
		return 0, err
	}
	// 热路径写 ZSET；计数异步落库。同时轻量 +1，避免刷新前看到 0。
	_ = s.repo.IncrementLikeCount(ctx, commentID, 1)
	_ = s.rdb.Del(ctx, commentListKey(comment.EventID)).Err()
	return int64(score), nil
}

// StartLikeCountFlusher 定期把 ZSET 分数刷回 MySQL like_count（面试：热数据 Redis，最终落库）。
func (s *EventCommentService) StartLikeCountFlusher(ctx context.Context) {
	ticker := time.NewTicker(commentLikeFlushEvery)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.flushLikeCounts(ctx)
		}
	}
}

func (s *EventCommentService) flushLikeCounts(ctx context.Context) {
	var cursor uint64
	for {
		keys, next, err := s.rdb.Scan(ctx, cursor, "fuchang:comment:like:*", 20).Result()
		if err != nil {
			return
		}
		for _, key := range keys {
			pairs, err := s.rdb.ZRangeWithScores(ctx, key, 0, -1).Result()
			if err != nil || len(pairs) == 0 {
				continue
			}
			counts := make(map[int64]int64, len(pairs))
			for _, pair := range pairs {
				id, err := strconv.ParseInt(fmt.Sprint(pair.Member), 10, 64)
				if err != nil {
					continue
				}
				counts[id] = int64(pair.Score)
			}
			_ = s.repo.FlushLikeCounts(ctx, counts)
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

func (s *EventCommentService) ensurePublishedEvent(ctx context.Context, eventID int64) error {
	event, err := s.catalog.FindEventByID(ctx, eventID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return ErrCommentEventNotPublished
		}
		return err
	}
	if event.Status != models.EventStatusPublished {
		return ErrCommentEventNotPublished
	}
	return nil
}

func (s *EventCommentService) checkRateLimit(ctx context.Context, userID int64) error {
	key := commentRateKey(userID)
	n, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		return err
	}
	if n == 1 {
		_ = s.rdb.Expire(ctx, key, commentRateLimitWindow).Err()
	}
	if n > commentRateLimitMax {
		return ErrCommentRateLimited
	}
	return nil
}

func (s *EventCommentService) loadListCache(
	ctx context.Context,
	eventID int64,
) ([]EventCommentView, bool, error) {
	raw, err := s.rdb.Get(ctx, commentListKey(eventID)).Bytes()
	if err == redis.Nil {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	var views []EventCommentView
	if err := json.Unmarshal(raw, &views); err != nil {
		return nil, false, err
	}
	return views, true, nil
}

func (s *EventCommentService) fillListCache(ctx context.Context, eventID int64) error {
	list, err := s.repo.ListRecentByEvent(ctx, eventID, commentListCacheLimit)
	if err != nil {
		return err
	}
	views := make([]EventCommentView, 0, len(list))
	for _, item := range list {
		views = append(views, toCommentView(item, 0))
	}
	body, err := json.Marshal(views)
	if err != nil {
		return err
	}
	ttl := commentListCacheTTL
	if len(views) == 0 {
		ttl = commentEmptyCacheTTL
	}
	return s.rdb.Set(ctx, commentListKey(eventID), body, ttl).Err()
}

func normalizeCommentContent(content string) (string, error) {
	content = strings.TrimSpace(content)
	n := utf8.RuneCountInString(content)
	if n < 2 || n > 500 {
		return "", fmt.Errorf("%w: 长度需为 2-500 字", ErrCommentInvalid)
	}
	return content, nil
}

func toCommentView(item models.EventComment, viewerID int64) EventCommentView {
	username := "赴场用户"
	if item.User != nil && item.User.Username != "" {
		username = item.User.Username
	}
	return EventCommentView{
		ID:        strconv.FormatInt(item.ID, 10),
		EventID:   strconv.FormatInt(item.EventID, 10),
		UserID:    strconv.FormatInt(item.UserID, 10),
		Username:  username,
		Content:   item.Content,
		LikeCount: item.LikeCount,
		CreatedAt: item.CreateTime,
		IsOwner:   viewerID > 0 && item.UserID == viewerID,
	}
}
