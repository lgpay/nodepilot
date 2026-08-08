package model

import "time"

// Admin 单管理员账号（MVP 不实现多账号/角色）
type Admin struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	Username     string `gorm:"uniqueIndex;size:64" json:"username"`
	PasswordHash string `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

// Node 代理节点（数据面）：运行 node-agent + xray
type Node struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Name          string    `gorm:"size:128" json:"name"`
	Address       string    `gorm:"size:128" json:"address"` // agent 监听地址，如 127.0.0.1:54321
	Region        string    `gorm:"size:64" json:"region"`
	Tags          string    `gorm:"size:512" json:"tags"` // 逗号分隔
	Token         string    `gorm:"size:128" json:"-"` // 节点 token 明文（仅下发/校验用，列表接口不返回）
	Enabled       bool      `gorm:"default:true" json:"enabled"`
	Status        string    `gorm:"size:16;default:'offline'" json:"status"` // online|offline
	Connectivity  string    `gorm:"size:16;default:'ok'" json:"connectivity"` // ok|degraded|offline
	AgentVersion  string    `gorm:"size:32" json:"agent_version"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	PortRange     string    `gorm:"size:64" json:"port_range"` // 如 10000-65535 或 10000-20000,30000-40000；空=全局默认
	CreatedAt     time.Time `json:"created_at"`
}

// Inbound 入站（协议配置）
type Inbound struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	NodeID         uint   `gorm:"index" json:"node_id"`
	Protocol       string `gorm:"size:16" json:"protocol"` // vmess|vless|trojan|ss|socks|http
	Port           int    `json:"port"`
	Transport      string `gorm:"size:16" json:"transport"` // tcp|ws|grpc
	TLSCertID      uint   `json:"tls_cert_id"`
	TLSEnabled     bool   `gorm:"default:false" json:"tls_enabled"`
	StreamSettings string `gorm:"type:text" json:"stream_settings"` // JSON（高级 streamSettings 覆盖）
	Fallback       string `gorm:"type:text" json:"fallback"` // JSON
	Enabled        bool   `gorm:"default:true" json:"enabled"`
	PortAutoFixed  bool   `gorm:"default:false" json:"port_auto_fixed"`
}

// Client 代理用户（vmess 等客户端账号）
type Client struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	InboundID         uint      `gorm:"index" json:"inbound_id"`
	UUID              string    `gorm:"size:64" json:"uuid"`
	Email             string    `gorm:"size:128" json:"email"`
	TrafficLimitBytes int64     `gorm:"default:-1" json:"traffic_limit_bytes"` // -1 表示不限
	ExpireTime        time.Time `json:"expire_time"`
	Enabled           bool      `gorm:"default:true" json:"enabled"`
	CreatedAt         time.Time `json:"created_at"`
}

// ConfigVersion 下发配置版本（用于回滚与状态追踪）
type ConfigVersion struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	NodeID      uint      `gorm:"index" json:"node_id"`
	Version     int       `json:"version"`
	ContentJSON string    `gorm:"type:text" json:"content_json"`
	Status      string    `gorm:"size:16" json:"status"` // applied|failed
	AppliedAt   time.Time `json:"applied_at"`
	Error       string    `gorm:"type:text" json:"error"`
}
