package server

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/configgen"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// ListClients lists the accounts belonging to one inbound.
func ListClients(c *gin.Context) {
	id := c.Param("id")
	var clients []model.Client
	if err := store.DB.Where("inbound_id = ?", id).Find(&clients).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	c.JSON(200, clients)
}

func CreateClient(c *gin.Context) {
	inboundID := c.Param("id")
	var body struct {
		Alias             string `json:"alias"`
		TrafficLimitBytes int64  `json:"traffic_limit_bytes"`
		ExpireTime        string `json:"expire_time"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	trafficLimit := body.TrafficLimitBytes
	if trafficLimit < 0 {
		trafficLimit = 0
	}
	client := model.Client{
		InboundID:         uint(atoiSafe(inboundID)),
		UUID:              configgen.GenUUID(),
		Alias:             body.Alias,
		TrafficLimitBytes: trafficLimit,
		Enabled:           true,
	}
	if body.ExpireTime != "" {
		if t, err := time.Parse(time.RFC3339, body.ExpireTime); err != nil {
			c.JSON(400, gin.H{"error": "客户端到期时间格式不正确"})
			return
		} else {
			client.ExpireTime = t
		}
	}
	if err := store.DB.Create(&client).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	slog.Info("audit", "action", "client_create", "id", client.ID, "inbound_id", client.InboundID, "alias", client.Alias)
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
		Alias             *string `json:"alias"`
		TrafficLimitBytes *int64  `json:"traffic_limit_bytes"`
		Enabled           *bool   `json:"enabled"`
		ExpireTime        *string `json:"expire_time"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Alias != nil {
		updates["alias"] = *body.Alias
	}
	if body.TrafficLimitBytes != nil {
		limit := *body.TrafficLimitBytes
		if limit < 0 {
			limit = 0
		}
		updates["traffic_limit_bytes"] = limit
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	if body.ExpireTime != nil {
		if *body.ExpireTime == "" {
			updates["expire_time"] = time.Time{}
		} else if t, err := time.Parse(time.RFC3339, *body.ExpireTime); err == nil {
			updates["expire_time"] = t
		} else {
			c.JSON(400, gin.H{"error": "客户端到期时间格式不正确"})
			return
		}
	}
	if err := store.DB.Model(&client).Updates(updates).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	slog.Info("audit", "action", "client_update", "id", client.ID)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteClient(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Client{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": "internal error"})
		return
	}
	slog.Info("audit", "action", "client_delete", "id", id)
	c.JSON(200, gin.H{"ok": true})
}
