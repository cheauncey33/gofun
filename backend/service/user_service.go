package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/config"
	"WHU_Snack_GO/models"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func Register(username, password string, dormID int64) error {
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return err
	}
	user := models.User{
		Username: username,
		Password: string(hashedPassword),
		Balance:  100.0,
		DormID:   dormID,
	}
	return common.DB.Create(&user).Error
}

func Login(username, password string) (string, error) {
	var user models.User
	if err := common.DB.Where("username = ?", username).First(&user).Error; err != nil {
		return "", fmt.Errorf("用户不存在")
	}
	err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		return "", fmt.Errorf("密码错误")
	}

	// 更新最后登录时间
	now := time.Now()
	common.DB.Model(&user).Update("last_login_at", &now)

	cfg := config.GlobalConfig
		return common.GenerateToken(user.ID, cfg.JWT.ExpireSecs)
}

func GetUserInfo(userID int64) (models.User, error) {
	var user models.User
	err := common.DB.Select("id", "username", "balance", "dorm_id", "phone", "avatar_url", "role", "last_login_at", "create_time").
		Where("id = ?", userID).First(&user).Error
	return user, err
}

type UpdateUserInfoReq struct {
	Phone     string `json:"phone" binding:"omitempty,phone"`
	AvatarURL string `json:"avatar_url"`
}

func UpdateUserInfo(userID int64, req UpdateUserInfoReq) error {
	updates := map[string]interface{}{}
	if req.Phone != "" {
		// 检查手机号是否已被使用
		var exist models.User
		if err := common.DB.Where("phone = ? AND id != ?", req.Phone, userID).First(&exist).Error; err == nil {
			return fmt.Errorf("手机号已被使用")
		}
		updates["phone"] = req.Phone
	}
	if req.AvatarURL != "" {
		updates["avatar_url"] = req.AvatarURL
	}
	if len(updates) == 0 {
		return nil
	}
	return common.DB.Model(&models.User{}).Where("id = ?", userID).Updates(updates).Error
}

type ChangePasswordReq struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,password"`
}

func ChangePassword(userID int64, req ChangePasswordReq) error {
	var user models.User
	if err := common.DB.Where("id = ?", userID).First(&user).Error; err != nil {
		return fmt.Errorf("用户不存在")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)); err != nil {
		return fmt.Errorf("原密码错误")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), 12)
	if err != nil {
		return err
	}

	return common.DB.Model(&user).Update("password", string(hashedPassword)).Error
}
