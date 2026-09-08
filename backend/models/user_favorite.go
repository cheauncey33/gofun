package models

const (
	FavoriteTargetEvent    = "event"
	FavoriteTargetRushSale = "rush_sale"
)

type UserFavorite struct {
	Base
	UserID     int64  `gorm:"not null;uniqueIndex:uk_user_favorite,priority:1" json:"user_id,string"`
	TargetType string `gorm:"size:16;not null;uniqueIndex:uk_user_favorite,priority:2" json:"target_type"`
	TargetID   int64  `gorm:"not null;uniqueIndex:uk_user_favorite,priority:3" json:"target_id,string"`
}

func (UserFavorite) TableName() string {
	return "user_favorite"
}
