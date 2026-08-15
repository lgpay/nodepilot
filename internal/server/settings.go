package server

import (
	"strconv"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/store"
)

// GetSettings 读取全局设置（当前仅 agent_heartbeat_interval，后续可扩展）
// GET /api/v1/settings （authed）
func GetSettings(c *gin.Context) {
	hb := 30
	if v, err := store.GetSetting("agent_heartbeat_interval"); err == nil && v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 5 && n <= 86400 {
			hb = n
		}
	}
	c.JSON(200, gin.H{"agent_heartbeat_interval": hb})
}

// UpdateSettings 保存全局设置
// PUT /api/v1/settings （authed）
func UpdateSettings(c *gin.Context) {
	var body struct {
		AgentHeartbeatInterval *int `json:"agent_heartbeat_interval"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if body.AgentHeartbeatInterval != nil {
		v := *body.AgentHeartbeatInterval
		if v < 5 || v > 86400 {
			c.JSON(400, gin.H{"error": "agent_heartbeat_interval 需在 5-86400 秒之间"})
			return
		}
		if err := store.SetSetting("agent_heartbeat_interval", strconv.Itoa(v)); err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
	}
	c.JSON(200, gin.H{"ok": true})
}
