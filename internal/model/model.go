package model

import "time"

// Admin 单管理员账号（MVP 不实现多账号/角色）
type Admin struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Username      string    `gorm:"uniqueIndex;size:64" json:"username"`
	PasswordHash  string    `json:"-"`
	MustChangePwd bool      `gorm:"default:true" json:"must_change_pwd"` // 首次启动随机密码，要求登录后立即修改
	CreatedAt     time.Time `json:"created_at"`
}

// Node 代理节点（数据面）：运行 node-agent + xray
type Node struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	Name          string    `gorm:"size:128" json:"name"`
	Address       string    `gorm:"size:128" json:"address"` // agent 监听地址，如 127.0.0.1:54321
	Region        string    `gorm:"size:64" json:"region"`   // 国家（中文名或 ISO 码），用于生成旗帜
	City          string    `gorm:"size:64" json:"city"`     // 城市（如 法兰克福），让区域显示更精确
	Flag          string    `json:"flag" gorm:"-"`           // 由 region 派生，不入库
	Tags          string    `gorm:"size:512" json:"tags"` // 逗号分隔
	// 节点 token 不再以明文持久化：
	//   TokenHash：sha256(token) hex，用于校验 agent 上报/下发的 Bearer（常量时间比较）。
	//   TokenEnc ：AES-GCM 加密的明文 token，仅用于管理端主动推送配置/证书到 agent 时解密使用。
	// 明文 token 仅在创建节点时返回一次（部署 agent 用）。
	TokenHash string `gorm:"size:64;index" json:"-"`
	TokenEnc  string `gorm:"type:text" json:"-"`
	Enabled       bool      `gorm:"default:true" json:"enabled"`
	Status        string    `gorm:"size:16;default:'offline'" json:"status"` // online|offline
	Connectivity  string    `gorm:"size:16;default:'ok'" json:"connectivity"` // ok|degraded|offline
	AgentVersion  string    `gorm:"size:32" json:"agent_version"`
	LastHeartbeat time.Time `json:"last_heartbeat"`
	PortRange     string    `gorm:"size:64" json:"port_range"` // 如 10000-65535 或 10000-20000,30000-40000；空=全局默认
	MonthlyTrafficBytes int64 `gorm:"default:0" json:"monthly_traffic_bytes"` // 月流量上限(字节)，0=不限
	ExpiresAt     *time.Time `json:"expires_at"` // 服务器有效期（到期时间），空=长期有效
	CreatedAt     time.Time `json:"created_at"`
}

// Inbound 入站（协议配置）
type Inbound struct {
	ID             uint   `gorm:"primaryKey" json:"id"`
	NodeID         uint   `gorm:"index" json:"node_id"`
	Name           string `gorm:"size:128" json:"name"` // 别名/备注（用于订阅分组精确选择）
	Protocol       string `gorm:"size:16" json:"protocol"` // vmess|vless|trojan|ss|socks|http
	Port           int    `json:"port"`
	Transport      string `gorm:"size:16" json:"transport"` // tcp|ws|grpc
	TLSCertID      uint   `json:"tls_cert_id"`
	TLSEnabled     bool   `gorm:"default:false" json:"tls_enabled"`
	StreamSettings string `gorm:"type:text" json:"stream_settings"` // JSON（高级 streamSettings 覆盖）
	Fallback       string `gorm:"type:text" json:"fallback"` // JSON
	Enabled        bool   `gorm:"default:true" json:"enabled"`
	PortAutoFixed  bool   `gorm:"default:false" json:"port_auto_fixed"` // 当前端口是否由自愈换过（诊断标记）
	AutoHeal       bool   `gorm:"default:true" json:"auto_heal"`       // 端口不通时是否允许自动换端口（已由间隔控制，此字段保留兼容）
	AutoHealInterval int `gorm:"default:10" json:"auto_heal_interval"` // 自动修复最小间隔(分钟)，0=不自动修复
}

// Client 代理用户（vmess 等客户端账号）
type Client struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	InboundID         uint      `gorm:"index" json:"inbound_id"`
	UUID              string    `gorm:"size:64" json:"uuid"`
	Alias             string    `gorm:"size:128" json:"alias"` // 别名/备注（vmess ps），仅展示用；xray 统计键固定用 UUID
	TrafficLimitBytes int64     `gorm:"default:0" json:"traffic_limit_bytes"` // 0 表示不限（历史 -1 亦按不限处理）
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

// Certificate 全局 TLS 证书（管理端签发泛域名，分发到各 agent）
// 证书私钥仅存节点本地；本表仅存 agent 本地路径 + 元数据；CF Token 加密存储。
type Certificate struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Domain     string    `gorm:"size:128" json:"domain"`    // 如 *.rootdomain.com
	CertPath   string    `gorm:"type:text" json:"cert_path"` // agent 本地 fullchain.pem
	KeyPath    string    `gorm:"type:text" json:"key_path"`  // agent 本地 privkey.pem
	CAPath     string    `gorm:"type:text" json:"ca_path"`   // agent 本地 ca.pem
	AutoRenew  bool      `gorm:"default:true" json:"auto_renew"`
	ExpiresAt  time.Time `json:"expires_at"`
	CFEmail    string    `gorm:"size:128" json:"cf_email"`
	CFTokenEnc string    `gorm:"type:text" json:"-"` // AES-GCM 密文，不序列化
	Status     string    `gorm:"size:16;default:'pending'" json:"status"` // pending|issued|failed
	LastError  string    `gorm:"type:text" json:"last_error"`
	CreatedAt  time.Time `json:"created_at"`
}

// TrafficStat 按天流量统计（节点 / 入站 / 客户端维度）
// client_id 为 0 表示无法归属到具体客户端（如 xray 计数键在控制面无对应记录）。
type TrafficStat struct {
	ID        uint   `gorm:"primaryKey" json:"id"`
	NodeID    uint   `gorm:"uniqueIndex:uk_traffic;index" json:"node_id"`
	InboundID uint   `gorm:"uniqueIndex:uk_traffic;index" json:"inbound_id"`
	ClientID  uint   `gorm:"uniqueIndex:uk_traffic;index" json:"client_id"`
	Date      string `gorm:"size:10;uniqueIndex:uk_traffic;index" json:"date"` // YYYY-MM-DD (UTC)
	UpBytes   int64  `json:"up_bytes"`
	DownBytes int64  `json:"down_bytes"`
}
