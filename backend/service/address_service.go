package service

import (
	"WHU_Snack_GO/common"
	"WHU_Snack_GO/models"
	"fmt"

	"gorm.io/gorm"
)

func CreateAddress(userID int64, addr *models.Address) error {
	addr.UserID = userID

	// 检查用户地址数量
	var count int64
	common.DB.Model(&models.Address{}).Where("user_id = ?", userID).Count(&count)
	if count == 0 {
		addr.IsDefault = true
	}

	return common.DB.Create(addr).Error
}

func UpdateAddress(userID, addressID int64, addr *models.Address) error {
	var existing models.Address
	if err := common.DB.Where("id = ? AND user_id = ?", addressID, userID).First(&existing).Error; err != nil {
		return fmt.Errorf("地址不存在")
	}

	updates := map[string]interface{}{
		"receiver_name": addr.ReceiverName,
		"phone":         addr.Phone,
		"province":      addr.Province,
		"city":          addr.City,
		"district":      addr.District,
		"detail":        addr.Detail,
	}
	return common.DB.Model(&existing).Updates(updates).Error
}

func DeleteAddress(userID, addressID int64) error {
	var addr models.Address
	if err := common.DB.Where("id = ? AND user_id = ?", addressID, userID).First(&addr).Error; err != nil {
		return fmt.Errorf("地址不存在")
	}

	if err := common.DB.Delete(&addr).Error; err != nil {
		return err
	}

	// 如果删除的是默认地址，设置另一个为默认
	if addr.IsDefault {
		var next models.Address
		if err := common.DB.Where("user_id = ?", userID).First(&next).Error; err == nil {
			common.DB.Model(&next).Update("is_default", true)
		}
	}
	return nil
}

func ListAddresses(userID int64) ([]models.Address, error) {
	var addresses []models.Address
	err := common.DB.Where("user_id = ?", userID).Order("is_default DESC, create_time DESC").Find(&addresses).Error
	return addresses, err
}

func SetDefaultAddress(userID, addressID int64) error {
	var addr models.Address
	if err := common.DB.Where("id = ? AND user_id = ?", addressID, userID).First(&addr).Error; err != nil {
		return fmt.Errorf("地址不存在")
	}

	return common.DB.Transaction(func(tx *gorm.DB) error {
		// 清除旧默认
		tx.Model(&models.Address{}).Where("user_id = ? AND is_default = ?", userID, true).Update("is_default", false)
		// 设置新默认
		return tx.Model(&addr).Update("is_default", true).Error
	})
}
