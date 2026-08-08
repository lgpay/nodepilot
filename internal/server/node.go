package server

import (
	"time"

	"github.com/gin-gonic/gin"
	"nodepilot/internal/auth"
	"nodepilot/internal/model"
	"nodepilot/internal/store"
)

// getNode 按 id 取节点（id 可为数字主键）
func getNode(c *gin.Context, id string) (*model.Node, error) {
	var node model.Node
	if err := store.DB.First(&node, id).Error; err != nil {
		return nil, err
	}
	return &node, nil
}

// ---- 认证 ----

func LoginHandler(c *gin.Context) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	var admin model.Admin
	if err := store.DB.Where("username = ?", body.Username).First(&admin).Error; err != nil ||
		!auth.CheckPassword(body.Password, admin.PasswordHash) {
		c.JSON(401, gin.H{"error": "invalid credentials"})
		return
	}
	token, err := auth.IssueJWT(admin.Username)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"token": token})
}

func LogoutHandler(c *gin.Context) {
	// MVP：无服务端会话存储，由客户端丢弃 token
	c.JSON(200, gin.H{"ok": true})
}

// ---- 节点 CRUD ----

func CreateNode(c *gin.Context) {
	var body struct {
		Name      string `json:"name"`
		Address   string `json:"address"`
		Region    string `json:"region"`
		Tags      string `json:"tags"`
		PortRange string `json:"port_range"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	token := auth.GenToken()
	node := model.Node{
		Name:      body.Name,
		Address:   body.Address,
		Region:    body.Region,
		Tags:      body.Tags,
		PortRange: body.PortRange,
		Token:     token,
		Enabled:   true,
		Status:    "offline",
		Connectivity: "ok",
	}
	if err := store.DB.Create(&node).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	// token 仅在创建时明文返回一次，用于部署 agent
	c.JSON(201, gin.H{"id": node.ID, "token": token, "address": node.Address})
}

func ListNodes(c *gin.Context) {
	var nodes []model.Node
	// 不返回 Token 字段
	store.DB.Select("id,name,address,region,tags,enabled,status,connectivity,agent_version,last_heartbeat,port_range,created_at").
		Find(&nodes)
	c.JSON(200, nodes)
}

func GetNode(c *gin.Context) {
	id := c.Param("id")
	var node model.Node
	if err := store.DB.Select("id,name,address,region,tags,enabled,status,connectivity,agent_version,last_heartbeat,port_range,created_at").
		First(&node, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	var versions []model.ConfigVersion
	store.DB.Where("node_id = ?", node.ID).Order("version desc").Limit(20).Find(&versions)
	c.JSON(200, gin.H{"node": node, "config_versions": versions})
}

func UpdateNode(c *gin.Context) {
	id := c.Param("id")
	var node model.Node
	if err := store.DB.First(&node, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "node not found"})
		return
	}
	var body struct {
		Name      *string `json:"name"`
		Region    *string `json:"region"`
		Tags      *string `json:"tags"`
		Enabled   *bool   `json:"enabled"`
		PortRange *string `json:"port_range"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	updates := map[string]interface{}{}
	if body.Name != nil {
		updates["name"] = *body.Name
	}
	if body.Region != nil {
		updates["region"] = *body.Region
	}
	if body.Tags != nil {
		updates["tags"] = *body.Tags
	}
	if body.Enabled != nil {
		updates["enabled"] = *body.Enabled
	}
	if body.PortRange != nil {
		updates["port_range"] = *body.PortRange
	}
	store.DB.Model(&node).Updates(updates)
	c.JSON(200, gin.H{"ok": true})
}

func DeleteNode(c *gin.Context) {
	id := c.Param("id")
	if err := store.DB.Delete(&model.Node{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"ok": true})
}

// Heartbeat agent 上报状态
func Heartbeat(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		AgentVersion string  `json:"agent_version"`
		Cpu          float64 `json:"cpu"`
		Mem          float64 `json:"mem"`
		XrayRunning  bool    `json:"xray_running"`
	}
	c.ShouldBindJSON(&body)
	store.DB.Model(&model.Node{}).Where("id = ?", id).Updates(map[string]interface{}{
		"status":        "online",
		"agent_version": body.AgentVersion,
		"last_heartbeat": time.Now(),
	})
	c.JSON(200, gin.H{"ok": true})
}

// Traffic MVP 占位
func Traffic(c *gin.Context) {
	c.JSON(200, gin.H{"ok": true})
}
