package models

// EventComment 活动讨论区评论（登录即可发，不要求已购票）。
type EventComment struct {
	Base
	EventID   int64  `gorm:"not null;index;index:idx_event_comment_event_created,priority:1" json:"event_id,string"`
	UserID    int64  `gorm:"not null;index" json:"user_id,string"`
	Content   string `gorm:"size:500;not null" json:"content"`
	LikeCount int64  `gorm:"not null;default:0" json:"like_count"`
	User      *User  `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (EventComment) TableName() string {
	return "event_comment"
}
