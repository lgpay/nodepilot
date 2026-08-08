package model

import "time"

// Admin 单管理员账号（MVP 不实现多账号/角色）
type Admin struct {
	ID           uint   `gorm:"primaryKey"`
	Username     string `gorm:"uniqueIndex;size:64"`
	PasswordHash string
	CreatedAt    time.Time
}

// Node 代理节点（数据面）：运行 node-agent + xray
type Node struct {
	ID            uint      `gorm:"primaryKey"`
	Name          string    `gorm:"size:128"`
	Address       string    `gorm:"size:128"` // agent 监听地址，如 127.0.0.1:54321
	Region        string    `gorm:"size:64"`
	Tags          string    `gorm:"size:512"` // 逗号分隔
	Token         string    `gorm:"size:128"` // 节点 token 明文（仅下发/校验用，列表接口不返回）
	Enabled       bool      `gorm:"default:true"`
	Status        string    `gorm:"size:16;default:'offline'"` // online|offline
	Connectivity  string    `gorm:"size:16;default:'ok'"`      // ok|degraded|offline
	AgentVersion  string    `gorm:"size:32"`
	LastHeartbeat time.Time
	PortRange     string `gorm:"size:64"` // 如 10000-65535 或 10000-20000,30000-40000；空=全局默认
	CreatedAt     time.Time
}

// Inbound 入站（协议配置）
type Inbound struct {
	ID             uint   `gorm:"primaryKey"`
	NodeID         uint   `gorm:"index"`
	Protocol       string `gorm:"size:16"` // vmess|vless|trojan|ss|socks|http
	Port           int
	Transport      string `gorm:"size:16"` // tcp|ws|grpc
	TLSCertID      uint
	TLSEnabled     bool `gorm:"default:false"`
	StreamSettings string `gorm:"type:text"` // JSON（高级 streamSettings 覆盖）
	Fallback       string `gorm:"type:text"` // JSON
	Enabled        bool   `gorm:"default:true"`
	PortAutoFixed  bool   `gorm:"default:false"`
}

// Client 代理用户（vmess 等客户端账号）
type Client struct {
	ID                uint      `gorm:"primaryKey"`
	InboundID         uint      `gorm:"index"`
	UUID              string    `gorm:"size:64"`
	Email             string    `gorm:"size:128"`
	TrafficLimitBytes int64     `gorm:"default:-1"` // -1 表示不限
	ExpireTime        time.Time
	Enabled           bool `gorm:"default:true"`
	CreatedAt         time.Time
}

// ConfigVersion 下发配置版本（用于回滚与状态追踪）
type ConfigVersion struct {
	ID          uint      `gorm:"primaryKey"`
	NodeID      uint      `gorm:"index"`
	Version     int
	ContentJSON string `gorm:"type:text"`
	Status      string `gorm:"size:16"` // applied|failed
	AppliedAt   time.Time
	Error       string `gorm:"type:text"`
}
