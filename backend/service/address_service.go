package service

import (
	"WHU_Snack_GO/container"
	"WHU_Snack_GO/models"
	"fmt"

	"gorm.io/gorm"
)

type AddressService struct {
	db *gorm.DB
}

func NewAddressService(c *container.Container) *AddressService {
	return &AddressService{db: c.DB}
}

func (s *AddressService) CreateAddress(userID int64, addr *models.Address) error {
	addr.UserID = userID

	var count int64
	s.db.Model(&models.Address{}).Where("user_id = ?", userID).Count(&count)
	if count == 0 {
		addr.IsDefault = true
	}

	return s.db.Create(addr).Error
}

func (s *AddressService) UpdateAddress(userID, addressID int64, addr *models.Address) error {
	var existing models.Address
	if err := s.db.Where("id = ? AND user_id = ?", addressID, userID).First(&existing).Error; err != nil {
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
	return s.db.Model(&existing).Updates(updates).Error
}

func (s *AddressService) DeleteAddress(userID, addressID int64) error {
	var addr models.Address
	if err := s.db.Where("id = ? AND user_id = ?", addressID, userID).First(&addr).Error; err != nil {
		return fmt.Errorf("地址不存在")
	}

	if err := s.db.Delete(&addr).Error; err != nil {
		return err
	}

	if addr.IsDefault {
		var next models.Address
		if err := s.db.Where("user_id = ?", userID).First(&next).Error; err == nil {
			s.db.Model(&next).Update("is_default", true)
		}
	}
	return nil
}

func (s *AddressService) ListAddresses(userID int64) ([]models.Address, error) {
	var addresses []models.Address
	err := s.db.Where("user_id = ?", userID).Order("is_default DESC, create_time DESC").Find(&addresses).Error
	return addresses, err
}

func (s *AddressService) SetDefaultAddress(userID, addressID int64) error {
	var addr models.Address
	if err := s.db.Where("id = ? AND user_id = ?", addressID, userID).First(&addr).Error; err != nil {
		return fmt.Errorf("地址不存在")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		tx.Model(&models.Address{}).Where("user_id = ? AND is_default = ?", userID, true).Update("is_default", false)
		return tx.Model(&addr).Update("is_default", true).Error
	})
}
