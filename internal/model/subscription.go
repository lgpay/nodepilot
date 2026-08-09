package model

import "time"

// SubscriptionGroup 订阅分组：按筛选规则聚合 client，生成对外订阅链接
type SubscriptionGroup struct {
	ID      uint      `gorm:"primaryKey" json:"id"`
	Name    string    `gorm:"size:128" json:"name"`
	Token   string    `gorm:"size:128" json:"token"` // /sub/{token} 访问令牌（明文存，仅订阅用）
	Format  string    `gorm:"size:16" json:"format"`  // vmess | clash | surfboard | loon | sip008
	Mode    string    `gorm:"size:16;default:'acl4ssr_online'" json:"mode"` // none(裸订阅) | acl4ssr_online(ACL4SSR_Online)
	Filters string    `gorm:"type:text" json:"filters"` // JSON: {"node_ids":[],"protocol":[],"tags":[]}
	Enabled bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// Setting 简单键值配置（如 default_port_range）
type Setting struct {
	Key   string `gorm:"primaryKey" json:"key"`
	Value string `gorm:"type:text" json:"value"`
}
