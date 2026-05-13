package models

type Category struct {
	Base
	Name     string    `gorm:"size:64;not null;uniqueIndex" json:"name"`
	ParentID *int64    `gorm:"index" json:"parent_id"`
	Sort     int       `gorm:"default:0" json:"sort"`
	Products []Product `gorm:"foreignKey:CategoryID" json:"products,omitempty"`
}
