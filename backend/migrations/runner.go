package migrations

import (
	"embed"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"
)

//go:embed *.sql
var files embed.FS

type SchemaMigration struct {
	Version   string    `gorm:"primaryKey;size:64"`
	AppliedAt time.Time `gorm:"not null"`
}

func (SchemaMigration) TableName() string {
	return "schema_migration"
}

var baselineTables = []string{
	"dormitory",
	"user",
	"organizer",
	"organizer_member",
	"venue",
	"event",
	"event_session",
	"ticket_tier",
	"ticket_order",
	"ticket_order_item",
	"rush_sale_campaign",
}

// Run 只执行尚未记录的迁移。对旧数据库，V1 会先确认全部基线表存在再登记，
// 不会重新创建或覆盖现有数据；部分基线会直接报错，避免把残缺结构误标为完成。
func Run(db *gorm.DB) error {
	if err := db.AutoMigrate(&SchemaMigration{}); err != nil {
		return fmt.Errorf("创建迁移记录表: %w", err)
	}

	entries, err := files.ReadDir(".")
	if err != nil {
		return fmt.Errorf("读取迁移目录: %w", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var count int64
		if err := db.Model(&SchemaMigration{}).Where("version = ?", name).Count(&count).Error; err != nil {
			return err
		}
		if count > 0 {
			continue
		}
		if name == "001_ticketing_baseline.sql" {
			handled, err := baselineExistingDatabase(db, name)
			if err != nil {
				return err
			}
			if handled {
				continue
			}
		}
		body, err := files.ReadFile(name)
		if err != nil {
			return fmt.Errorf("读取迁移 %s: %w", name, err)
		}
		if err := executeStatements(db, string(body)); err != nil {
			return fmt.Errorf("执行迁移 %s: %w", name, err)
		}
		if err := db.Create(&SchemaMigration{
			Version:   name,
			AppliedAt: time.Now(),
		}).Error; err != nil {
			return fmt.Errorf("登记迁移 %s: %w", name, err)
		}
	}
	return nil
}

func baselineExistingDatabase(db *gorm.DB, version string) (bool, error) {
	present := 0
	for _, table := range baselineTables {
		if db.Migrator().HasTable(table) {
			present++
		}
	}
	if present == 0 {
		return false, nil
	}
	if present != len(baselineTables) {
		return false, fmt.Errorf(
			"票务数据库基线不完整: 找到 %d/%d 张表，请先修复结构再登记基线",
			present,
			len(baselineTables),
		)
	}
	return true, db.Create(&SchemaMigration{
		Version:   version,
		AppliedAt: time.Now(),
	}).Error
}

func executeStatements(db *gorm.DB, body string) error {
	for _, statement := range strings.Split(body, "-- statement") {
		statement = strings.TrimSpace(statement)
		if statement == "" {
			continue
		}
		if err := db.Exec(statement).Error; err != nil {
			return err
		}
	}
	return nil
}
