package container

import (
	"log"

	"gofun/models"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

const (
	demoAdminPassword     = "admin123"
	demoOrganizerPassword = "organizer123"
	demoUserPassword      = "user123"
	demoOrganizerSlug     = "gofun-demo"
)

func EnsureDemoAccounts(db *gorm.DB) {
	type demoAccount struct {
		username     string
		password     string
		role         string
		balanceCents int64
	}
	accounts := []demoAccount{
		{username: "admin", password: demoAdminPassword, role: "admin", balanceCents: 999900},
		{username: "organizer", password: demoOrganizerPassword, role: "user", balanceCents: 100000},
		{username: "user", password: demoUserPassword, role: "user", balanceCents: 100000},
	}
	for _, item := range accounts {
		user, created, err := createDemoUserIfAbsent(db, item.username, item.password, item.role, item.balanceCents)
		if err != nil {
			log.Printf("准备演示账号 %s 失败: %v", item.username, err)
			continue
		}
		if created {
			log.Printf("演示账号已创建: %s", item.username)
		}
		if item.username == "organizer" {
			if err := ensureDemoOrganizerOwner(db, user.ID); err != nil {
				log.Printf("绑定演示主办方失败: %v", err)
			}
		}
	}
}

func createDemoUserIfAbsent(db *gorm.DB, username, password, role string, balanceCents int64) (*models.User, bool, error) {
	var existing models.User
	if err := db.Where("username = ?", username).Limit(1).Find(&existing).Error; err != nil {
		return nil, false, err
	}
	if existing.ID != 0 {
		return &existing, false, nil
	}
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return nil, false, err
	}
	user := models.User{
		Username:     username,
		Password:     string(hashed),
		Balance:      float64(balanceCents) / 100,
		BalanceCents: balanceCents,
		Role:         role,
	}
	if err := db.Create(&user).Error; err != nil {
		return nil, false, err
	}
	return &user, true, nil
}

func ensureDemoOrganizerOwner(db *gorm.DB, userID int64) error {
	var organizer models.Organizer
	if err := db.Where("slug = ?", demoOrganizerSlug).Limit(1).Find(&organizer).Error; err != nil {
		return err
	}
	if organizer.ID == 0 {
		return nil
	}
	member := models.OrganizerMember{
		OrganizerID: organizer.ID,
		UserID:      userID,
		Role:        models.OrganizerRoleOwner,
		Status:      models.OrganizerStatusActive,
	}
	var existing models.OrganizerMember
	if err := db.Unscoped().Where("organizer_id = ? AND user_id = ?", organizer.ID, userID).Limit(1).Find(&existing).Error; err != nil {
		return err
	}
	if existing.ID == 0 {
		return db.Create(&member).Error
	}
	return db.Unscoped().Model(&existing).Updates(map[string]any{
		"role":        models.OrganizerRoleOwner,
		"status":      models.OrganizerStatusActive,
		"delete_time": nil,
	}).Error
}
