package agent

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/auth"
	"nodepilot/internal/httputil"
)

// cfg 保存 agent 运行配置
var cfg AgentConfig

// heartbeatInterval 心跳间隔(秒)，可被控制面动态调整
var heartbeatInterval = 30
var heartbeatIntervalMu sync.RWMutex

func getHeartbeatInterval() int {
	heartbeatIntervalMu.RLock()
	defer heartbeatIntervalMu.RUnlock()
	return heartbeatInterval
}

func setHeartbeatInterval(v int) {
	heartbeatIntervalMu.Lock()
	heartbeatInterval = v
	heartbeatIntervalMu.Unlock()
}

// version agent 版本号，由 cmd/agent 通过 ldflags 注入（见 main.go）
var version = "0.1.0"

// SetVersion 设置 agent 版本号（构建期 -ldflags "-X nodepilot/internal/agent.version=..."）。
func SetVersion(v string) {
	if v != "" {
		version = v
	}
}

// AgentConfig agent 启动参数
type AgentConfig struct {
	Token     string
	Addr      string
	ServerURL string
	NodeID    string
	ConfigDir string
	CertDir   string
}

// SetConfig 设置 agent 配置
func SetConfig(c AgentConfig) {
	cfg = c
}

// TokenMiddleware 校验管理端下发的 Bearer token
func TokenMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		authz := c.GetHeader("Authorization")
		t := strings.TrimPrefix(authz, "Bearer ")
		if !auth.CheckNodeToken(t, auth.TokenHash(cfg.Token)) {
			c.AbortWithStatusJSON(401, gin.H{"error": "unauthorized"})
			return
		}
		c.Next()
	}
}

// RegisterRoutes 注册 agent HTTP 路由
func RegisterRoutes(r *gin.Engine) {
	ag := r.Group("/agent/v1")
	ag.Use(TokenMiddleware())
	{
		ag.PUT("/config", PutConfig)
		ag.PUT("/cert", PutCert)
		ag.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
		ag.GET("/status", GetStatus)
	}
}

// PutCert 接收管理端下发的证书文件内容并落盘
func PutCert(c *gin.Context) {
	var body struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
		CAPEM   string `json:"ca_pem"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": "invalid request body"})
		return
	}
	if body.CertPEM == "" || body.KeyPEM == "" {
		c.JSON(400, gin.H{"error": "cert_pem and key_pem required"})
		return
	}
	if err := ReceiveCert(body.CertPEM, body.KeyPEM, body.CAPEM); err != nil {
		log.Printf("[agent] save cert failed: %v", err)
		c.JSON(500, gin.H{"error": "failed to save certificate"})
		return
	}
	c.JSON(200, gin.H{"ok": true, "paths": CertPaths()})
}

// PutConfig 接收管理端下发的 xray 配置：写盘 + 重启 xray
func PutConfig(c *gin.Context) {
	var body struct {
		Version    int             `json:"version"`
		XrayConfig json.RawMessage `json:"xray_config"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": "invalid request body"})
		return
	}
	if err := os.MkdirAll(cfg.ConfigDir, 0755); err != nil {
		log.Printf("[agent] mkdir config dir failed: %v", err)
		c.JSON(500, gin.H{"error": "failed to prepare config dir"})
		return
	}
	path := filepath.Join(cfg.ConfigDir, "config.json")
	// 先写临时文件并校验，通过后才落正式路径，避免坏配置留在磁盘并在重启后拉起。
	// 注意：临时文件必须以 .json 结尾——xray 按文件扩展名识别配置格式，
	// 若用 .tmp 后缀，xray run -test 会报 "Failed to get format" 导致校验永远失败、
	// 配置永远无法下发（见节点自愈/端口变更全部失效的问题）。
	tmpPath := filepath.Join(cfg.ConfigDir, "config.tmp.json")
	if err := os.WriteFile(tmpPath, body.XrayConfig, 0644); err != nil {
		log.Printf("[agent] write temp config failed: %v", err)
		c.JSON(500, gin.H{"error": "failed to write config"})
		return
	}
	if err := Validate(tmpPath); err != nil {
		_ = os.Remove(tmpPath)
		log.Printf("[agent] config validation failed: %v", err)
		c.JSON(400, gin.H{"error": "config validation failed", "detail": err.Error(), "version": body.Version})
		return
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		log.Printf("[agent] write config failed: %v", err)
		c.JSON(500, gin.H{"error": "failed to write config"})
		return
	}
	if err := Restart(path); err != nil {
		log.Printf("[agent] xray start failed: %v", err)
		c.JSON(500, gin.H{"error": "config written but xray start failed", "version": body.Version})
		return
	}
	c.JSON(200, gin.H{"accepted": true, "version": body.Version})
}

// GetStatus 返回 agent / xray 状态
func GetStatus(c *gin.Context) {
	c.JSON(200, gin.H{
		"agent_version": version,
		"xray_running":  IsXrayRunning(),
		"config_path":   configPath,
	})
}

// StartHeartbeat 周期向管理端上报心跳，同时做 xray 进程看护（崩溃自动拉起）。
// 控制面可在心跳响应中下发新的间隔，这里动态调整，无需重启 agent。
func StartHeartbeat(interval time.Duration) {
	setHeartbeatInterval(int(interval / time.Second))
	go func() {
		EnsureXrayRunning()
		postHeartbeat()
		for {
			time.Sleep(time.Duration(getHeartbeatInterval()) * time.Second)
			EnsureXrayRunning()
			postHeartbeat()
		}
	}()
}

func postHeartbeat() {
	body, _ := json.Marshal(map[string]interface{}{
		"agent_version":       version,
		"cpu":                 0.0,
		"mem":                 0.0,
		"xray_running":        IsXrayRunning(),
		"heartbeat_interval":  getHeartbeatInterval(),
		"timestamp":           time.Now().Format(time.RFC3339),
	})
	url := strings.TrimRight(cfg.ServerURL, "/") + "/api/v1/nodes/" + cfg.NodeID + "/heartbeat"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[agent] heartbeat build error: %v", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.Token)
	client := httputil.AgentClient(10 * time.Second)
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[agent] heartbeat failed: %v", err)
		return
	}
	defer resp.Body.Close()
	// 控制面在响应中返回期望的心跳间隔，动态调整本地定时（无需重启）
	var r struct {
		HeartbeatInterval int `json:"heartbeat_interval"`
	}
	if resp.StatusCode == 200 {
		if dec := json.NewDecoder(resp.Body); dec.Decode(&r) == nil && r.HeartbeatInterval >= 5 && r.HeartbeatInterval <= 86400 {
			if r.HeartbeatInterval != getHeartbeatInterval() {
				log.Printf("[agent] heartbeat interval adjusted to %ds by control plane", r.HeartbeatInterval)
				setHeartbeatInterval(r.HeartbeatInterval)
			}
		}
	}
}
