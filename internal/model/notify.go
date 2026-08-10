package model

import (
	"encoding/json"
	"strings"
	"time"
)

// NotificationChannel 预警通知渠道：邮件(SMTP) / 企业微信自建应用 / Telegram Bot。
// Config 为渠道专属 JSON（见 internal/notify 中各 Sender 的解析结构）。
// Events 为该渠道订阅的通知事件 key（JSON 数组字符串），空 = 订阅全部。
type NotificationChannel struct {
	ID      uint      `gorm:"primaryKey" json:"id"`
	Type    string    `gorm:"size:16;index" json:"type"` // email | wecom | tg
	Name    string    `gorm:"size:64" json:"name"`       // 展示名
	Enabled bool      `gorm:"default:true" json:"enabled"`
	Config  string    `gorm:"type:text" json:"config"` // 渠道专属 JSON
	Events  string    `gorm:"type:text" json:"events"` // 订阅的通知事件(JSON 数组)；空=全部
	CreatedAt time.Time `json:"created_at"`
}

// Subscribes 该渠道是否订阅了 event。Events 为空或解析失败时视为订阅全部（向后兼容）。
func (ch NotificationChannel) Subscribes(event string) bool {
	ev := strings.TrimSpace(ch.Events)
	if ev == "" {
		return true
	}
	var list []string
	if err := json.Unmarshal([]byte(ev), &list); err != nil {
		return true
	}
	for _, e := range list {
		if e == event {
			return true
		}
	}
	return false
}
