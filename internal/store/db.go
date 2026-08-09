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
	if err := db.AutoMigrate(
		&model.Admin{},
		&model.Node{},
		&model.Inbound{},
		&model.Client{},
		&model.ConfigVersion{},
		&model.Certificate{},
		&model.SubscriptionGroup{},
		&model.Setting{},
		&model.TrafficStat{},
		&model.NotificationChannel{},
	); err != nil {
		return err
	}
	// 默认端口范围
	if _, err := GetSetting("default_port_range"); err != nil {
		_ = SetSetting("default_port_range", "10000-65535")
	}
	return nil
}

// GetSetting 读取配置项，未找到返回错误
func GetSetting(key string) (string, error) {
	var s model.Setting
	if err := DB.First(&s, "key = ?", key).Error; err != nil {
		return "", err
	}
	return s.Value, nil
}

// SetSetting 写入/更新配置项
func SetSetting(key, value string) error {
	var s model.Setting
	err := DB.First(&s, "key = ?", key).Error
	if err != nil {
		s.Key = key
		s.Value = value
		return DB.Create(&s).Error
	}
	s.Value = value
	return DB.Model(&s).Update("value", value).Error
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
