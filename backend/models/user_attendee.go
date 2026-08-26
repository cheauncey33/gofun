package models

// UserAttendee 是账号绑定的观演人档案。证件明文加密存放，接口只回掩码。
type UserAttendee struct {
	Base
	UserID         int64  `gorm:"not null;uniqueIndex:uk_user_attendee_identity,priority:1" json:"user_id,string"`
	Name           string `gorm:"size:64;not null" json:"name"`
	IDType         string `gorm:"size:16;not null;default:'id_card'" json:"id_type"`
	IDNumberMasked string `gorm:"size:32;not null" json:"id_number_masked"`
	IDNumberCipher string `gorm:"size:512;not null" json:"-"`
	IdentityKey    string `gorm:"size:64;not null;uniqueIndex:uk_user_attendee_identity,priority:2" json:"-"`
}

func (UserAttendee) TableName() string {
	return "user_attendee"
}
