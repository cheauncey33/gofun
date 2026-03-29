package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"

	"golang.org/x/crypto/bcrypt"
)

func RegisterUser(username, password string, dormID int64) error {
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
