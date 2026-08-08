package store

import (
	"log"
	"time"

	"nodepilot/internal/auth"
	"nodepilot/internal/model"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// DB 全局数据库连接（MVP 单管理端 SQLite）
var DB *gorm.DB

// Init 打开 SQLite 并自动建表
func Init(dbPath string) error {
	db, err := gorm.Open(sqlite.Open(dbPath), &gorm.Config{})
	if err != nil {
		return err
	}
	DB = db
	return db.AutoMigrate(
		&model.Admin{},
		&model.Node{},
		&model.Inbound{},
		&model.Client{},
		&model.ConfigVersion{},
	)
}

// InitAdmin 首次启动初始化管理员（表为空时写入默认账号）
func InitAdmin(username, password string) error {
	var c int64
	if err := DB.Model(&model.Admin{}).Count(&c).Error; err != nil {
		return err
	}
	if c > 0 {
		return nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	if err := DB.Create(&model.Admin{Username: username, PasswordHash: hash}).Error; err != nil {
		return err
	}
	log.Printf("[store] initialized admin '%s' (default password, please change)", username)
	return nil
}

// Now 便于统一时间来源
func Now() time.Time { return time.Now() }
