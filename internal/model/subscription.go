package model

import "time"

// SubscriptionGroup 订阅分组：按筛选规则聚合 client，生成对外订阅链接
type SubscriptionGroup struct {
	ID      uint      `gorm:"primaryKey"`
	Name    string    `gorm:"size:128"`
	Token   string    `gorm:"size:128"` // /sub/{token} 访问令牌（明文存，仅订阅用）
	Format  string    `gorm:"size:16"`  // vmess | clash | sip008
	Filters string    `gorm:"type:text"` // JSON: {"node_ids":[],"protocol":[],"tags":[]}
	Enabled bool      `gorm:"default:true"`
	CreatedAt time.Time
}

// Setting 简单键值配置（如 default_port_range）
type Setting struct {
	Key   string `gorm:"primaryKey"`
	Value string `gorm:"type:text"`
}
