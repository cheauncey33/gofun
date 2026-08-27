package models

import "time"

const (
	FunnelStageBrowse   = "browse"
	FunnelStageDetail   = "detail"
	FunnelStageCheckout = "checkout"
)

// FunnelDaily 按活动、自然日、阶段汇总前台访问。
// hits 是曝光次数，uniques 是同一访客当天去重后的人数。
type FunnelDaily struct {
	EventID     int64     `gorm:"primaryKey;not null" json:"event_id,string"`
	OrganizerID int64     `gorm:"not null;index:idx_funnel_org_day,priority:1" json:"organizer_id,string"`
	Day         time.Time `gorm:"primaryKey;type:date" json:"day"`
	Stage       string    `gorm:"primaryKey;size:16" json:"stage"`
	Hits        int64     `gorm:"not null;default:0" json:"hits"`
	Uniques     int64     `gorm:"not null;default:0" json:"uniques"`
}

func (FunnelDaily) TableName() string {
	return "funnel_daily"
}

// FunnelOrderDaily 按活动、自然日、购票渠道汇总提交/支付/退款。
// 漏斗读路径只扫这张日表，避免按时间窗扫描 ticket_order。
type FunnelOrderDaily struct {
	EventID     int64     `gorm:"primaryKey;not null" json:"event_id,string"`
	OrganizerID int64     `gorm:"not null;index:idx_funnel_order_org_day,priority:1" json:"organizer_id,string"`
	Day         time.Time `gorm:"primaryKey;type:date" json:"day"`
	Source      string    `gorm:"primaryKey;size:16" json:"source"`
	Submitted   int64     `gorm:"not null;default:0" json:"submitted"`
	Paid        int64     `gorm:"not null;default:0" json:"paid"`
	Refunded    int64     `gorm:"not null;default:0" json:"refunded"`
}

func (FunnelOrderDaily) TableName() string {
	return "funnel_order_daily"
}
