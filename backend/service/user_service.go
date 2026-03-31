package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// 用户提交的form表单，在user数据库中插入记录。
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

// 用户提交form表单，查询是否存在，存在则跳转
func Login(username, password string) (string, error) {
	var user models.User
	if err := common.DB.Where("username =? ", username).First(&user).Error; err != nil {
		return "", fmt.Errorf("用户不存在")
	}
	err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(password))
	if err != nil {
		return "", fmt.Errorf("密码错误")
	}
	return common.GenerateToken(user.ID)
}
func GetUserInfo(userID int64) (models.User, error) {
	var user models.User
	err := common.DB.Debug().Select("id", "username", "balance", "dorm_id").Where("id=?", userID).First(&user).Error
	return user, err
}
