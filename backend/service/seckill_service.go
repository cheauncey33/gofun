package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"WHU_Snack_GO/pkg/lock"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/bwmarrin/snowflake"
	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"
)

var seckillDeductLua = `
	local stockKey = KEYS[1]
	local userKey = KEYS[2]
	local tokenKey = KEYS[3]
	local userID = ARGV[1]
	local limit = tonumber(ARGV[2])
	local quantity = tonumber(ARGV[3])
	local expectedToken = ARGV[4]
	local ttlSeconds = tonumber(ARGV[5]) or 0

	if quantity == nil or quantity <= 0 then
	    return -4
	end

	local storedToken = redis.call('GET', tokenKey)
	if storedToken == false or storedToken ~= expectedToken then
	    return -3
	end
	redis.call('DEL', tokenKey)

	local stock = tonumber(redis.call('GET', stockKey)) or 0
	if stock < quantity then
	    return -1
	end

	local count = tonumber(redis.call('HGET', userKey, userID)) or 0
	if count + quantity > limit then
	    return -2
	end

	redis.call('DECRBY', stockKey, quantity)
	redis.call('HINCRBY', userKey, userID, quantity)
	if ttlSeconds > 0 then
	    redis.call('EXPIRE', stockKey, ttlSeconds)
	    redis.call('EXPIRE', userKey, ttlSeconds)
	end
	return 1
	`

type SeckillService struct {
	db            *gorm.DB
	rdb           *redis.Client
	snowflakeNode *snowflake.Node
	publish       func(body []byte) error
	jwtSecret     []byte
	lockTimeout   time.Duration
}

func NewSeckillService(c *container.Container, lockTimeoutSec int) *SeckillService {
	return &SeckillService{
		db:            c.DB,
		rdb:           c.RDB,
		snowflakeNode: c.SnowflakeNode,
		publish:       c.PublishPersistent,
		jwtSecret:     c.JWTSecret,
		lockTimeout:   time.Duration(lockTimeoutSec) * time.Second,
	}
}

func (s *SeckillService) GetSeckillActivityList(page, pageSize int) ([]models.SeckillActivity, int64, error) {
	var activities []models.SeckillActivity
	var total int64

	query := s.db.Model(&models.SeckillActivity{}).Preload("Product")
	query.Count(&total)

	offset := (page - 1) * pageSize
	err := query.Order("start_time DESC").Limit(pageSize).Offset(offset).Find(&activities).Error
	return activities, total, err
}

func (s *SeckillService) GetSeckillActivityDetail(activityID int64) (*models.SeckillActivity, error) {
	var activity models.SeckillActivity
	err := s.db.Preload("Product").First(&activity, activityID).Error
	return &activity, err
}

func (s *SeckillService) WarmUpSeckillActivity(activityID int64) error {
	var activity models.SeckillActivity
	if err := s.db.First(&activity, activityID).Error; err != nil {
		return err
	}

	now := time.Now()
	if now.Before(activity.StartTime) || now.After(activity.EndTime) {
		return fmt.Errorf("活动不在有效时间范围内")
	}

	cacheTTL := seckillCacheTTL(activity.EndTime)
	if cacheTTL <= 0 {
		s.clearSeckillRedisKeys(activityID)
		return fmt.Errorf("活动已结束")
	}

	stockKey, userCountKey := seckillRedisKeys(activityID)
	s.rdb.Set(context.Background(), stockKey, activity.Stock, cacheTTL)
	s.rdb.Expire(context.Background(), userCountKey, cacheTTL)

	s.db.Model(&activity).Updates(map[string]interface{}{
		"status":          models.SeckillStatusActive,
		"remaining_stock": activity.Stock,
	})

	return nil
}

func (s *SeckillService) GenerateSeckillToken(activityID, userID int64) (string, error) {
	var activity models.SeckillActivity
	if err := s.db.First(&activity, activityID).Error; err != nil {
		return "", fmt.Errorf("活动不存在")
	}

	now := time.Now()
	if now.Before(activity.StartTime) {
		return "", fmt.Errorf("秒杀活动尚未开始")
	}
	if now.After(activity.EndTime) {
		return "", fmt.Errorf("秒杀活动已结束")
	}
	if activity.Status != models.SeckillStatusActive {
		return "", fmt.Errorf("秒杀活动未激活")
	}

	userCountKey := fmt.Sprintf("seckill:user_count:%d", activityID)
	countStr, _ := s.rdb.HGet(context.Background(), userCountKey, strconv.FormatInt(userID, 10)).Result()
	if countStr != "" {
		count, _ := strconv.Atoi(countStr)
		if count >= activity.LimitPerUser {
			return "", fmt.Errorf("已达限购上限，已购买%d件", count)
		}
	}

	stockKey := fmt.Sprintf("seckill:stock:%d", activityID)
	stockStr, _ := s.rdb.Get(context.Background(), stockKey).Result()
	if stockStr != "" {
		stock, _ := strconv.Atoi(stockStr)
		if stock <= 0 {
			return "", fmt.Errorf("秒杀已售罄")
		}
	}

	ts := time.Now().Unix()
	payload := fmt.Sprintf("%d:%d:%d", activityID, userID, ts)
	mac := hmac.New(sha256.New, s.jwtSecret)
	mac.Write([]byte(payload))
	token := hex.EncodeToString(mac.Sum(nil))

	tokenKey := fmt.Sprintf("seckill:token:%d:%d", activityID, userID)
	s.rdb.Set(context.Background(), tokenKey, token, 60*time.Second)

	return token, nil
}

func (s *SeckillService) ExecuteSeckill(activityID, userID int64, token string, quantity int) error {
	if quantity <= 0 {
		return fmt.Errorf("购买数量必须大于0")
	}

	ctx := context.Background()
	lockKey := fmt.Sprintf("lock:seckill:%d:user:%d", activityID, userID)
	return lock.WithLock(ctx, s.rdb, lockKey, s.lockTimeout, func() error {
		stockKey := fmt.Sprintf("seckill:stock:%d", activityID)
		userKey := fmt.Sprintf("seckill:user_count:%d", activityID)
		tokenKey := fmt.Sprintf("seckill:token:%d:%d", activityID, userID)

		var activity models.SeckillActivity
		if err := s.db.First(&activity, activityID).Error; err != nil {
			return fmt.Errorf("活动不存在")
		}
		now := time.Now()
		if now.Before(activity.StartTime) {
			return fmt.Errorf("秒杀活动尚未开始")
		}
		if now.After(activity.EndTime) || activity.Status != models.SeckillStatusActive {
			s.clearSeckillRedisKeys(activityID)
			return fmt.Errorf("秒杀活动已结束")
		}
		cacheTTL := seckillCacheTTL(activity.EndTime)
		if cacheTTL <= 0 {
			s.clearSeckillRedisKeys(activityID)
			return fmt.Errorf("秒杀活动已结束")
		}

		result, err := s.rdb.Eval(ctx, seckillDeductLua,
			[]string{stockKey, userKey, tokenKey},
			userID, activity.LimitPerUser, quantity, token, int64(cacheTTL.Seconds()),
		).Result()
		if err != nil {
			return fmt.Errorf("系统繁忙")
		}

		resNum := result.(int64)
		switch resNum {
		case -1:
			return fmt.Errorf("秒杀已售罄")
		case -2:
			return fmt.Errorf("已达限购上限")
		case -3:
			return fmt.Errorf("秒杀令牌无效或已过期")
		case -4:
			return fmt.Errorf("购买数量必须大于0")
		case 1:
			orderID := s.snowflakeNode.Generate().Int64()
			var input CreateOrderInput
			input.Items = append(input.Items, CreateOrderItemInput{ProductID: activity.ProductID, Num: quantity})

			msg := OrderMessage{
				OrderID:           orderID,
				UserID:            userID,
				SeckillActivityID: activityID,
				SeckillPrice:      activity.SeckillPrice,
				Items:             input,
			}
			body, _ := json.Marshal(msg)
			err = s.publish(body)
			if err != nil {
				s.rdb.IncrBy(ctx, stockKey, int64(quantity))
				s.rdb.HIncrBy(ctx, userKey, strconv.FormatInt(userID, 10), int64(-quantity))
				return fmt.Errorf("订单排队失败")
			}

			stockStr, _ := s.rdb.Get(ctx, stockKey).Result()
			remaining, _ := strconv.Atoi(stockStr)
			s.db.Model(&activity).Update("remaining_stock", remaining)

			return nil
		}

		return fmt.Errorf("未知错误")
	})
}

func (s *SeckillService) CreateSeckillActivity(activity *models.SeckillActivity) error {
	return s.db.Create(activity).Error
}

func (s *SeckillService) UpdateSeckillActivity(id int64, activity *models.SeckillActivity) error {
	if err := s.db.Model(&models.SeckillActivity{}).Where("id = ?", id).Updates(activity).Error; err != nil {
		return err
	}

	var updated models.SeckillActivity
	if err := s.db.First(&updated, id).Error; err != nil {
		return err
	}
	s.refreshSeckillRedisTTL(updated)
	return nil
}

func (s *SeckillService) DeleteSeckillActivity(id int64) error {
	if err := s.db.Delete(&models.SeckillActivity{}, id).Error; err != nil {
		return err
	}
	s.clearSeckillRedisKeys(id)
	return nil
}

func seckillRedisKeys(activityID int64) (string, string) {
	return fmt.Sprintf("seckill:stock:%d", activityID), fmt.Sprintf("seckill:user_count:%d", activityID)
}

func seckillCacheTTL(endTime time.Time) time.Duration {
	ttl := time.Until(endTime)
	if ttl <= 0 {
		return 0
	}
	return ttl
}

func (s *SeckillService) refreshSeckillRedisTTL(activity models.SeckillActivity) {
	if activity.Status == models.SeckillStatusEnded || time.Now().After(activity.EndTime) {
		s.clearSeckillRedisKeys(activity.ID)
		return
	}
	if activity.Status != models.SeckillStatusActive {
		return
	}

	cacheTTL := seckillCacheTTL(activity.EndTime)
	if cacheTTL <= 0 {
		s.clearSeckillRedisKeys(activity.ID)
		return
	}

	stockKey, userCountKey := seckillRedisKeys(activity.ID)
	ctx := context.Background()
	s.rdb.Expire(ctx, stockKey, cacheTTL)
	s.rdb.Expire(ctx, userCountKey, cacheTTL)
}

func (s *SeckillService) clearSeckillRedisKeys(activityID int64) {
	stockKey, userCountKey := seckillRedisKeys(activityID)
	ctx := context.Background()
	s.rdb.Del(ctx, stockKey, userCountKey)

	iter := s.rdb.Scan(ctx, 0, fmt.Sprintf("seckill:token:%d:*", activityID), 0).Iterator()
	var tokenKeys []string
	for iter.Next(ctx) {
		tokenKeys = append(tokenKeys, iter.Val())
	}
	if len(tokenKeys) > 0 {
		s.rdb.Del(ctx, tokenKeys...)
	}
}
