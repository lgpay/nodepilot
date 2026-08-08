package server

import (
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/configgen"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// ---- 入站 CRUD ----

func ListInbounds(c *gin.Context) {
	id := c.Param("id")
	var inbounds []model.Inbound
	store.DB.Where("node_id = ?", id).Find(&inbounds)
	c.JSON(200, inbounds)
}

func CreateInbound(c *gin.Context) {
	nodeID := c.Param("id")
	var body struct {
		Protocol       string `json:"protocol"`
		Port           int    `json:"port"`
		Transport      string `json:"transport"`
		TLSEnabled     bool   `json:"tls_enabled"`
		StreamSettings string `json:"stream_settings"` // JSON 字符串
		Fallback       string `json:"fallback"`        // JSON 字符串
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	in := model.Inbound{
		NodeID:         uint(atoiSafe(nodeID)),
		Protocol:       body.Protocol,
		Port:           body.Port,
		Transport:      body.Transport,
		TLSEnabled:     body.TLSEnabled,
		StreamSettings: body.StreamSettings,
		Fallback:       body.Fallback,
		Enabled:        true,
	}
	if err := store.DB.Create(&in).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"id": in.ID})
}

func UpdateInbound(c *gin.Context) {
	id := c.Param("id")
	var in model.Inbound
	if err := store.DB.First(&in, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "inbound not found"})
		return
	}
	var body struct {
		Protocol       *string `json:"protocol"`
		Port           *int    `json:"port"`
		Transport      *string `json:"transport"`
		TLSEnabled     *bool   `json:"tls_enabled"`
		StreamSettings *string `json:"stream_settings"`
		Fallback       *string `json:"fallback"`
		Enabled        *bool   `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Protocol != nil {
		updates["protocol"] = *body.Protocol
	}
	if body.Port != nil {
		updates["port"] = *body.Port
	}
	if body.Transport != nil {
		updates["transport"] = *body.Transport
	}
	if body.TLSEnabled != nil {
		updates["tls_enabled"] = *body.TLSEnabled
	}
	if body.StreamSettings != nil {
		updates["stream_settings"] = *body.StreamSettings
	}
	if body.Fallback != nil {
		updates["fallback"] = *body.Fallback
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	store.DB.Model(&in).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteInbound(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Inbound{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// ---- 用户/客户端 CRUD ----

func ListClients(c *gin.Context) {
	id := c.Param("id")
	var clients []model.Client
	store.DB.Where("inbound_id = ?", id).Find(&clients)
	c.JSON(200, clients)
}

func CreateClient(c *gin.Context) {
	inboundID := c.Param("id")
	var body struct {
		Email             string `json:"email"`
		TrafficLimitBytes int64  `json:"traffic_limit_bytes"` // -1 不限
		ExpireTime        string `json:"expire_time"`         // RFC3339，可空
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	client := model.Client{
		InboundID:         uint(atoiSafe(inboundID)),
		UUID:              configgen.GenUUID(),
		Email:             body.Email,
		TrafficLimitBytes: body.TrafficLimitBytes,
		Enabled:           true,
	}
	if body.ExpireTime != "" {
		if t, err := time.Parse(time.RFC3339, body.ExpireTime); err == nil {
			client.ExpireTime = t
		}
	}
	if err := store.DB.Create(&client).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, gin.H{"id": client.ID, "uuid": client.UUID})
}

func UpdateClient(c *gin.Context) {
	id := c.Param("id")
	var client model.Client
	if err := store.DB.First(&client, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "client not found"})
		return
	}
	var body struct {
		Email             *string `json:"email"`
		TrafficLimitBytes *int64  `json:"traffic_limit_bytes"`
		Enabled           *bool   `json:"enabled"`
		ExpireTime        *string `json:"expire_time"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Email != nil {
		updates["email"] = *body.Email
	}
	if body.TrafficLimitBytes != nil {
		updates["traffic_limit_bytes"] = *body.TrafficLimitBytes
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	if body.ExpireTime != nil && *body.ExpireTime != "" {
		if t, err := time.Parse(time.RFC3339, *body.ExpireTime); err == nil {
			updates["expire_time"] = t
		}
	}
	store.DB.Model(&client).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteClient(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Client{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// atoiSafe 将路径参数转为 uint 前的 int
func atoiSafe(s string) int {
	v, _ := strconv.Atoi(s)
	return v
}
