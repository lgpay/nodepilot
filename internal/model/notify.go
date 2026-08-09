package model

import "time"

// NotificationChannel 预警通知渠道：邮件(SMTP) / 企业微信自建应用 / Telegram Bot。
// Config 为渠道专属 JSON（见 internal/notify 中各 Sender 的解析结构）。
type NotificationChannel struct {
	ID      uint      `gorm:"primaryKey" json:"id"`
	Type    string    `gorm:"size:16;index" json:"type"` // email | wecom | tg
	Name    string    `gorm:"size:64" json:"name"`       // 展示名
	Enabled bool      `gorm:"default:true" json:"enabled"`
	Config  string    `gorm:"type:text" json:"config"` // 渠道专属 JSON
	CreatedAt time.Time `json:"created_at"`
}
