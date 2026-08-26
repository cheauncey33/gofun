package service

import (
	"fmt"
	"gofun/models"
	"strings"
	"unicode/utf8"
)

const maxUserAttendees = 8

func (s *UserService) ListAttendees(userID int64) ([]models.UserAttendee, error) {
	var rows []models.UserAttendee
	err := s.db.Where("user_id = ?", userID).Order("id ASC").Find(&rows).Error
	return rows, err
}

type SaveAttendeeReq struct {
	Name     string `json:"name"`
	IDType   string `json:"id_type"`
	IDNumber string `json:"id_number"`
}

func (s *UserService) CreateAttendee(userID int64, req SaveAttendeeReq) (*models.UserAttendee, error) {
	name, idNumber, err := normalizeAttendeeInput(req)
	if err != nil {
		return nil, err
	}
	var count int64
	if err := s.db.Model(&models.UserAttendee{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count >= maxUserAttendees {
		return nil, fmt.Errorf("最多绑定 %d 位观演人", maxUserAttendees)
	}
	key := stableIdentityKey(s.identityKey, idNumber)
	cipher, err := encryptIdentity(s.identityKey, idNumber)
	if err != nil {
		return nil, err
	}
	row := models.UserAttendee{
		UserID:         userID,
		Name:           name,
		IDType:         "id_card",
		IDNumberMasked: maskIDNumber(idNumber),
		IDNumberCipher: cipher,
		IdentityKey:    key,
	}
	if err := s.db.Create(&row).Error; err != nil {
		if isDuplicateStorageKeyError(err) {
			return nil, fmt.Errorf("该证件已绑定")
		}
		return nil, err
	}
	return &row, nil
}

func (s *UserService) DeleteAttendee(userID, attendeeID int64) error {
	result := s.db.Where("id = ? AND user_id = ?", attendeeID, userID).Delete(&models.UserAttendee{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("观演人不存在")
	}
	return nil
}

func normalizeAttendeeInput(req SaveAttendeeReq) (string, string, error) {
	name := strings.TrimSpace(req.Name)
	idNumber := strings.ToUpper(strings.TrimSpace(req.IDNumber))
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 64 {
		return "", "", fmt.Errorf("姓名应为 2 到 64 个字符")
	}
	idType := strings.TrimSpace(req.IDType)
	if idType == "" {
		idType = "id_card"
	}
	if idType != "id_card" || !validMainlandIDCard(idNumber) {
		return "", "", fmt.Errorf("身份证格式不正确")
	}
	return name, idNumber, nil
}
