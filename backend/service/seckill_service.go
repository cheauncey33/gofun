package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

var seckillDeductLua = `
local stockKey = KEYS[1]
local userKey = KEYS[2]
local tokenKey = KEYS[3]
local userID = ARGV[1]
local limit = tonumber(ARGV[2])
local quantity = tonumber(ARGV[3])
local expectedToken = ARGV[4]

if quantity == nil or quantity <= 0 then
    return -4
end

-- 验证token: 必须存在且值匹配
local storedToken = redis.call('GET', tokenKey)
if storedToken == false or storedToken ~= expectedToken then
    return -3
end
redis.call('DEL', tokenKey)

-- 检查库存
local stock = tonumber(redis.call('GET', stockKey)) or 0
if stock < quantity then
    return -1
end

-- 检查用户限购
local count = tonumber(redis.call('HGET', userKey, userID)) or 0
if count + quantity > limit then
    return -2
end

-- 扣库存 + 累加购买数
redis.call('DECRBY', stockKey, quantity)
redis.call('HINCRBY', userKey, userID, quantity)
return 1
`

// GetSeckillActivityList 获取秒杀活动列表
func GetSeckillActivityList(page, pageSize int) ([]models.SeckillActivity, int64, error) {
	var activities []models.SeckillActivity
	var total int64

	query := common.DB.Model(&models.SeckillActivity{}).Preload("Product")
	query.Count(&total)

	offset := (page - 1) * pageSize
	err := query.Order("start_time DESC").Limit(pageSize).Offset(offset).Find(&activities).Error
	return activities, total, err
}

// GetSeckillActivityDetail 秒杀活动详情
func GetSeckillActivityDetail(activityID int64) (*models.SeckillActivity, error) {
	var activity models.SeckillActivity
	err := common.DB.Preload("Product").First(&activity, activityID).Error
	return &activity, err
}

// WarmUpSeckillActivity 预热秒杀库存到Redis
func WarmUpSeckillActivity(activityID int64) error {
	var activity models.SeckillActivity
	if err := common.DB.First(&activity, activityID).Error; err != nil {
		return err
	}

	now := time.Now()
	if now.Before(activity.StartTime) || now.After(activity.EndTime) {
		return fmt.Errorf("活动不在有效时间范围内")
	}

	stockKey := fmt.Sprintf("seckill:stock:%d", activityID)
	common.RDB.Set(common.Ctx, stockKey, activity.Stock, 0)

	common.DB.Model(&activity).Updates(map[string]interface{}{
		"status":          models.SeckillStatusActive,
		"remaining_stock": activity.Stock,
	})

	return nil
}

// GenerateSeckillToken 为用户生成一次性秒杀令牌
func GenerateSeckillToken(activityID, userID int64) (string, error) {
	var activity models.SeckillActivity
	if err := common.DB.First(&activity, activityID).Error; err != nil {
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

	// 检查用户是否已达限购上限
	userCountKey := fmt.Sprintf("seckill:user_count:%d", activityID)
	countStr, _ := common.RDB.HGet(common.Ctx, userCountKey, strconv.FormatInt(userID, 10)).Result()
	if countStr != "" {
		count, _ := strconv.Atoi(countStr)
		if count >= activity.LimitPerUser {
			return "", fmt.Errorf("已达限购上限，已购买%d件", count)
		}
	}

	// 检查库存是否已售罄
	stockKey := fmt.Sprintf("seckill:stock:%d", activityID)
	stockStr, _ := common.RDB.Get(common.Ctx, stockKey).Result()
	if stockStr != "" {
		stock, _ := strconv.Atoi(stockStr)
		if stock <= 0 {
			return "", fmt.Errorf("秒杀已售罄")
		}
	}

	// 生成 HMAC token
	ts := time.Now().Unix()
	payload := fmt.Sprintf("%d:%d:%d", activityID, userID, ts)
	mac := hmac.New(sha256.New, common.JWTSecret)
	mac.Write([]byte(payload))
	token := hex.EncodeToString(mac.Sum(nil))

	// 存 Redis，60 秒有效期
	tokenKey := fmt.Sprintf("seckill:token:%d:%d", activityID, userID)
	common.RDB.Set(common.Ctx, tokenKey, token, 60*time.Second)

	return token, nil
}

// ExecuteSeckill 执行秒杀
func ExecuteSeckill(activityID, userID int64, token string, quantity int) error {
	if quantity <= 0 {
		return fmt.Errorf("购买数量必须大于0")
	}

	stockKey := fmt.Sprintf("seckill:stock:%d", activityID)
	userKey := fmt.Sprintf("seckill:user_count:%d", activityID)
	tokenKey := fmt.Sprintf("seckill:token:%d:%d", activityID, userID)

	var activity models.SeckillActivity
	if err := common.DB.First(&activity, activityID).Error; err != nil {
		return fmt.Errorf("活动不存在")
	}

	result, err := common.RDB.Eval(common.Ctx, seckillDeductLua,
		[]string{stockKey, userKey, tokenKey},
		userID, activity.LimitPerUser, quantity, token,
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
		// 生成订单
		orderID := common.Node.Generate().Int64()
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
		err = common.PublishPersistent(context.Background(), body)
		if err != nil {
			// 回滚Redis
			common.RDB.IncrBy(common.Ctx, stockKey, int64(quantity))
			common.RDB.HIncrBy(common.Ctx, userKey, strconv.FormatInt(userID, 10), int64(-quantity))
			return fmt.Errorf("订单排队失败")
		}

		// 更新剩余库存
		stockStr, _ := common.RDB.Get(common.Ctx, stockKey).Result()
		remaining, _ := strconv.Atoi(stockStr)
		common.DB.Model(&activity).Update("remaining_stock", remaining)

		return nil
	}

	return fmt.Errorf("未知错误")
}

// Admin functions

func CreateSeckillActivity(activity *models.SeckillActivity) error {
	return common.DB.Create(activity).Error
}

func UpdateSeckillActivity(id int64, activity *models.SeckillActivity) error {
	return common.DB.Model(&models.SeckillActivity{}).Where("id = ?", id).Updates(activity).Error
}

func DeleteSeckillActivity(id int64) error {
	return common.DB.Delete(&models.SeckillActivity{}, id).Error
}
