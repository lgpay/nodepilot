package server

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/model"
	"nodepilot/internal/notify"
	"nodepilot/internal/store"
)

// ---- 通知渠道 CRUD ----

func ListNotifiers(c *gin.Context) {
	var chs []model.NotificationChannel
	store.DB.Order("id").Find(&chs)
	c.JSON(http.StatusOK, chs)
}

func CreateNotifier(c *gin.Context) {
	var body struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Enabled bool   `json:"enabled"`
		Config  string `json:"config"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if body.Type != "email" && body.Type != "wecom" && body.Type != "tg" {
		c.JSON(400, gin.H{"error": "type 必须为 email / wecom / tg"})
		return
	}
	ch := model.NotificationChannel{
		Type:    body.Type,
		Name:    body.Name,
		Enabled: body.Enabled,
		Config:  body.Config,
	}
	if err := store.DB.Create(&ch).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(201, ch)
}

func GetNotifier(c *gin.Context) {
	var ch model.NotificationChannel
	if err := store.DB.First(&ch, c.Param("id")).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, ch)
}

func UpdateNotifier(c *gin.Context) {
	var ch model.NotificationChannel
	if err := store.DB.First(&ch, c.Param("id")).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	var body struct {
		Name    *string `json:"name"`
		Enabled *bool   `json:"enabled"`
		Config  *string `json:"config"`
		Type    *string `json:"type"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Name != nil {
		updates["name"] = *body.Name
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	if body.Config != nil {
		updates["config"] = *body.Config
	}
	if body.Type != nil {
		if *body.Type != "email" && *body.Type != "wecom" && *body.Type != "tg" {
			c.JSON(400, gin.H{"error": "type 必须为 email / wecom / tg"})
			return
		}
		updates["type"] = *body.Type
	}
	store.DB.Model(&ch).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteNotifier(c *gin.Context) {
	if err := store.DB.Delete(&model.NotificationChannel{}, c.Param("id")).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// TestNotifier 用当前配置立即发一条测试消息，返回发送结果（失败含原因）
func TestNotifier(c *gin.Context) {
	var ch model.NotificationChannel
	if err := store.DB.First(&ch, c.Param("id")).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	s, err := notify.BuildSender(ch)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if err := s.Send("NodePilot 测试通知", "这是一条来自 NodePilot 的测试消息，若收到说明渠道配置正确。"); err != nil {
		c.JSON(200, gin.H{"ok": false, "error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}
